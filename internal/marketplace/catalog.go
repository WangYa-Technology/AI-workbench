package marketplace

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type ProductPage struct {
	Items          []Product      `json:"items"`
	Total          int            `json:"total"`
	CategoryCounts map[string]int `json:"categoryCounts"`
	NextCursor     *string        `json:"nextCursor,omitempty"`
}

var categoryCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,47}$`)

func normalizeProductFilter(f ListFilter) (ListFilter, error) {
	if !utf8.ValidString(f.Query) {
		return f, ErrInvalidProductFilter
	}
	f.Query = strings.ToLower(strings.TrimSpace(f.Query))
	f.ProductType = strings.ToLower(strings.TrimSpace(f.ProductType))
	f.Category = strings.ToLower(strings.TrimSpace(f.Category))
	f.LicenseCode = strings.TrimSpace(f.LicenseCode)
	f.Sort = strings.TrimSpace(f.Sort)
	if f.Sort == "" {
		f.Sort = "newest"
	}
	if f.Limit == 0 {
		f.Limit = 50
	}
	if !utf8.ValidString(f.Query) || strings.ContainsRune(f.Query, 0) || utf8.RuneCountInString(f.Query) > 120 ||
		!utf8.ValidString(f.LicenseCode) || strings.ContainsRune(f.LicenseCode, 0) || utf8.RuneCountInString(f.LicenseCode) > 100 ||
		f.Limit < 1 || f.Limit > 100 ||
		(f.Category != "" && !categoryCodePattern.MatchString(f.Category)) {
		return f, ErrInvalidProductFilter
	}
	switch f.ProductType {
	case "", "prompt", "workflow", "asset", "work":
	default:
		return f, ErrInvalidProductFilter
	}
	switch f.Sort {
	case "newest", "price_asc", "price_desc":
	default:
		return f, ErrInvalidProductFilter
	}
	return f, nil
}

type productCursor struct {
	Version int       `json:"v"`
	Time    time.Time `json:"time"`
	ID      uuid.UUID `json:"id"`
	Price   int       `json:"price"`
	Scope   string    `json:"scope"`
}

func productScope(viewer uuid.UUID, f ListFilter) string {
	raw, _ := json.Marshal([]string{viewer.String(), f.Query, f.ProductType, f.Category, f.LicenseCode, f.Sort})
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

func decodeProductCursor(raw, scope string) (*productCursor, error) {
	if raw == "" {
		return nil, nil
	}
	if len(raw) > 1024 {
		return nil, ErrInvalidProductFilter
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	var c productCursor
	if err != nil || json.Unmarshal(data, &c) != nil || c.Version != 1 || c.Time.IsZero() || c.ID == uuid.Nil || c.Price < 0 || c.Scope != scope {
		return nil, ErrInvalidProductFilter
	}
	return &c, nil
}

const productFilterSQL = ` WHERE p.status='active'
	AND ($2='' OR lower(p.title||' '||p.description||' '||u.display_name) LIKE '%'||$2||'%')
	AND ($3='' OR p.product_type=$3)
	AND ($4='' OR p.license_code=$4)`

func (s *Service) ListProducts(ctx context.Context, viewerID uuid.UUID, filter ListFilter) (ProductPage, error) {
	f, err := normalizeProductFilter(filter)
	if err != nil {
		return ProductPage{}, err
	}
	scope := productScope(viewerID, f)
	cursor, err := decodeProductCursor(f.Cursor, scope)
	if err != nil {
		return ProductPage{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ProductPage{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	query := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(f.Query)
	args := []any{viewerID, query, f.ProductType, f.LicenseCode}
	page := ProductPage{Items: []Product{}, CategoryCounts: map[string]int{}}
	// Facets ignore only the selected category; totals and rows share this snapshot.
	counts, err := tx.Query(ctx, `SELECT p.category,count(*)`+productFrom+productFilterSQL+` GROUP BY p.category`, args...)
	if err != nil {
		return ProductPage{}, fmt.Errorf("count products: %w", err)
	}
	for counts.Next() {
		var category string
		var count int
		if err := counts.Scan(&category, &count); err != nil {
			counts.Close()
			return ProductPage{}, err
		}
		page.CategoryCounts[category] = count
		if f.Category == "" || f.Category == category {
			page.Total += count
		}
	}
	counts.Close()
	if err := counts.Err(); err != nil {
		return ProductPage{}, err
	}

	order, after := "p.created_at DESC,p.id DESC", ""
	args = append(args, f.Category, f.Limit+1)
	if f.Sort == "price_asc" {
		order = "p.price_cents ASC,p.created_at DESC,p.id DESC"
	} else if f.Sort == "price_desc" {
		order = "p.price_cents DESC,p.created_at DESC,p.id DESC"
	}
	if cursor != nil {
		args = append(args, cursor.Time, cursor.ID)
		after = " AND (p.created_at,p.id)<($7,$8)"
		if f.Sort != "newest" {
			args = append(args, cursor.Price)
			comparison := ">"
			if f.Sort == "price_desc" {
				comparison = "<"
			}
			after = " AND (p.price_cents" + comparison + "$9 OR (p.price_cents=$9 AND (p.created_at,p.id)<($7,$8)))"
		}
	}
	rows, err := tx.Query(ctx, productSelect+productFilterSQL+` AND ($5='' OR p.category=$5)`+after+` ORDER BY `+order+` LIMIT $6`, args...)
	if err != nil {
		return ProductPage{}, fmt.Errorf("list products: %w", err)
	}
	for rows.Next() {
		item, err := scanProduct(rows)
		if err != nil {
			rows.Close()
			return ProductPage{}, err
		}
		page.Items = append(page.Items, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return ProductPage{}, err
	}
	if len(page.Items) > f.Limit {
		page.Items = page.Items[:f.Limit]
		last := page.Items[len(page.Items)-1]
		raw, _ := json.Marshal(productCursor{Version: 1, Time: last.CreatedAt, ID: last.ID, Price: last.PriceCents, Scope: scope})
		next := base64.RawURLEncoding.EncodeToString(raw)
		page.NextCursor = &next
	}
	if err := tx.Commit(ctx); err != nil {
		return ProductPage{}, err
	}
	return page, nil
}
