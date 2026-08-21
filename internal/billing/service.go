package billing

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrAccountNotFound   = errors.New("billing account not found")
	ErrInsufficientFunds = errors.New("insufficient local test credits")
	ErrReservationState  = errors.New("billing reservation is not actionable")
	ErrInvalidAdjustment = errors.New("invalid billing adjustment")
	ErrInvalidStatement  = errors.New("invalid billing statement filters")
)

type Account struct {
	UserID         uuid.UUID `json:"userId"`
	Currency       string    `json:"currency"`
	BalanceCents   int64     `json:"balanceCents"`
	ReservedCents  int64     `json:"reservedCents"`
	AvailableCents int64     `json:"availableCents"`
	PaymentMode    string    `json:"paymentMode"`
	Version        int64     `json:"version"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type Entry struct {
	ID                uuid.UUID      `json:"id"`
	OperationID       uuid.UUID      `json:"operationId"`
	EntryType         string         `json:"entryType"`
	Direction         string         `json:"direction"`
	AmountCents       int            `json:"amountCents"`
	Currency          string         `json:"currency"`
	BalanceAfterCents int64          `json:"balanceAfterCents"`
	Description       string         `json:"description"`
	Metadata          map[string]any `json:"metadata"`
	CreatedAt         time.Time      `json:"createdAt"`
}

type Statement struct {
	Account    Account `json:"account"`
	Entries    []Entry `json:"entries"`
	NextCursor *string `json:"nextCursor,omitempty"`
}

type StatementInput struct {
	Direction string
	EntryType string
	DateFrom  *time.Time
	DateTo    *time.Time
	Cursor    string
	Limit     int
}

type statementCursor struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        uuid.UUID `json:"id"`
}

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func (s *Service) Statement(ctx context.Context, userID uuid.UUID, input StatementInput) (Statement, error) {
	account, err := GetAccount(ctx, s.pool, userID, "USD")
	if err != nil {
		return Statement{}, err
	}
	input.Direction = strings.TrimSpace(strings.ToLower(input.Direction))
	input.EntryType = strings.TrimSpace(strings.ToLower(input.EntryType))
	if (input.Direction != "" && input.Direction != "debit" && input.Direction != "credit") ||
		(input.EntryType != "" && !validEntryType(input.EntryType)) ||
		(input.DateFrom != nil && input.DateTo != nil && input.DateFrom.After(*input.DateTo)) {
		return Statement{}, ErrInvalidStatement
	}
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Limit < 1 || input.Limit > 50 {
		return Statement{}, ErrInvalidStatement
	}
	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if input.Cursor != "" {
		cursor, err := decodeStatementCursor(input.Cursor)
		if err != nil {
			return Statement{}, ErrInvalidStatement
		}
		cursorTime, cursorID = &cursor.CreatedAt, &cursor.ID
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id,operation_id,entry_type,direction,amount_cents,currency,balance_after_cents,description,metadata,created_at
		FROM billing_entries
		WHERE user_id=$1 AND currency=$2
		  AND ($3='' OR direction=$3)
		  AND ($4='' OR entry_type=$4)
		  AND ($5::timestamptz IS NULL OR created_at >= $5)
		  AND ($6::timestamptz IS NULL OR created_at <= $6)
		  AND ($7::timestamptz IS NULL OR (created_at,id) < ($7,$8::uuid))
		ORDER BY created_at DESC,id DESC LIMIT $9`, userID, account.Currency, input.Direction, input.EntryType, input.DateFrom, input.DateTo, cursorTime, cursorID, input.Limit+1)
	if err != nil {
		return Statement{}, fmt.Errorf("list billing entries: %w", err)
	}
	defer rows.Close()
	entries := make([]Entry, 0)
	for rows.Next() {
		var item Entry
		if err := rows.Scan(&item.ID, &item.OperationID, &item.EntryType, &item.Direction, &item.AmountCents,
			&item.Currency, &item.BalanceAfterCents, &item.Description, &item.Metadata, &item.CreatedAt); err != nil {
			return Statement{}, fmt.Errorf("scan billing entry: %w", err)
		}
		entries = append(entries, item)
	}
	if err := rows.Err(); err != nil {
		return Statement{}, fmt.Errorf("iterate billing entries: %w", err)
	}
	statement := Statement{Account: account, Entries: entries}
	if len(statement.Entries) > input.Limit {
		statement.Entries = statement.Entries[:input.Limit]
		cursor := encodeStatementCursor(statement.Entries[len(statement.Entries)-1])
		statement.NextCursor = &cursor
	}
	return statement, nil
}

func validEntryType(value string) bool {
	switch value {
	case "generation_charge", "product_purchase", "product_sale", "product_refund", "task_payment", "task_earning", "admin_adjustment", "initial_credit", "subscription_purchase":
		return true
	default:
		return false
	}
}

