package support

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type adminCursor struct {
	Priority  int       `json:"priority"`
	UpdatedAt time.Time `json:"updatedAt"`
	ID        uuid.UUID `json:"id"`
}

func (s *Service) ListAdmin(ctx context.Context, filter ListFilter) (Page, error) {
	filter.Query = strings.ToLower(strings.TrimSpace(filter.Query))
	filter.Status = strings.ToLower(strings.TrimSpace(filter.Status))
	filter.Category = strings.ToLower(strings.TrimSpace(filter.Category))
	if len(filter.Query) > 120 || (filter.Status != "" && !validStatus(filter.Status)) ||
		(filter.Category != "" && !validCategory(filter.Category)) {
		return Page{}, ErrInvalidAdminFilter
	}
	if filter.Limit == 0 {
		filter.Limit = 20
	}
	if filter.Limit < 1 || filter.Limit > 50 {
		return Page{}, ErrInvalidAdminFilter
	}
	var priority *int
	var updatedAt *time.Time
	var cursorID *uuid.UUID
	if filter.Cursor != "" {
		cursor, err := decodeAdminCursor(filter.Cursor)
		if err != nil {
			return Page{}, err
		}
		priority, updatedAt, cursorID = &cursor.Priority, &cursor.UpdatedAt, &cursor.ID
	}
	const prioritySQL = `CASE WHEN c.status IN ('open','waiting_for_requester') THEN 0 WHEN c.status='in_review' THEN 1 ELSE 2 END`
	rows, err := s.pool.Query(ctx, caseSelect+`
		WHERE ($1='' OR strpos(lower(c.subject),$1)>0 OR strpos(lower(c.details),$1)>0 OR strpos(lower(u.handle),$1)>0)
		  AND ($2='' OR c.status=$2) AND ($3='' OR c.category=$3)
		  AND ($4::int IS NULL OR `+prioritySQL+`>$4 OR (`+prioritySQL+`=$4 AND (c.updated_at,c.id)<($5,$6::uuid)))
		ORDER BY `+prioritySQL+`,c.updated_at DESC,c.id DESC LIMIT $7`,
		filter.Query, filter.Status, filter.Category, priority, updatedAt, cursorID, filter.Limit+1)
	if err != nil {
		return Page{}, fmt.Errorf("list admin support cases: %w", err)
	}
	defer rows.Close()
	items := make([]Case, 0)
	for rows.Next() {
		item, err := scanCase(rows)
		if err != nil {
			return Page{}, fmt.Errorf("scan admin support case: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return Page{}, err
	}
	page := Page{Items: items}
	if len(page.Items) > filter.Limit {
		page.Items = page.Items[:filter.Limit]
		cursor := encodeAdminCursor(page.Items[len(page.Items)-1])
		page.NextCursor = &cursor
	}
	return page, nil
}

func supportPriority(status string) int {
	if status == "open" || status == "waiting_for_requester" {
		return 0
	}
	if status == "in_review" {
		return 1
	}
	return 2
}

func encodeAdminCursor(item Case) string {
	body, _ := json.Marshal(adminCursor{Priority: supportPriority(item.Status), UpdatedAt: item.UpdatedAt, ID: item.ID})
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodeAdminCursor(value string) (adminCursor, error) {
	var cursor adminCursor
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &cursor) != nil || cursor.Priority < 0 || cursor.Priority > 2 || cursor.UpdatedAt.IsZero() || cursor.ID == uuid.Nil {
		return adminCursor{}, ErrInvalidAdminFilter
	}
	return cursor, nil
}
