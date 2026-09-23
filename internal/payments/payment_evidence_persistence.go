package payments

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// Keep the already verified response in this execution while PostgreSQL retries
// an aborted transaction. Checkout reads, session lookups, merchant identity
// recovery and refund reads retry only the local write of that response.
// An expired worker cannot rely on job redelivery to preserve its response.
// Only proven transaction aborts are retried here, never provider operations,
// constraint errors or uncertain connection/commit failures.
func retryPaymentEvidenceWrite(ctx context.Context, write func(context.Context) (bool, error)) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for delay := 20 * time.Millisecond; ; delay = min(delay*2, 200*time.Millisecond) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		stored, err := write(ctx)
		var conflict *pgconn.PgError
		if !errors.As(err, &conflict) || (conflict.Code != "40001" && conflict.Code != "40P01") {
			return stored, err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false, ctx.Err()
		case <-timer.C:
		}
	}
}