func encodeStatementCursor(item Entry) string {
	body, _ := json.Marshal(statementCursor{CreatedAt: item.CreatedAt, ID: item.ID})
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodeStatementCursor(value string) (statementCursor, error) {
	var cursor statementCursor
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &cursor) != nil || cursor.ID == uuid.Nil || cursor.CreatedAt.IsZero() {
		return statementCursor{}, ErrInvalidStatement
	}
	return cursor, nil
}

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func GetAccount(ctx context.Context, q rowQuerier, userID uuid.UUID, currency string) (Account, error) {
	var item Account
	err := q.QueryRow(ctx, `
		SELECT user_id,currency,balance_cents,reserved_cents,balance_cents-reserved_cents,payment_mode,version,updated_at
		FROM billing_accounts WHERE user_id=$1 AND currency=$2`, userID, currency).Scan(
		&item.UserID, &item.Currency, &item.BalanceCents, &item.ReservedCents, &item.AvailableCents,
		&item.PaymentMode, &item.Version, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrAccountNotFound
	}
	if err != nil {
		return Account{}, fmt.Errorf("get billing account: %w", err)
	}
	return item, nil
}

func ReserveTx(ctx context.Context, tx pgx.Tx, userID, operationID uuid.UUID, amount int, currency string) error {
	if amount <= 0 || currency != "USD" {
		return ErrInvalidAdjustment
	}
	var existingUser uuid.UUID
	var existingAmount int
	var status string
	err := tx.QueryRow(ctx, `SELECT user_id,amount_cents,status FROM billing_reservations WHERE operation_type='generation' AND operation_id=$1`, operationID).
		Scan(&existingUser, &existingAmount, &status)
	if err == nil {
		if existingUser == userID && existingAmount == amount && (status == "held" || status == "captured") {
			return nil
		}
		return ErrReservationState
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("load billing reservation: %w", err)
	}

	var balance, reserved int64
	if err := tx.QueryRow(ctx, `SELECT balance_cents,reserved_cents FROM billing_accounts WHERE user_id=$1 AND currency=$2 FOR UPDATE`, userID, currency).
		Scan(&balance, &reserved); errors.Is(err, pgx.ErrNoRows) {
		return ErrAccountNotFound
	} else if err != nil {
		return fmt.Errorf("lock billing account: %w", err)
	}
	if balance-reserved < int64(amount) {
		return ErrInsufficientFunds
	}
	if _, err := tx.Exec(ctx, `UPDATE billing_accounts SET reserved_cents=reserved_cents+$3,version=version+1,updated_at=now() WHERE user_id=$1 AND currency=$2`, userID, currency, amount); err != nil {
		return fmt.Errorf("reserve billing credits: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO billing_reservations(user_id,operation_type,operation_id,amount_cents,currency)
		VALUES($1,'generation',$2,$3,$4)`, userID, operationID, amount, currency); err != nil {
		return fmt.Errorf("record billing reservation: %w", err)
	}
	return nil
}

func CaptureGenerationTx(ctx context.Context, tx pgx.Tx, generationID uuid.UUID, description string) error {
	var userID uuid.UUID
	var amount int
	var currency, status string
	err := tx.QueryRow(ctx, `
		SELECT user_id,amount_cents,currency,status FROM billing_reservations
		WHERE operation_type='generation' AND operation_id=$1 FOR UPDATE`, generationID).
		Scan(&userID, &amount, &currency, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAccountNotFound
	}
	if err != nil {
		return fmt.Errorf("lock generation reservation: %w", err)
	}
	if status == "captured" {
		return nil
	}
	if status != "held" {
		return ErrReservationState
	}
	var balanceAfter int64
	err = tx.QueryRow(ctx, `
		UPDATE billing_accounts
		SET balance_cents=balance_cents-$3,reserved_cents=reserved_cents-$3,version=version+1,updated_at=now()
		WHERE user_id=$1 AND currency=$2 AND reserved_cents >= $3 AND balance_cents >= $3
		RETURNING balance_cents`, userID, currency, amount).Scan(&balanceAfter)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrReservationState
	}
	if err != nil {
		return fmt.Errorf("capture generation credits: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE billing_reservations SET status='captured',updated_at=now() WHERE operation_type='generation' AND operation_id=$1`, generationID); err != nil {
		return fmt.Errorf("complete billing reservation: %w", err)
	}
	description = strings.TrimSpace(description)
	if description == "" {
		description = "Local Test image generation"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO billing_entries(user_id,operation_id,entry_type,direction,amount_cents,currency,balance_after_cents,description)
		VALUES($1,$2,'generation_charge','debit',$3,$4,$5,$6)
		ON CONFLICT (user_id,operation_id,entry_type,direction) DO NOTHING`, userID, generationID, amount, currency, balanceAfter, description); err != nil {
		return fmt.Errorf("record generation charge: %w", err)
	}
	return nil
}

func ReleaseGenerationTx(ctx context.Context, tx pgx.Tx, generationID uuid.UUID, reason string) error {
	var userID uuid.UUID
	var amount int
	var currency, status string
	err := tx.QueryRow(ctx, `
		SELECT user_id,amount_cents,currency,status FROM billing_reservations
		WHERE operation_type='generation' AND operation_id=$1 FOR UPDATE`, generationID).
		Scan(&userID, &amount, &currency, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lock generation reservation: %w", err)
	}
	if status == "released" {
		return nil
	}
	if status != "held" {
		return ErrReservationState
	}
	if _, err := tx.Exec(ctx, `
		UPDATE billing_accounts SET reserved_cents=reserved_cents-$3,version=version+1,updated_at=now()
		WHERE user_id=$1 AND currency=$2 AND reserved_cents >= $3`, userID, currency, amount); err != nil {
		return fmt.Errorf("release generation credits: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE billing_reservations SET status='released',release_reason=$2,updated_at=now()
		WHERE operation_type='generation' AND operation_id=$1`, generationID, strings.TrimSpace(reason)); err != nil {
		return fmt.Errorf("record generation release: %w", err)
	}
	return nil
}

func TransferTx(ctx context.Context, tx pgx.Tx, fromUserID, toUserID, operationID uuid.UUID, amount int, currency, debitType, creditType, description string) error {
	if amount <= 0 || currency != "USD" || fromUserID == toUserID {
		return ErrInvalidAdjustment
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM billing_entries WHERE user_id=$1 AND operation_id=$2 AND entry_type=$3 AND direction='debit')`, fromUserID, operationID, debitType).Scan(&exists); err != nil {
		return fmt.Errorf("check billing transfer replay: %w", err)
	}
	if exists {
		return nil
	}
	rows, err := tx.Query(ctx, `
		SELECT user_id,balance_cents,reserved_cents FROM billing_accounts
		WHERE user_id=ANY($1) AND currency=$2 ORDER BY user_id FOR UPDATE`, []uuid.UUID{fromUserID, toUserID}, currency)
	if err != nil {
		return fmt.Errorf("lock transfer accounts: %w", err)
	}
	balances := map[uuid.UUID][2]int64{}
	for rows.Next() {
		var id uuid.UUID
		var balance, reserved int64
		if err := rows.Scan(&id, &balance, &reserved); err != nil {
			rows.Close()
			return fmt.Errorf("scan transfer account: %w", err)
		}
		balances[id] = [2]int64{balance, reserved}
	}
	rows.Close()
	if len(balances) != 2 {
		return ErrAccountNotFound
	}
	from := balances[fromUserID]
	if from[0]-from[1] < int64(amount) {
		return ErrInsufficientFunds
	}
	fromAfter := from[0] - int64(amount)
	toAfter := balances[toUserID][0] + int64(amount)
	if _, err := tx.Exec(ctx, `UPDATE billing_accounts SET balance_cents=$3,version=version+1,updated_at=now() WHERE user_id=$1 AND currency=$2`, fromUserID, currency, fromAfter); err != nil {
		return fmt.Errorf("debit billing account: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE billing_accounts SET balance_cents=$3,version=version+1,updated_at=now() WHERE user_id=$1 AND currency=$2`, toUserID, currency, toAfter); err != nil {
		return fmt.Errorf("credit billing account: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO billing_entries(user_id,operation_id,entry_type,direction,amount_cents,currency,balance_after_cents,description)
		VALUES($1,$3,$4,'debit',$5,$6,$7,$9),($2,$3,$8,'credit',$5,$6,$10,$9)`,
		fromUserID, toUserID, operationID, debitType, amount, currency, fromAfter, creditType, description, toAfter); err != nil {
		return fmt.Errorf("record billing transfer: %w", err)
	}
	return nil
}

func AdjustTx(ctx context.Context, tx pgx.Tx, userID, operationID uuid.UUID, deltaCents int, currency, description string, metadata map[string]any) (Account, error) {
	if deltaCents == 0 || currency != "USD" || len(strings.TrimSpace(description)) < 5 {
		return Account{}, ErrInvalidAdjustment
	}
	var balance, reserved int64
	if err := tx.QueryRow(ctx, `SELECT balance_cents,reserved_cents FROM billing_accounts WHERE user_id=$1 AND currency=$2 FOR UPDATE`, userID, currency).Scan(&balance, &reserved); errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrAccountNotFound
	} else if err != nil {
		return Account{}, fmt.Errorf("lock adjusted account: %w", err)
	}
	after := balance + int64(deltaCents)
	if after < reserved || after < 0 {
		return Account{}, ErrInsufficientFunds
	}
	if _, err := tx.Exec(ctx, `UPDATE billing_accounts SET balance_cents=$3,version=version+1,updated_at=now() WHERE user_id=$1 AND currency=$2`, userID, currency, after); err != nil {
		return Account{}, fmt.Errorf("adjust billing account: %w", err)
	}
	direction := "credit"
	amount := deltaCents
	if deltaCents < 0 {
		direction = "debit"
		amount = -deltaCents
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO billing_entries(user_id,operation_id,entry_type,direction,amount_cents,currency,balance_after_cents,description,metadata)
		VALUES($1,$2,'admin_adjustment',$3,$4,$5,$6,$7,$8)`, userID, operationID, direction, amount, currency, after, strings.TrimSpace(description), metadata); err != nil {
		return Account{}, fmt.Errorf("record billing adjustment: %w", err)
	}
	return GetAccount(ctx, tx, userID, currency)
}
