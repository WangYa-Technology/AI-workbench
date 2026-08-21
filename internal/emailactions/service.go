package emailactions

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

const (
	DeliveryJobKind = "identity.email_action.deliver"
	ExpiryJobKind   = "identity.email_action.expire"
	VerifyEmail     = "verify_email"
	PasswordReset   = "password_reset"
)

var (
	ErrInvalid                 = errors.New("invalid email action input")
	ErrNotFound                = errors.New("email action not found")
	ErrConflict                = errors.New("email action state conflict")
	ErrExpired                 = errors.New("email action expired")
	ErrAlreadyVerified         = errors.New("email is already verified")
	ErrInvalidDeadLetterFilter = errors.New("invalid email dead-letter filter")
	ErrInvalidOwnerFilter      = errors.New("invalid owner email-action filter")
)

type Action struct {
	ID               uuid.UUID  `json:"id"`
	Kind             string     `json:"kind"`
	Status           string     `json:"status"`
	EmailHint        string     `json:"emailHint"`
	Locale           string     `json:"locale"`
	OwnerHandle      string     `json:"ownerHandle,omitempty"`
	Version          int        `json:"version"`
	AttemptCount     int        `json:"attemptCount"`
	OriginalActionID *uuid.UUID `json:"originalActionId,omitempty"`
	ExpiresAt        time.Time  `json:"expiresAt"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
	DeliveredAt      *time.Time `json:"deliveredAt,omitempty"`
	ConsumedAt       *time.Time `json:"consumedAt,omitempty"`
	CancelledAt      *time.Time `json:"cancelledAt,omitempty"`
	DeadLetteredAt   *time.Time `json:"deadLetteredAt,omitempty"`
	Attempts         []Attempt  `json:"attempts"`
}

type Attempt struct {
	AttemptNumber int       `json:"attemptNumber"`
	Adapter       string    `json:"adapter"`
	Status        string    `json:"status"`
	ErrorCode     *string   `json:"errorCode,omitempty"`
	ReceiptSHA256 *string   `json:"receiptSha256,omitempty"`
	AttemptedAt   time.Time `json:"attemptedAt"`
}

type Transition struct {
	ExpectedVersion int `json:"expectedVersion"`
}

type DeadLetterListInput struct {
	Query  string
	Kind   string
	Cursor string
	Limit  int
}

type OwnerListInput struct {
	Cursor string
	Limit  int
}

type ActionPage struct {
	Items      []Action `json:"items"`
	NextCursor *string  `json:"nextCursor,omitempty"`
}

type actionCursor struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        uuid.UUID `json:"id"`
}

type Service struct {
	pool      *pgxpool.Pool
	key       []byte
	mode      string
	mailRoot  string
	webOrigin string
	now       func() time.Time
}

func NewService(pool *pgxpool.Pool, key []byte, mode, mediaRoot, webOrigin string) *Service {
	return &Service{pool: pool, key: append([]byte(nil), key...), mode: mode, mailRoot: filepath.Join(mediaRoot, "mailbox"), webOrigin: strings.TrimRight(webOrigin, "/"), now: time.Now}
}

func (s *Service) RequestVerification(ctx context.Context, userID uuid.UUID, requestID string) (Action, error) {
	var email, locale string
	var verifiedAt *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT email,locale,email_verified_at FROM users WHERE id=$1 AND status='active'`, userID).Scan(&email, &locale, &verifiedAt); errors.Is(err, pgx.ErrNoRows) {
		return Action{}, ErrNotFound
	} else if err != nil {
		return Action{}, err
	}
	if verifiedAt != nil {
		return Action{}, ErrAlreadyVerified
	}
	return s.create(ctx, userID, email, locale, VerifyEmail, 24*time.Hour, &userID, requestID)
}

// RequestPasswordReset deliberately returns no existence signal for unknown or inactive accounts.
func (s *Service) RequestPasswordReset(ctx context.Context, email, requestID string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if len(email) < 3 || len(email) > 254 || !strings.Contains(email, "@") {
		return nil
	}
	var userID uuid.UUID
	var storedEmail, locale string
	err := s.pool.QueryRow(ctx, `SELECT id,email,locale FROM users WHERE lower(email)=$1 AND status='active'`, email).Scan(&userID, &storedEmail, &locale)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = s.create(ctx, userID, storedEmail, locale, PasswordReset, time.Hour, nil, requestID)
	return err
}

