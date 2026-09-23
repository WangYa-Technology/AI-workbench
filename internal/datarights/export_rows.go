package datarights

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type exportChildQuery struct{ name, query string }

// A cursor returns one bounded record at a time and leaves the connection idle
// between fetches, so nested arrays can stream through their own cursor in the
// same repeatable-read transaction. No jsonb_agg of unbounded child histories.
// The SQL CASE refuses oversized records before pgx receives their body; it
// does not claim to bound PostgreSQL's memory when building one stored record.
func exportRows(ctx context.Context, tx pgx.Tx, query string, args []any, maxBytes int64, visit func([]byte) error) error {
	cursor := pgx.Identifier{"export_" + uuid.NewString()}.Sanitize()
	stmt := fmt.Sprintf(`DECLARE %s NO SCROLL CURSOR FOR SELECT CASE WHEN octet_length(value::text)<=%d THEN value ELSE NULL END FROM (%s) AS export_source(value)`, cursor, maxBytes, query)
	if _, err := tx.Exec(ctx, stmt, append([]any{pgx.QueryExecModeExec}, args...)...); err != nil {
		return err
	}
	defer func() { _, _ = tx.Exec(ctx, "CLOSE "+cursor, pgx.QueryExecModeExec) }()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var value []byte
		err := tx.QueryRow(ctx, "FETCH FORWARD 1 FROM "+cursor, pgx.QueryExecModeExec).Scan(&value)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if value == nil {
			return ErrExportRowTooLarge
		}
		if err = visit(value); err != nil {
			return err
		}
	}
}

func writeExportSection(ctx context.Context, tx pgx.Tx, w io.Writer, name, query string, userID uuid.UUID, scalar bool, maxRowBytes int64, children []exportChildQuery) error {
	key, _ := json.Marshal(name)
	if _, err := w.Write(append(key, ':')); err != nil {
		return err
	}
	if !scalar {
		if _, err := io.WriteString(w, "["); err != nil {
			return err
		}
	}
	count := 0
	err := exportRows(ctx, tx, query, []any{userID}, maxRowBytes, func(value []byte) error {
		if count > 0 {
			if _, err := io.WriteString(w, ","); err != nil {
				return err
			}
		}
		count++
		if len(children) == 0 {
			_, err := w.Write(value)
			return err
		}
		var parent struct {
			ID uuid.UUID `json:"id"`
		}
		if err := json.Unmarshal(value, &parent); err != nil {
			return err
		}
		if parent.ID == uuid.Nil || len(value) < 2 || value[len(value)-1] != '}' {
			return errors.New("invalid export parent record")
		}
		if _, err := w.Write(value[:len(value)-1]); err != nil {
			return err
		}
		for _, child := range children {
			if _, err := io.WriteString(w, ","); err != nil {
				return err
			}
			key, _ := json.Marshal(child.name)
			if _, err := w.Write(append(key, ':', '[')); err != nil {
				return err
			}
			n := 0
			if err := exportRows(ctx, tx, child.query, []any{userID, parent.ID}, maxRowBytes, func(record []byte) error {
				if n > 0 {
					if _, err := io.WriteString(w, ","); err != nil {
						return err
					}
				}
				n++
				_, err := w.Write(record)
				return err
			}); err != nil {
				return err
			}
			if _, err := io.WriteString(w, "]"); err != nil {
				return err
			}
		}
		_, err := io.WriteString(w, "}")
		return err
	})
	if err != nil {
		return fmt.Errorf("export %s: %w", name, err)
	}
	if scalar {
		if count != 1 {
			return errors.New("export account missing")
		}
		return nil
	}
	_, err = io.WriteString(w, "]")
	return err
}
