package payments

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const connectAccountCommandVersion = "stripe-connect-account-v1"

var ErrPayoutReconciliation = errors.New("payout account creation requires reconciliation")

func reservePayoutAccountTx(ctx context.Context, tx pgx.Tx, runtime ProviderRuntime, userID uuid.UUID, email string, liveMode bool) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payout_account_commands WHERE user_id=$1)`, userID).Scan(&exists); err != nil || exists {
		return err
	}
	identity, err := checkoutIdentity(ctx, runtime)
	if err != nil {
		return err
	}
	if identity.Provider != "stripe" || identity.LiveMode != liveMode || !validStripeID(identity.MerchantID, "acct_") {
		return ErrPayoutReconciliation
	}
	body, err := json.Marshal(identity)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO payout_account_commands(user_id,identity,email,command_version,idempotency_key)
	 VALUES($1,$2,$3,$4,$5)`, userID, body, email, connectAccountCommandVersion, "connect-account-"+userID.String())
	return err
}

func loadPayoutAccountCommandTx(ctx context.Context, tx pgx.Tx, runtime ProviderRuntime, userID uuid.UUID, liveMode bool) (ConnectAccountRequest, error) {
	// Authenticate before the database deadline check so a slow identity read
	// cannot turn an eligible request into a late external write.
	current, err := checkoutIdentity(ctx, runtime)
	if err != nil {
		return ConnectAccountRequest{}, err
	}
	var body []byte
	var email, version, key string
	var remaining float64
	var eligible bool
	err = tx.QueryRow(ctx, `SELECT identity,email,command_version,idempotency_key,
	 reserved_at <= clock_timestamp() AND reserved_at > clock_timestamp()-interval '23 hours' AND completed_at IS NULL,
	 extract(epoch FROM reserved_at+interval '23 hours'-clock_timestamp())::float8
	 FROM payout_account_commands WHERE user_id=$1 FOR UPDATE`, userID).Scan(&body, &email, &version, &key, &eligible, &remaining)
	if errors.Is(err, pgx.ErrNoRows) {
		return ConnectAccountRequest{}, ErrPayoutReconciliation
	}
	if err != nil {
		return ConnectAccountRequest{}, err
	}
	var original ProductCheckoutIdentity
	if json.Unmarshal(body, &original) != nil || original != current || current.Provider != "stripe" || current.LiveMode != liveMode ||
		version != connectAccountCommandVersion || key != "connect-account-"+userID.String() || !eligible || remaining <= 0 {
		return ConnectAccountRequest{}, ErrPayoutReconciliation
	}
	return ConnectAccountRequest{UserID: userID, Email: email, Identity: &original,
		RetryBefore: time.Now().Add(time.Duration(remaining * float64(time.Second)))}, nil
}

func readPayoutAccountCommandStatus(ctx context.Context, query interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, userID uuid.UUID, item PayoutStatus) (PayoutStatus, error) {
	var reservedAt time.Time
	var retryable bool
	err := query.QueryRow(ctx, `SELECT reserved_at,
	 reserved_at <= clock_timestamp() AND reserved_at > clock_timestamp()-interval '23 hours'
	 AND completed_at IS NULL AND identity->'liveMode'=to_jsonb($2::boolean)
	 FROM payout_account_commands WHERE user_id=$1`, userID, item.LiveMode).Scan(&reservedAt, &retryable)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, nil
	}
	if err != nil {
		return PayoutStatus{}, err
	}
	item.Status = "creation_pending"
	if !retryable {
		item.Status = "recovery_required"
	}
	item.CanStartOnboarding = item.ProviderAvailable && retryable
	item.UpdatedAt = &reservedAt
	return item, nil
}