func (s *Service) create(ctx context.Context, userID uuid.UUID, email, locale, kind string, lifetime time.Duration, actorID *uuid.UUID, requestID string) (Action, error) {
	if len(s.key) != 32 || (kind != VerifyEmail && kind != PasswordReset) || (locale != "en-US" && locale != "zh-CN") {
		return Action{}, ErrInvalid
	}
	token, err := newToken()
	if err != nil {
		return Action{}, err
	}
	actionID := uuid.New()
	nonce, ciphertext, err := s.encrypt(actionID, kind, token)
	if err != nil {
		return Action{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Action{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `UPDATE identity_email_actions SET status='cancelled',token_hash=NULL,token_nonce=NULL,token_ciphertext=NULL,cancelled_at=now(),updated_at=now(),version=version+1 WHERE user_id=$1 AND kind=$2 AND status IN ('queued','delivered') RETURNING id`, userID, kind)
	if err != nil {
		return Action{}, err
	}
	staleIDs := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return Action{}, err
		}
		staleIDs = append(staleIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Action{}, err
	}
	if len(staleIDs) > 0 {
		if _, err := tx.Exec(ctx, `UPDATE jobs SET status='cancelled',updated_at=now() WHERE kind=$1 AND status IN ('queued','running') AND payload->>'actionId'=ANY($2::text[])`, DeliveryJobKind, uuidStrings(staleIDs)); err != nil {
			return Action{}, err
		}
	}
	expiresAt := s.now().UTC().Add(lifetime)
	var item Action
	err = tx.QueryRow(ctx, `INSERT INTO identity_email_actions(id,user_id,kind,email_snapshot,locale,token_hash,token_nonce,token_ciphertext,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id,kind,status,locale,version,attempt_count,expires_at,created_at,updated_at,delivered_at,consumed_at,cancelled_at,dead_lettered_at`, actionID, userID, kind, email, locale, hashToken(token), nonce, ciphertext, expiresAt).Scan(&item.ID, &item.Kind, &item.Status, &item.Locale, &item.Version, &item.AttemptCount, &item.ExpiresAt, &item.CreatedAt, &item.UpdatedAt, &item.DeliveredAt, &item.ConsumedAt, &item.CancelledAt, &item.DeadLetteredAt)
	if err != nil {
		return Action{}, err
	}
	item.EmailHint = maskEmail(email)
	item.Attempts = []Attempt{}
	payload, _ := json.Marshal(map[string]any{"actionId": actionID})
	if _, err := tx.Exec(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,$2,5),($3,$2,3)`, DeliveryJobKind, payload, ExpiryJobKind); err != nil {
		return Action{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE jobs SET available_at=$2 WHERE kind=$1 AND payload->>'actionId'=$3 AND status='queued'`, ExpiryJobKind, expiresAt, actionID.String()); err != nil {
		return Action{}, err
	}
	if err := audit(ctx, tx, actorID, "identity."+kind+"_requested", "identity_email_action", actionID, "", requestID, map[string]any{"kind": kind}); err != nil {
		return Action{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Action{}, err
	}
	for _, id := range staleIDs {
		_ = s.removeMailbox(userID, id)
	}
	return item, nil
}

func (s *Service) ConfirmVerification(ctx context.Context, token, requestID string) error {
	_, _, err := s.consume(ctx, token, VerifyEmail, "", requestID)
	return err
}

func (s *Service) ConfirmPasswordReset(ctx context.Context, token, password, requestID string) (int64, error) {
	if len(password) < 10 || len(password) > 128 {
		return 0, ErrInvalid
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return 0, err
	}
	_, revoked, err := s.consume(ctx, token, PasswordReset, string(passwordHash), requestID)
	return revoked, err
}

func (s *Service) consume(ctx context.Context, token, kind, passwordHash, requestID string) (uuid.UUID, int64, error) {
	if len(token) < 32 || len(token) > 200 {
		return uuid.Nil, 0, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var actionID, userID uuid.UUID
	var status string
	var expiresAt time.Time
	err = tx.QueryRow(ctx, `SELECT id,user_id,status,expires_at FROM identity_email_actions WHERE token_hash=$1 AND kind=$2 FOR UPDATE`, hashToken(token), kind).Scan(&actionID, &userID, &status, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, 0, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, 0, err
	}
	if !expiresAt.After(s.now()) {
		_, _ = tx.Exec(ctx, `UPDATE identity_email_actions SET status='expired',token_hash=NULL,token_nonce=NULL,token_ciphertext=NULL,updated_at=now(),version=version+1 WHERE id=$1`, actionID)
		_ = tx.Commit(ctx)
		_ = s.removeMailbox(userID, actionID)
		return uuid.Nil, 0, ErrExpired
	}
	if status != "queued" && status != "delivered" {
		return uuid.Nil, 0, ErrConflict
	}
	var revoked int64
	if kind == VerifyEmail {
		if _, err := tx.Exec(ctx, `UPDATE users SET email_verified_at=COALESCE(email_verified_at,now()),updated_at=now() WHERE id=$1 AND status='active'`, userID); err != nil {
			return uuid.Nil, 0, err
		}
	} else {
		if _, err := tx.Exec(ctx, `UPDATE users SET password_hash=$2,updated_at=now() WHERE id=$1 AND status='active'`, userID, passwordHash); err != nil {
			return uuid.Nil, 0, err
		}
		result, err := tx.Exec(ctx, `UPDATE sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, userID)
		if err != nil {
			return uuid.Nil, 0, err
		}
		revoked = result.RowsAffected()
	}
	if _, err := tx.Exec(ctx, `UPDATE identity_email_actions SET status='consumed',token_hash=NULL,token_nonce=NULL,token_ciphertext=NULL,consumed_at=now(),updated_at=now(),version=version+1 WHERE id=$1`, actionID); err != nil {
		return uuid.Nil, 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE jobs SET status='cancelled',updated_at=now() WHERE kind IN ($1,$2) AND status IN ('queued','running') AND payload->>'actionId'=$3`, DeliveryJobKind, ExpiryJobKind, actionID.String()); err != nil {
		return uuid.Nil, 0, err
	}
	metadata := map[string]any{"kind": kind}
	if kind == PasswordReset {
		metadata["revokedSessions"] = revoked
	}
	if err := audit(ctx, tx, &userID, "identity."+kind+"_consumed", "identity_email_action", actionID, "", requestID, metadata); err != nil {
		return uuid.Nil, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, 0, err
	}
	_ = s.removeMailbox(userID, actionID)
	return userID, revoked, nil
}

func (s *Service) ListForUser(ctx context.Context, userID uuid.UUID, input OwnerListInput) (ActionPage, error) {
	if input.Limit == 0 {
		input.Limit = 5
	}
	if userID == uuid.Nil || input.Limit < 1 || input.Limit > 50 {
		return ActionPage{}, ErrInvalidOwnerFilter
	}
	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if input.Cursor != "" {
		cursor, err := decodeOwnerActionCursor(input.Cursor)
		if err != nil {
			return ActionPage{}, err
		}
		cursorTime, cursorID = &cursor.CreatedAt, &cursor.ID
	}
	rows, err := s.pool.Query(ctx, actionSelect+`
		WHERE a.user_id=$1
		  AND ($2::timestamptz IS NULL OR (a.created_at,a.id)<($2,$3::uuid))
		ORDER BY a.created_at DESC,a.id DESC LIMIT $4`, userID, cursorTime, cursorID, input.Limit+1)
	if err != nil {
		return ActionPage{}, fmt.Errorf("list owner identity email actions: %w", err)
	}
	defer rows.Close()
	items := make([]Action, 0)
	for rows.Next() {
		item, err := scanAction(rows)
		if err != nil {
			return ActionPage{}, err
		}
		item.Attempts, err = s.listAttempts(ctx, item.ID)
		if err != nil {
			return ActionPage{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return ActionPage{}, err
	}
	page := ActionPage{Items: items}
	if len(page.Items) > input.Limit {
		page.Items = page.Items[:input.Limit]
		cursor := encodeActionCursor(page.Items[len(page.Items)-1])
		page.NextCursor = &cursor
	}
	return page, nil
}

func (s *Service) ListDeadLetters(ctx context.Context, input DeadLetterListInput) (ActionPage, error) {
	input.Query = strings.ToLower(strings.TrimSpace(input.Query))
	input.Kind = strings.ToLower(strings.TrimSpace(input.Kind))
	if len(input.Query) > 120 || (input.Kind != "" && input.Kind != VerifyEmail && input.Kind != PasswordReset) {
		return ActionPage{}, ErrInvalidDeadLetterFilter
	}
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Limit < 1 || input.Limit > 50 {
		return ActionPage{}, ErrInvalidDeadLetterFilter
	}
	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if input.Cursor != "" {
		cursor, err := decodeActionCursor(input.Cursor)
		if err != nil {
			return ActionPage{}, err
		}
		cursorTime, cursorID = &cursor.CreatedAt, &cursor.ID
	}
	rows, err := s.pool.Query(ctx, actionSelect+`
		WHERE a.status='dead_letter'
		  AND ($1='' OR strpos(lower(u.handle),$1)>0 OR strpos(lower(a.email_snapshot),$1)>0)
		  AND ($2='' OR a.kind=$2)
		  AND ($3::timestamptz IS NULL OR (a.created_at,a.id)<($3,$4::uuid))
		ORDER BY a.created_at DESC,a.id DESC LIMIT $5`, input.Query, input.Kind, cursorTime, cursorID, input.Limit+1)
	if err != nil {
		return ActionPage{}, fmt.Errorf("list identity email dead letters: %w", err)
	}
	defer rows.Close()
	items := make([]Action, 0)
	for rows.Next() {
		item, err := scanAction(rows)
		if err != nil {
			return ActionPage{}, err
		}
		item.Attempts, err = s.listAttempts(ctx, item.ID)
		if err != nil {
			return ActionPage{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return ActionPage{}, err
	}
	page := ActionPage{Items: items}
	if len(page.Items) > input.Limit {
		page.Items = page.Items[:input.Limit]
		cursor := encodeActionCursor(page.Items[len(page.Items)-1])
		page.NextCursor = &cursor
	}
	return page, nil
}

const actionSelect = `SELECT a.id,a.kind,a.status,a.email_snapshot,a.locale,u.handle,a.version,a.attempt_count,a.original_action_id,a.expires_at,a.created_at,a.updated_at,a.delivered_at,a.consumed_at,a.cancelled_at,a.dead_lettered_at FROM identity_email_actions a JOIN users u ON u.id=a.user_id `

func scanAction(row pgx.Row) (Action, error) {
	var item Action
	var email string
	if err := row.Scan(&item.ID, &item.Kind, &item.Status, &email, &item.Locale, &item.OwnerHandle, &item.Version, &item.AttemptCount, &item.OriginalActionID, &item.ExpiresAt, &item.CreatedAt, &item.UpdatedAt, &item.DeliveredAt, &item.ConsumedAt, &item.CancelledAt, &item.DeadLetteredAt); err != nil {
		return Action{}, err
	}
	item.EmailHint = maskEmail(email)
	return item, nil
}

func encodeActionCursor(item Action) string {
	body, _ := json.Marshal(actionCursor{CreatedAt: item.CreatedAt, ID: item.ID})
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodeActionCursor(value string) (actionCursor, error) {
	var cursor actionCursor
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &cursor) != nil || cursor.CreatedAt.IsZero() || cursor.ID == uuid.Nil {
		return actionCursor{}, ErrInvalidDeadLetterFilter
	}
	return cursor, nil
}

func decodeOwnerActionCursor(value string) (actionCursor, error) {
	var cursor actionCursor
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &cursor) != nil || cursor.CreatedAt.IsZero() || cursor.ID == uuid.Nil {
		return actionCursor{}, ErrInvalidOwnerFilter
	}
	return cursor, nil
}

func (s *Service) listAttempts(ctx context.Context, actionID uuid.UUID) ([]Attempt, error) {
	rows, err := s.pool.Query(ctx, `SELECT attempt_number,adapter,status,error_code,receipt_sha256,attempted_at FROM identity_email_delivery_attempts WHERE action_id=$1 ORDER BY attempt_number`, actionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Attempt{}
	for rows.Next() {
		var item Attempt
		if err := rows.Scan(&item.AttemptNumber, &item.Adapter, &item.Status, &item.ErrorCode, &item.ReceiptSHA256, &item.AttemptedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) HandleDeliveryJob(ctx context.Context, job jobs.Job) error {
	actionID, err := actionIDFromJob(job)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var userID uuid.UUID
	var kind, status, email, locale string
	var nonce, ciphertext []byte
	var expiresAt time.Time
	var attemptCount int
	err = tx.QueryRow(ctx, `SELECT user_id,kind,status,email_snapshot,locale,token_nonce,token_ciphertext,expires_at,attempt_count FROM identity_email_actions WHERE id=$1 FOR UPDATE`, actionID).Scan(&userID, &kind, &status, &email, &locale, &nonce, &ciphertext, &expiresAt, &attemptCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if status != "queued" {
		return nil
	}
	if !expiresAt.After(s.now()) {
		if _, err := tx.Exec(ctx, `UPDATE identity_email_actions SET status='expired',token_hash=NULL,token_nonce=NULL,token_ciphertext=NULL,updated_at=now(),version=version+1 WHERE id=$1`, actionID); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		_ = s.removeMailbox(userID, actionID)
		return nil
	}
	attemptNumber := attemptCount + 1
	if s.mode == "disabled" {
		return s.deliveryFailure(ctx, tx, job, actionID, attemptNumber, "delivery_disabled")
	}
	token, err := s.decrypt(actionID, kind, nonce, ciphertext)
	if err != nil {
		return s.deliveryFailure(ctx, tx, job, actionID, attemptNumber, "token_decryption_failed")
	}
	content := s.render(email, locale, kind, token, expiresAt)
	if err := s.writeMailbox(userID, actionID, content); err != nil {
		return s.deliveryFailure(ctx, tx, job, actionID, attemptNumber, "local_mailbox_write_failed")
	}
	receipt := sha256.Sum256(content)
	if _, err := tx.Exec(ctx, `INSERT INTO identity_email_delivery_attempts(action_id,attempt_number,adapter,status,receipt_sha256) VALUES($1,$2,'local_file','delivered',$3)`, actionID, attemptNumber, hex.EncodeToString(receipt[:])); err != nil {
		_ = s.removeMailbox(userID, actionID)
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE identity_email_actions SET status='delivered',attempt_count=$2,delivered_at=now(),updated_at=now(),version=version+1 WHERE id=$1`, actionID, attemptNumber); err != nil {
		_ = s.removeMailbox(userID, actionID)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		_ = s.removeMailbox(userID, actionID)
		return err
	}
	return nil
}

func (s *Service) deliveryFailure(ctx context.Context, tx pgx.Tx, job jobs.Job, actionID uuid.UUID, attemptNumber int, code string) error {
	if attemptNumber > 15 {
		return errors.New("identity email attempt limit reached")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO identity_email_delivery_attempts(action_id,attempt_number,adapter,status,error_code) VALUES($1,$2,$3,'failed',$4)`, actionID, attemptNumber, s.mode, code); err != nil {
		return err
	}
	terminal := job.Attempts >= job.MaxAttempts || attemptNumber >= 15
	if terminal {
		if _, err := tx.Exec(ctx, `UPDATE identity_email_actions SET status='dead_letter',attempt_count=$2,dead_lettered_at=now(),updated_at=now(),version=version+1 WHERE id=$1`, actionID, attemptNumber); err != nil {
			return err
		}
	} else if _, err := tx.Exec(ctx, `UPDATE identity_email_actions SET attempt_count=$2,updated_at=now(),version=version+1 WHERE id=$1`, actionID, attemptNumber); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if terminal {
		return nil
	}
	return retryError{code: code, delay: time.Duration(job.Attempts*job.Attempts) * time.Second}
}

func (s *Service) HandleExpiryJob(ctx context.Context, job jobs.Job) error {
	actionID, err := actionIDFromJob(job)
	if err != nil {
		return err
	}
	var userID uuid.UUID
	err = s.pool.QueryRow(ctx, `UPDATE identity_email_actions SET status='expired',token_hash=NULL,token_nonce=NULL,token_ciphertext=NULL,updated_at=now(),version=version+1 WHERE id=$1 AND status IN ('queued','delivered') AND expires_at<=now() RETURNING user_id`, actionID).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_ = s.removeMailbox(userID, actionID)
	return nil
}

func (s *Service) Retry(ctx context.Context, _ uuid.UUID, actionID uuid.UUID, input Transition, _ string) (Action, error) {
	if !validTransition(input) {
		return Action{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Action{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var userID uuid.UUID
	var expiresAt time.Time
	var attemptCount int
	err = tx.QueryRow(ctx, `SELECT user_id,expires_at,attempt_count FROM identity_email_actions WHERE id=$1 AND status='dead_letter' AND version=$2 AND token_hash IS NOT NULL FOR UPDATE`, actionID, input.ExpectedVersion).Scan(&userID, &expiresAt, &attemptCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return Action{}, ErrConflict
	}
	if err != nil {
		return Action{}, err
	}
	if !expiresAt.After(s.now()) || attemptCount >= 15 {
		return Action{}, ErrExpired
	}
	if _, err := tx.Exec(ctx, `UPDATE identity_email_actions SET status='queued',dead_lettered_at=NULL,updated_at=now(),version=version+1 WHERE id=$1`, actionID); err != nil {
		return Action{}, err
	}
	payload, _ := json.Marshal(map[string]any{"actionId": actionID})
	remaining := 15 - attemptCount
	if remaining > 5 {
		remaining = 5
	}
	if _, err := tx.Exec(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,$2,$3)`, DeliveryJobKind, payload, remaining); err != nil {
		return Action{}, err
	}
	if err := notifications.CreateTx(ctx, tx, notifications.CreateInput{UserID: userID, Kind: "security.email_delivery_retried", Title: "Identity email delivery retried", Body: "Operations retried a failed identity email delivery.", TargetPath: "/settings", ResourceType: "identity_email_action", ResourceID: &actionID, SourceKey: "identity-email-retry:" + actionID.String() + ":" + fmt.Sprint(attemptCount)}); err != nil {
		return Action{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Action{}, err
	}
	return s.actionByID(ctx, actionID)
}

func (s *Service) Cancel(ctx context.Context, _ uuid.UUID, actionID uuid.UUID, input Transition, _ string) (Action, error) {
	if !validTransition(input) {
		return Action{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Action{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var userID uuid.UUID
	err = tx.QueryRow(ctx, `UPDATE identity_email_actions SET status='cancelled',token_hash=NULL,token_nonce=NULL,token_ciphertext=NULL,cancelled_at=now(),dead_lettered_at=NULL,updated_at=now(),version=version+1 WHERE id=$1 AND status IN ('queued','delivered','dead_letter') AND version=$2 RETURNING user_id`, actionID, input.ExpectedVersion).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Action{}, ErrConflict
	}
	if err != nil {
		return Action{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE jobs SET status='cancelled',updated_at=now() WHERE kind IN ($1,$2) AND status IN ('queued','running') AND payload->>'actionId'=$3`, DeliveryJobKind, ExpiryJobKind, actionID.String()); err != nil {
		return Action{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Action{}, err
	}
	_ = s.removeMailbox(userID, actionID)
	return s.actionByID(ctx, actionID)
}

func (s *Service) actionByID(ctx context.Context, actionID uuid.UUID) (Action, error) {
	item, err := scanAction(s.pool.QueryRow(ctx, actionSelect+`WHERE a.id=$1`, actionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Action{}, ErrNotFound
	}
	if err != nil {
		return Action{}, err
	}
	item.Attempts, err = s.listAttempts(ctx, item.ID)
	if err != nil {
		return Action{}, err
	}
	return item, nil
}

func (s *Service) MinimizeUser(ctx context.Context, tx pgx.Tx, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `UPDATE identity_email_actions SET status=CASE WHEN status IN ('queued','delivered','dead_letter') THEN 'cancelled' ELSE status END,email_snapshot='deleted+'||substr(id::text,1,8)||'@invalid.local',token_hash=NULL,token_nonce=NULL,token_ciphertext=NULL,cancelled_at=CASE WHEN status IN ('queued','delivered','dead_letter') THEN now() ELSE cancelled_at END,dead_lettered_at=NULL,updated_at=now(),version=version+1 WHERE user_id=$1 RETURNING id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE jobs SET status='cancelled',updated_at=now() WHERE kind IN ($1,$2) AND status IN ('queued','running') AND payload->>'actionId'=ANY($3::text[])`, DeliveryJobKind, ExpiryJobKind, uuidStrings(ids)); err != nil {
		return nil, err
	}
	return ids, nil
}

func (s *Service) RemoveUserMailbox(userID uuid.UUID) error {
	return os.RemoveAll(filepath.Join(s.mailRoot, userID.String()))
}

func (s *Service) encrypt(actionID uuid.UUID, kind, token string) ([]byte, []byte, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return nonce, gcm.Seal(nil, nonce, []byte(token), []byte(actionID.String()+":"+kind)), nil
}

func (s *Service) decrypt(actionID uuid.UUID, kind string, nonce, ciphertext []byte) (string, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, []byte(actionID.String()+":"+kind))
	if err != nil {
		return "", errors.New("identity email token decryption failed")
	}
	return string(plaintext), nil
}

func (s *Service) render(email, locale, kind, token string, expiresAt time.Time) []byte {
	path := "/verify-email"
	subject := "Verify your HCAI CHAT email"
	intro := "Confirm this email address to protect your HCAI CHAT account."
	action := "Verify email"
	if kind == PasswordReset {
		path = "/reset-password"
		subject = "Reset your HCAI CHAT password"
		intro = "A password reset was requested for your HCAI CHAT account."
		action = "Reset password"
	}
	if locale == "zh-CN" {
		if kind == VerifyEmail {
			subject, intro, action = "验证您的 HCAI CHAT 邮箱", "请确认此邮箱地址，以保护您的 HCAI CHAT 账户。", "验证邮箱"
		} else {
			subject, intro, action = "重置您的 HCAI CHAT 密码", "有人为您的 HCAI CHAT 账户请求了密码重置。", "重置密码"
		}
	}
	link := s.webOrigin + path + "?token=" + url.QueryEscape(token)
	body := fmt.Sprintf("From: HCAI CHAT <no-reply@hcai.local>\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s\r\n\r\n%s: %s\r\n\r\nExpires: %s\r\n\r\nIf you did not request this action, you can ignore this message.\r\n", email, subject, intro, action, link, expiresAt.UTC().Format(time.RFC3339))
	return []byte(body)
}

func (s *Service) writeMailbox(userID, actionID uuid.UUID, content []byte) error {
	directory := filepath.Join(s.mailRoot, userID.String())
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".email-*.tmp")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, filepath.Join(directory, actionID.String()+".eml"))
}

func (s *Service) removeMailbox(userID, actionID uuid.UUID) error {
	err := os.Remove(filepath.Join(s.mailRoot, userID.String(), actionID.String()+".eml"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func actionIDFromJob(job jobs.Job) (uuid.UUID, error) {
	var payload struct {
		ActionID uuid.UUID `json:"actionId"`
	}
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.ActionID == uuid.Nil {
		return uuid.Nil, ErrInvalid
	}
	return payload.ActionID, nil
}

func newToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "emailact_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

func hashToken(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func maskEmail(value string) string {
	parts := strings.SplitN(value, "@", 2)
	if len(parts) != 2 || len(parts[0]) == 0 {
		return "***"
	}
	prefix := parts[0][:1]
	return prefix + "***@" + parts[1]
}

func validTransition(input Transition) bool {
	return input.ExpectedVersion > 0
}

func audit(ctx context.Context, tx pgx.Tx, actorID *uuid.UUID, action, resourceType string, resourceID uuid.UUID, reason, requestID string, metadata map[string]any) error {
	if strings.TrimSpace(requestID) == "" {
		requestID = "identity-email-local"
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	body, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id,metadata) VALUES($1,$2,$3,$4,$5,$6,$7)`, actorID, action, resourceType, resourceID, nullable(reason), requestID, body)
	return err
}

func nullable(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.TrimSpace(value)
}

func uuidStrings(values []uuid.UUID) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.String()
	}
	return result
}

type retryError struct {
	code  string
	delay time.Duration
}

func (e retryError) Error() string             { return e.code }
func (e retryError) RetryDelay() time.Duration { return e.delay }
