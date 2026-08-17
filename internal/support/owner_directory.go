package support

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type OwnedListInput struct {
	Cursor string
	Limit  int
}

type ownerCursor struct {
	UpdatedAt time.Time `json:"updatedAt"`
	ID        uuid.UUID `json:"id"`
}

func (s *Service) ListOwned(ctx context.Context, requesterID uuid.UUID, input OwnedListInput) (Page, error) {
	if input.Limit == 0 {
		input.Limit = 20
	}
	if requesterID == uuid.Nil || input.Limit < 1 || input.Limit > 50 {
		return Page{}, ErrInvalidOwnerFilter
	}
	var updatedAt *time.Time
	var cursorID *uuid.UUID
	if input.Cursor != "" {
		cursor, err := decodeOwnerCursor(input.Cursor)
		if err != nil {
			return Page{}, err
		}
		updatedAt, cursorID = &cursor.UpdatedAt, &cursor.ID
	}
	rows, err := s.pool.Query(ctx, caseSelect+`
		WHERE c.requester_id=$1
		  AND ($2::timestamptz IS NULL OR (c.updated_at,c.id)<($2,$3::uuid))
		ORDER BY c.updated_at DESC,c.id DESC LIMIT $4`, requesterID, updatedAt, cursorID, input.Limit+1)
	if err != nil {
		return Page{}, fmt.Errorf("list owned support cases: %w", err)
	}
	defer rows.Close()
	items := make([]Case, 0)
	for rows.Next() {
		item, err := scanCase(rows)
		if err != nil {
			return Page{}, fmt.Errorf("scan owned support case: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return Page{}, err
	}
	page := Page{Items: items}
	if len(page.Items) > input.Limit {
		page.Items = page.Items[:input.Limit]
		cursor := encodeOwnerCursor(page.Items[len(page.Items)-1])
		page.NextCursor = &cursor
	}
	return page, nil
}

func encodeOwnerCursor(item Case) string {
	body, _ := json.Marshal(ownerCursor{UpdatedAt: item.UpdatedAt, ID: item.ID})
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodeOwnerCursor(value string) (ownerCursor, error) {
	var cursor ownerCursor
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &cursor) != nil || cursor.UpdatedAt.IsZero() || cursor.ID == uuid.Nil {
		return ownerCursor{}, ErrInvalidOwnerFilter
	}
	return cursor, nil
}
