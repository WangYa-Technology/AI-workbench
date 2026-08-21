package admin

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
)

type GenerationListInput struct {
	Query  string
	Mode   string
	Status string
	Cursor string
	Limit  int
}

type GenerationPage struct {
	Items      []GenerationItem `json:"items"`
	NextCursor *string          `json:"nextCursor,omitempty"`
}

type generationCursor struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        uuid.UUID `json:"id"`
}

func (s *Service) ListGenerations(ctx context.Context, input GenerationListInput) (GenerationPage, error) {
	input.Query = strings.ToLower(strings.TrimSpace(input.Query))
	input.Mode = strings.ToLower(strings.TrimSpace(input.Mode))
	input.Status = strings.ToLower(strings.TrimSpace(input.Status))
	if len(input.Query) > 120 || (input.Mode != "" && !oneOf(input.Mode, "chat", "image", "video", "music")) ||
		(input.Status != "" && !oneOf(input.Status, "queued", "running", "succeeded", "failed", "cancelled")) {
		return GenerationPage{}, ErrInvalidGenerationFilter
	}
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Limit < 1 || input.Limit > 50 {
		return GenerationPage{}, ErrInvalidGenerationFilter
	}
	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if input.Cursor != "" {
		cursor, err := decodeGenerationCursor(input.Cursor)
		if err != nil {
			return GenerationPage{}, err
		}
		cursorTime, cursorID = &cursor.CreatedAt, &cursor.ID
	}
	rows, err := s.pool.Query(ctx, creationAdminSelect+`
		WHERE ($1='' OR strpos(lower(g.prompt),$1)>0 OR strpos(lower(u.email),$1)>0 OR strpos(lower(u.handle),$1)>0 OR
			strpos(lower(g.model_name),$1)>0 OR strpos(lower(g.provider),$1)>0)
		  AND ($2='' OR g.mode=$2)
		  AND ($3='' OR g.status=$3)
		  AND ($4::timestamptz IS NULL OR (g.created_at,g.id) < ($4,$5::uuid))
		ORDER BY g.created_at DESC,g.id DESC LIMIT $6`, input.Query, input.Mode, input.Status, cursorTime, cursorID, input.Limit+1)
	if err != nil {
		return GenerationPage{}, fmt.Errorf("list admin generations: %w", err)
	}
	defer rows.Close()
	items := make([]GenerationItem, 0)
	for rows.Next() {
		item, err := scanGeneration(rows)
		if err != nil {
			return GenerationPage{}, fmt.Errorf("scan admin generation: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return GenerationPage{}, err
	}
	page := GenerationPage{Items: items}
	if len(page.Items) > input.Limit {
		page.Items = page.Items[:input.Limit]
		cursor := encodeGenerationCursor(page.Items[len(page.Items)-1])
		page.NextCursor = &cursor
	}
	return page, nil
}

func encodeGenerationCursor(item GenerationItem) string {
	body, _ := json.Marshal(generationCursor{CreatedAt: item.CreatedAt, ID: item.ID})
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodeGenerationCursor(value string) (generationCursor, error) {
	var cursor generationCursor
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &cursor) != nil || cursor.ID == uuid.Nil || cursor.CreatedAt.IsZero() {
		return generationCursor{}, ErrInvalidGenerationFilter
	}
	return cursor, nil
}

type FinanceListInput struct {
	Query  string
	State  string
	Cursor string
	Limit  int
}

type FinancePage struct {
	Items      []FinanceAccount `json:"items"`
	NextCursor *string          `json:"nextCursor,omitempty"`
}

type financeCursor struct {
	UpdatedAt time.Time `json:"updatedAt"`
	UserID    uuid.UUID `json:"userId"`
}

const financeAccountSelect = `
	SELECT b.user_id,b.currency,b.balance_cents,b.reserved_cents,b.balance_cents-b.reserved_cents,b.payment_mode,b.version,b.updated_at,
	       u.email,u.handle,u.display_name
	FROM billing_accounts b JOIN users u ON u.id=b.user_id`

func (s *Service) ListFinance(ctx context.Context, input FinanceListInput) (FinancePage, error) {
	input.Query = strings.ToLower(strings.TrimSpace(input.Query))
	input.State = strings.ToLower(strings.TrimSpace(input.State))
	if len(input.Query) > 120 || (input.State != "" && !oneOf(input.State, "available", "reserved", "depleted")) {
		return FinancePage{}, ErrInvalidFinanceFilter
	}
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Limit < 1 || input.Limit > 50 {
		return FinancePage{}, ErrInvalidFinanceFilter
	}
	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if input.Cursor != "" {
		cursor, err := decodeFinanceCursor(input.Cursor)
		if err != nil {
			return FinancePage{}, err
		}
		cursorTime, cursorID = &cursor.UpdatedAt, &cursor.UserID
	}
	rows, err := s.pool.Query(ctx, financeAccountSelect+`
		WHERE ($1='' OR strpos(lower(u.email),$1)>0 OR strpos(lower(u.handle),$1)>0 OR strpos(lower(u.display_name),$1)>0)
		  AND ($2='' OR ($2='available' AND b.balance_cents-b.reserved_cents>0) OR ($2='reserved' AND b.reserved_cents>0) OR ($2='depleted' AND b.balance_cents-b.reserved_cents=0))
		  AND ($3::timestamptz IS NULL OR (b.updated_at,b.user_id) < ($3,$4::uuid))
		ORDER BY b.updated_at DESC,b.user_id DESC LIMIT $5`, input.Query, input.State, cursorTime, cursorID, input.Limit+1)
	if err != nil {
		return FinancePage{}, fmt.Errorf("list finance accounts: %w", err)
	}
	defer rows.Close()
	items := make([]FinanceAccount, 0)
	for rows.Next() {
		item, err := scanFinanceAccount(rows)
		if err != nil {
			return FinancePage{}, fmt.Errorf("scan finance account: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return FinancePage{}, err
	}
	page := FinancePage{Items: items}
	if len(page.Items) > input.Limit {
		page.Items = page.Items[:input.Limit]
		cursor := encodeFinanceCursor(page.Items[len(page.Items)-1])
		page.NextCursor = &cursor
	}
	return page, nil
}

func (s *Service) financeAccount(ctx context.Context, userID uuid.UUID, currency string) (FinanceAccount, error) {
	item, err := scanFinanceAccount(s.pool.QueryRow(ctx, financeAccountSelect+` WHERE b.user_id=$1 AND b.currency=$2`, userID, currency))
	if errors.Is(err, pgx.ErrNoRows) {
		return FinanceAccount{}, ErrNotFound
	}
	if err != nil {
		return FinanceAccount{}, fmt.Errorf("get finance account: %w", err)
	}
	return item, nil
}

func scanFinanceAccount(row scanner) (FinanceAccount, error) {
	var item FinanceAccount
	err := row.Scan(&item.UserID, &item.Currency, &item.BalanceCents, &item.ReservedCents, &item.AvailableCents,
		&item.PaymentMode, &item.Version, &item.UpdatedAt, &item.Email, &item.Handle, &item.DisplayName)
	return item, err
}

func encodeFinanceCursor(item FinanceAccount) string {
	body, _ := json.Marshal(financeCursor{UpdatedAt: item.UpdatedAt, UserID: item.UserID})
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodeFinanceCursor(value string) (financeCursor, error) {
	var cursor financeCursor
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &cursor) != nil || cursor.UserID == uuid.Nil || cursor.UpdatedAt.IsZero() {
		return financeCursor{}, ErrInvalidFinanceFilter
	}
	return cursor, nil
}
