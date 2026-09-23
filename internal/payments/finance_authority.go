package payments

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrFinanceForbidden = errors.New("active finance permission required")

// Commands perform an early read, then pin authority after resource locks and
// before writes. Under Serializable, a committed revocation since the snapshot
// aborts the locking read; later revocations wait until the command finishes.
func paymentFinanceAuthority(ctx context.Context, tx pgx.Tx, actor uuid.UUID, lock bool) error {
	query := `SELECT u.id FROM users u JOIN role_permissions rp ON rp.role=u.role
 WHERE u.id=$1 AND u.status='active' AND rp.permission_id='admin:finance'`
	if lock {
		query += ` FOR SHARE OF u,rp`
	}
	var id uuid.UUID
	err := tx.QueryRow(ctx, query, actor).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrFinanceForbidden
	}
	return err
}
