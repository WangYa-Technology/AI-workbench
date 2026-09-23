package tasks

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Page struct {
	Items      []Summary      `json:"items"`
	Total      int            `json:"total"`
	TypeCounts map[string]int `json:"typeCounts"`
	NextCursor *string        `json:"nextCursor,omitempty"`
}

type listCursor struct {
	ID       uuid.UUID `json:"id"`
	Created  time.Time `json:"created"`
	Deadline time.Time `json:"deadline"`
	Budget   int       `json:"budget"`
	Scope    string    `json:"scope"`
}

func (s *Service) ListPage(ctx context.Context, actorID uuid.UUID, filter ListFilter) (Page, error) {
	filter.Query = strings.TrimSpace(filter.Query)
	filter.DeliverableType = strings.ToLower(strings.TrimSpace(filter.DeliverableType))
	filter.Status = strings.ToLower(strings.TrimSpace(filter.Status))
	if filter.Sort == "" {
		filter.Sort = "newest"
	}
	if filter.Limit == 0 {
		filter.Limit = 40
	}
	if filter.Limit < 1 || filter.Limit > 100 || len(filter.Query) > 500 || (filter.Mine && actorID == uuid.Nil) {
		return Page{}, ErrInvalid
	}
	allowed := map[string]bool{"": true, "open": true, "assigned": true, "submitted": true, "revision": true, "accepted": true, "disputed": true, "cancelled": true}
	if !allowed[filter.Status] {
		return Page{}, ErrInvalid
	}
	order, comparison := "d.created_at DESC,d.id DESC", "(d.created_at,d.id)<($7,$6::uuid)"
	switch filter.Sort {
	case "newest":
	case "deadline":
		order = "d.deadline ASC,d.id ASC"
		comparison = "(d.deadline,d.id)>($8,$6::uuid)"
	case "budget_desc":
		order = "d.budget_cents DESC,d.created_at DESC,d.id DESC"
		comparison = "(d.budget_cents,d.created_at,d.id)<($9,$7,$6::uuid)"
	default:
		return Page{}, ErrInvalid
	}
	scope := commandHash([]any{actorID, filter.Query, filter.DeliverableType, filter.Status, filter.Sort, filter.Mine})
	var cursor listCursor
	var cursorID *uuid.UUID
	if filter.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(filter.Cursor)
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.ID == uuid.Nil || cursor.Scope != scope || cursor.Created.IsZero() || cursor.Deadline.IsZero() {
			return Page{}, ErrInvalid
		}
		cursorID = &cursor.ID
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Page{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	const base = ` FROM demands d JOIN users c ON c.id=d.client_id
 WHERE ($1='' OR strpos(lower(d.title),lower($1))>0 OR strpos(lower(d.summary),lower($1))>0 OR strpos(lower(d.brief),lower($1))>0)
 AND ($2='' OR d.status=$2)
 AND (c.status='active' OR d.client_id=$3 OR d.assignee_id=$3 OR EXISTS(SELECT 1 FROM proposals p WHERE p.demand_id=d.id AND p.creator_id=$3))
 AND (NOT $4 OR d.client_id=$3 OR d.assignee_id=$3 OR EXISTS(SELECT 1 FROM proposals p WHERE p.demand_id=d.id AND p.creator_id=$3))`
	counts, err := tx.Query(ctx, `SELECT d.deliverable_type,count(*)`+base+` GROUP BY d.deliverable_type`, filter.Query, filter.Status, actorID, filter.Mine)
	if err != nil {
		return Page{}, err
	}
	page := Page{Items: []Summary{}, TypeCounts: map[string]int{}}
	for counts.Next() {
		var kind string
		var count int
		if err = counts.Scan(&kind, &count); err != nil {
			counts.Close()
			return Page{}, err
		}
		page.TypeCounts[kind] = count
		if filter.DeliverableType == "" || kind == filter.DeliverableType {
			page.Total += count
		}
	}
	counts.Close()
	if err = counts.Err(); err != nil {
		return Page{}, err
	}
	// All cursor parameters are typed even for sorts which do not use every field.
	query := `SELECT d.id,d.title,d.summary,d.deliverable_type,d.budget_cents,d.currency,d.deadline,d.status,
 d.client_timezone,d.allow_direct_accept,(SELECT count(*) FROM proposals p WHERE p.demand_id=d.id),
 c.id,c.handle,c.display_name,a.id,a.handle,a.display_name,d.created_at,d.updated_at`
	listBase := strings.Replace(base, " WHERE ", " LEFT JOIN users a ON a.id=d.assignee_id WHERE ", 1)
	rows, err := tx.Query(ctx, query+listBase+` AND ($5='' OR d.deliverable_type=$5)
 AND ($6::uuid IS NULL OR `+comparison+`)
 AND $7::timestamptz IS NOT NULL AND $8::timestamptz IS NOT NULL AND $9::integer IS NOT NULL
 ORDER BY `+order+` LIMIT $10`, filter.Query, filter.Status, actorID, filter.Mine, filter.DeliverableType, cursorID, cursor.Created, cursor.Deadline, cursor.Budget, filter.Limit+1)
	if err != nil {
		return Page{}, fmt.Errorf("list task page: %w", err)
	}
	for rows.Next() {
		item, e := scanSummary(rows)
		if e != nil {
			rows.Close()
			return Page{}, e
		}
		page.Items = append(page.Items, item)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return Page{}, err
	}
	if len(page.Items) > filter.Limit {
		page.Items = page.Items[:filter.Limit]
		last := page.Items[len(page.Items)-1]
		raw, _ := json.Marshal(listCursor{ID: last.ID, Created: last.CreatedAt, Deadline: last.Deadline, Budget: last.BudgetCents, Scope: scope})
		next := base64.RawURLEncoding.EncodeToString(raw)
		page.NextCursor = &next
	}
	if err = tx.Commit(ctx); err != nil {
		return Page{}, err
	}
	return page, nil
}
