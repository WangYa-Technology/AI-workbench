package tasktypes

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"regexp"
	"strings"
	"unicode/utf8"
)

var ErrInvalid = errors.New("invalid task type")
var codePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,47}$`)

func validType(t Type) bool {
	return codePattern.MatchString(t.Code) && t.NameZh != "" && t.NameEn != "" && utf8.RuneCountInString(t.NameZh) <= 80 && utf8.RuneCountInString(t.NameEn) <= 80 && t.SortOrder >= 0 && t.SortOrder <= 100000 && strings.Contains("|image|video|audio|prompt|workflow|mixed|", "|"+t.Icon+"|") && (t.Scope == "task" || t.Scope == "community" || t.Scope == "marketplace")
}

type Type struct {
	Scope     string `json:"scope"`
	Code      string `json:"code"`
	NameZh    string `json:"nameZh"`
	NameEn    string `json:"nameEn"`
	Icon      string `json:"icon"`
	SortOrder int    `json:"sortOrder"`
}
type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

type Content struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Category string `json:"category"`
}

func (s *Service) ListContent(ctx context.Context, scope, query, cursor string) ([]Content, string, error) {
	table := "posts"
	if scope == "marketplace" {
		table = "products"
	} else if scope != "community" {
		return nil, "", ErrInvalid
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text,COALESCE(title,''),category FROM `+table+` WHERE (NULLIF($1,'')::uuid IS NULL OR id>NULLIF($1,'')::uuid) AND ($2='' OR title ILIKE '%'||$2||'%') ORDER BY id LIMIT 51`, cursor, strings.TrimSpace(query))
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := []Content{}
	for rows.Next() {
		var item Content
		if err := rows.Scan(&item.ID, &item.Title, &item.Category); err != nil {
			return nil, "", err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > 50 {
		items = items[:50]
		next = items[49].ID
	}
	return items, next, nil
}
func (s *Service) Assign(ctx context.Context, scope, id, category string) error {
	table := "posts"
	if scope == "marketplace" {
		table = "products"
	} else if scope != "community" {
		return ErrInvalid
	}
	result, err := s.pool.Exec(ctx, `UPDATE `+table+` SET category=$2 WHERE id::text=$1 AND EXISTS(SELECT 1 FROM task_types WHERE code=$2 AND scope=$3)`, id, category, scope)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrInvalid
	}
	return nil
}
func (s *Service) List(ctx context.Context, scopes ...string) ([]Type, error) {
	query := `SELECT code,name_zh,name_en,icon,sort_order,scope FROM task_types`
	args := []any{}
	scope := "task"
	if len(scopes) > 0 && scopes[0] != "" {
		scope = scopes[0]
	}
	query += ` WHERE scope=$1`
	args = append(args, scope)
	query += ` ORDER BY sort_order,code`
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Type{}
	for rows.Next() {
		var t Type
		if err := rows.Scan(&t.Code, &t.NameZh, &t.NameEn, &t.Icon, &t.SortOrder, &t.Scope); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *Service) Validate(ctx context.Context, code string) bool {
	var n int
	return s.pool.QueryRow(ctx, `SELECT 1 FROM task_types WHERE code=$1`, code).Scan(&n) == nil
}
func (s *Service) Create(ctx context.Context, t Type) (Type, error) {
	t.Code = strings.ToLower(strings.TrimSpace(t.Code))
	t.NameZh = strings.TrimSpace(t.NameZh)
	t.NameEn = strings.TrimSpace(t.NameEn)
	if t.Code == "" || t.NameZh == "" || t.NameEn == "" || t.Icon == "" {
		return Type{}, ErrInvalid
	}
	if t.Scope == "" {
		t.Scope = "task"
	}
	if !validType(t) {
		return Type{}, ErrInvalid
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO task_types(code,name_zh,name_en,icon,sort_order,scope) VALUES($1,$2,$3,$4,$5,$6)`, t.Code, t.NameZh, t.NameEn, t.Icon, t.SortOrder, t.Scope)
	if err != nil {
		return Type{}, fmt.Errorf("create task type: %w", err)
	}
	return t, nil
}
func (s *Service) Update(ctx context.Context, code string, t Type) (Type, error) {
	t.NameZh = strings.TrimSpace(t.NameZh)
	t.NameEn = strings.TrimSpace(t.NameEn)
	if t.NameZh == "" || t.NameEn == "" || t.Icon == "" {
		return Type{}, ErrInvalid
	}
	if t.Scope == "" {
		t.Scope = "task"
	}
	t.Code = code
	if !validType(t) {
		return Type{}, ErrInvalid
	}
	result, err := s.pool.Exec(ctx, `UPDATE task_types SET name_zh=$2,name_en=$3,icon=$4,sort_order=$5 WHERE code=$1 AND scope=$6`, code, t.NameZh, t.NameEn, t.Icon, t.SortOrder, t.Scope)
	if err != nil {
		return Type{}, err
	}
	t.Code = code
	if result.RowsAffected() == 0 {
		return Type{}, ErrInvalid
	}
	return t, nil
}
func (s *Service) Delete(ctx context.Context, code, replacement string, scopes ...string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Serialize directory mutations, avoiding opposite replacement lock ordering.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(6842931)`); err != nil {
		return err
	}
	var n int
	var scope string
	if err = tx.QueryRow(ctx, `SELECT scope FROM task_types WHERE code=$1 FOR UPDATE`, code).Scan(&scope); err != nil {
		return ErrInvalid
	}
	expected := "task"
	if len(scopes) > 0 {
		expected = scopes[0]
	}
	if scope != expected {
		return ErrInvalid
	}
	if replacement != "" {
		var targetScope string
		if err = tx.QueryRow(ctx, `SELECT scope FROM task_types WHERE code=$1 FOR KEY SHARE`, replacement).Scan(&targetScope); err != nil || targetScope != scope || replacement == code {
			return ErrInvalid
		}
	}
	if err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM demands WHERE deliverable_type=$1)+(SELECT count(*) FROM posts WHERE category=$1)+(SELECT count(*) FROM products WHERE category=$1)`, code).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		if replacement == "" || replacement == code {
			return ErrInvalid
		}
		if _, err = tx.Exec(ctx, `UPDATE demands SET deliverable_type=$2 WHERE deliverable_type=$1`, code, replacement); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE posts SET category=$2 WHERE category=$1`, code, replacement); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE products SET category=$2 WHERE category=$1`, code, replacement); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `DELETE FROM task_types WHERE code=$1`, code); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
