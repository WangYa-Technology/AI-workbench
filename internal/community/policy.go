package community

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func textLength(value string) int { return utf8.RuneCountInString(value) }

// Keep the selected category valid until the containing transaction commits.
func categoryTx(ctx context.Context, tx pgx.Tx, value *string) error {
	*value = strings.TrimSpace(*value)
	err := tx.QueryRow(ctx, `SELECT code FROM task_types WHERE scope='community' AND ($1='' OR code=$1) ORDER BY sort_order,code LIMIT 1 FOR KEY SHARE`, *value).Scan(value)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalid
	}
	return err
}

func visiblePostTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	var locked uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM posts WHERE id=$1 FOR SHARE`, id).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	var visible bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM community_visible_posts WHERE id=$1)`, id).Scan(&visible); err != nil {
		return err
	}
	if !visible {
		return ErrNotFound
	}
	return nil
}
