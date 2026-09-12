package tasktypes

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"strings"
)

var ErrInvalid = errors.New("invalid task type")

type Type struct {
	Code      string `json:"code"`
	NameZh    string `json:"nameZh"`
	NameEn    string `json:"nameEn"`
	Icon      string `json:"icon"`
	SortOrder int    `json:"sortOrder"`
}
type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }
func (s *Service) List(ctx context.Context) ([]Type, error) {
	rows, err := s.pool.Query(ctx, `SELECT code,name_zh,name_en,icon,sort_order FROM task_types ORDER BY sort_order,code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Type{}
	for rows.Next() {
		var t Type
		if err := rows.Scan(&t.Code, &t.NameZh, &t.NameEn, &t.Icon, &t.SortOrder); err != nil {
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
	_, err := s.pool.Exec(ctx, `INSERT INTO task_types(code,name_zh,name_en,icon,sort_order) VALUES($1,$2,$3,$4,$5)`, t.Code, t.NameZh, t.NameEn, t.Icon, t.SortOrder)
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
	_, err := s.pool.Exec(ctx, `UPDATE task_types SET name_zh=$2,name_en=$3,icon=$4,sort_order=$5 WHERE code=$1`, code, t.NameZh, t.NameEn, t.Icon, t.SortOrder)
	if err != nil {
		return Type{}, err
	}
	t.Code = code
	return t, nil
}
func (s *Service) Delete(ctx context.Context, code, replacement string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var n int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM demands WHERE deliverable_type=$1`, code).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		if replacement == "" || replacement == code {
			return ErrInvalid
		}
		if _, err = tx.Exec(ctx, `UPDATE demands SET deliverable_type=$2 WHERE deliverable_type=$1`, code, replacement); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `DELETE FROM task_types WHERE code=$1`, code); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
