package productdelivery

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrInventoryFilter = errors.New("invalid delivery evidence filter")

// EvidenceGap is a database evidence projection, not a claim that a file exists
// or that today's bytes match the original purchase. It deliberately contains
// no buyer identity, storage locators, contract body or payment credentials.
type EvidenceGap struct {
	OrderID           uuid.UUID `json:"orderId"`
	Title             string    `json:"title"`
	OrderStatus       string    `json:"orderStatus"`
	Environment       string    `json:"environment"`
	Gap               string    `json:"gap"`
	HasActiveRights   bool      `json:"hasActiveRights"`
	HasPendingPayment bool      `json:"hasPendingPayment"`
	HasUnsettledFunds bool      `json:"hasUnsettledFunds"`
	HasSnapshot       bool      `json:"hasSnapshot"`
	CreatedAt         time.Time `json:"createdAt"`
}

type EvidenceGapFilter struct {
	Gap, Environment, Scope, Cursor string
	Limit                           int
}

type EvidenceGapPage struct {
	Items      []EvidenceGap `json:"items"`
	Scanned    int           `json:"scanned"`
	NextCursor *string       `json:"nextCursor,omitempty"`
}

type evidenceCursor struct {
	Version     int       `json:"v"`
	Actor       uuid.UUID `json:"actor"`
	Gap         string    `json:"gap"`
	Environment string    `json:"environment"`
	Scope       string    `json:"scope"`
	Time        time.Time `json:"time"`
	Order       uuid.UUID `json:"order"`
}

func normalizeEvidenceFilter(f EvidenceGapFilter, actor uuid.UUID) (EvidenceGapFilter, evidenceCursor, error) {
	c := evidenceCursor{Version: 1, Actor: actor, Gap: f.Gap, Environment: f.Environment, Scope: f.Scope}
	if f.Limit == 0 {
		f.Limit = 20
	}
	if f.Limit < 1 || f.Limit > 50 || actor == uuid.Nil || len(f.Cursor) > 1024 {
		return f, c, ErrInventoryFilter
	}
	switch f.Gap {
	case "", "contract_missing", "legacy_unfrozen", "required_snapshot_missing", "legacy_snapshot_unbound":
	default:
		return f, c, ErrInventoryFilter
	}
	switch f.Environment {
	case "", "live", "test", "unknown":
	default:
		return f, c, ErrInventoryFilter
	}
	if f.Scope != "" && f.Scope != "active" && f.Scope != "unsettled" {
		return f, c, ErrInventoryFilter
	}
	if f.Cursor != "" {
		data, err := base64.RawURLEncoding.DecodeString(f.Cursor)
		var got evidenceCursor
		if err != nil || json.Unmarshal(data, &got) != nil || got.Version != c.Version || got.Actor != c.Actor || got.Gap != c.Gap || got.Environment != c.Environment || got.Scope != c.Scope || got.Order == uuid.Nil || got.Time.IsZero() || got.Time.Year() < 1 || got.Time.Year() > 9999 {
			return f, c, ErrInventoryFilter
		}
		c = got
	}
	return f, c, nil
}

func (s *RepairService) ListEvidenceGaps(ctx context.Context, actor uuid.UUID, filter EvidenceGapFilter) (EvidenceGapPage, error) {
	page := EvidenceGapPage{Items: []EvidenceGap{}}
	f, cursor, err := normalizeEvidenceFilter(filter, actor)
	if err != nil {
		return page, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return page, err
	}
	defer tx.Rollback(ctx)
	if err = repairPermission(ctx, tx, actor); err != nil {
		return page, err
	}
	// Inspect original orders, never today's product offer. Include required but
	// missing snapshots as a distinct integrity gap, not a historical exception.
	// Bound evidence joins even when almost all orders have healthy snapshots.
	// The global creation index supports each window without a full-table sort.
	// Separate the initial and seek predicates so a generic prepared plan does
	// not scan every preceding order to evaluate an optional-cursor OR clause.
	cutoff := "WHERE $4::timestamptz IS NULL AND $5::uuid IS NOT NULL"
	if !cursor.Time.IsZero() {
		cutoff = "WHERE (created_at,id)<($4::timestamptz,$5::uuid)"
	}
	rows, err := tx.Query(ctx, `WITH candidates AS MATERIALIZED (
 SELECT id,product_title_snapshot,status,created_at,delivery_snapshot_required FROM orders `+cutoff+`
 ORDER BY created_at DESC,id DESC LIMIT 501
 ) SELECT o.id,o.product_title_snapshot,o.status,
 CASE WHEN pi.id IS NULL THEN 'unknown' WHEN pi.live_mode THEN 'live' ELSE 'test' END,
 g.kind,r.active,COALESCE(pi.status IN ('checkout_pending','checkout_open'),false),funds.required,d.order_id IS NOT NULL,o.created_at,
 (NOT o.delivery_snapshot_required OR c.order_id IS NULL OR d.order_id IS NULL)
 AND ($1='' OR g.kind=$1)
 AND ($2='' OR (CASE WHEN pi.id IS NULL THEN 'unknown' WHEN pi.live_mode THEN 'live' ELSE 'test' END)=$2)
 AND ($3='' OR ($3='active' AND (r.active OR COALESCE(pi.status IN ('checkout_pending','checkout_open'),false)))
  OR ($3='unsettled' AND funds.required)) AS matches
 FROM candidates o
 LEFT JOIN product_order_contracts c ON c.order_id=o.id
 LEFT JOIN product_delivery_snapshots d ON d.order_id=o.id
 LEFT JOIN payment_intents pi ON pi.order_id=o.id AND pi.purpose='product'
 CROSS JOIN LATERAL (SELECT CASE WHEN c.order_id IS NULL THEN 'contract_missing'
   WHEN o.delivery_snapshot_required AND d.order_id IS NULL THEN 'required_snapshot_missing'
   WHEN NOT o.delivery_snapshot_required AND d.order_id IS NOT NULL THEN 'legacy_snapshot_unbound'
   ELSE 'legacy_unfrozen' END AS kind) g
 CROSS JOIN LATERAL (SELECT EXISTS(SELECT 1 FROM entitlements e JOIN users u ON u.id=e.user_id
   WHERE e.order_id=o.id AND e.status='active' AND u.status<>'deleted') AS active) r
 CROSS JOIN LATERAL (SELECT EXISTS(SELECT 1 FROM product_order_funds_retention f WHERE f.order_id=o.id) AS required) funds
 ORDER BY o.created_at DESC,o.id DESC`, f.Gap, f.Environment, f.Scope, nullableEvidenceTime(cursor.Time), cursor.Order)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	more := false
	for rows.Next() {
		var item EvidenceGap
		var matches bool
		if err = rows.Scan(&item.OrderID, &item.Title, &item.OrderStatus, &item.Environment, &item.Gap, &item.HasActiveRights, &item.HasPendingPayment, &item.HasUnsettledFunds, &item.HasSnapshot, &item.CreatedAt, &matches); err != nil {
			return page, err
		}
		if page.Scanned == 500 || (matches && len(page.Items) == f.Limit) {
			more = true
			break
		}
		page.Scanned++
		cursor.Time, cursor.Order = item.CreatedAt, item.OrderID
		if matches {
			page.Items = append(page.Items, item)
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return page, err
	}
	if more {
		data, err := json.Marshal(cursor)
		if err != nil {
			return page, err
		}
		encoded := base64.RawURLEncoding.EncodeToString(data)
		page.NextCursor = &encoded
	}
	return page, tx.Commit(ctx)
}

func nullableEvidenceTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
