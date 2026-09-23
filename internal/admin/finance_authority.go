package admin

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Check before reading private resources; pin after resource/eligibility locks
// and before enqueue. Serializable snapshot reads alone cannot detect revocation.
func financeAuthorityTx(ctx context.Context, tx pgx.Tx, actor uuid.UUID, lock bool) error {
	query := `SELECT u.id FROM users u JOIN role_permissions rp ON rp.role=u.role
 WHERE u.id=$1 AND u.status='active' AND rp.permission_id='admin:finance'`
	if lock {
		query += " FOR SHARE OF u,rp"
	}
	var id uuid.UUID
	err := tx.QueryRow(ctx, query, actor).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrForbidden
	}
	return err
}

func financeCommandError(err error) error {
	var conflict *pgconn.PgError
	if errors.As(err, &conflict) && (conflict.Code == "40001" || conflict.Code == "40P01") {
		return ErrConflict
	}
	return err
}
