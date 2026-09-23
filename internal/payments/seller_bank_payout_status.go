package payments

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/jackc/pgx/v5"
)

// SellerBankPayoutStatus exposes accepted bank evidence, not operational jobs,
// raw provider responses, operator identities or private confirmation reasons.
type SellerBankPayoutStatus struct {
	Status         string     `json:"status"`
	RequiresReview bool       `json:"requiresReview"`
	ObservedAt     *time.Time `json:"observedAt,omitempty"`
	CheckedAt      *time.Time `json:"checkedAt,omitempty"`
}

// A later pending observation must not hide paid, and paid must not hide a
// confirmed failure/return. Both finance and seller views use the same order.
const sellerBankResultOrder = `CASE status WHEN 'failed' THEN 5 WHEN 'canceled' THEN 5 WHEN 'paid' THEN 4 WHEN 'in_transit' THEN 3 ELSE 2 END DESC,created_at DESC,id DESC`

// r is the owner-filtered seller_payout_requests row in the enclosing query.
const sellerBankPayoutStatusSelect = `(SELECT jsonb_strip_nulls(jsonb_build_object(
 'status',COALESCE(result.status,'unconfirmed'),'observedAt',result.created_at,
 'checkedAt',(SELECT max(finished_at) FROM seller_bank_payout_reads WHERE command_id=c.id),
 'requiresReview',COALESCE(result.status IN ('failed','canceled'),false) OR r.status='reconciliation_required'
 OR EXISTS(SELECT 1 FROM seller_bank_payout_reads WHERE command_id=c.id AND
 (requires_review OR (finished_at IS NULL AND deadline_at+interval '5 seconds'<statement_timestamp())))))
 FROM seller_bank_payout_commands c
 LEFT JOIN LATERAL (SELECT status,created_at FROM seller_bank_payout_results WHERE command_id=c.id
 ORDER BY ` + sellerBankResultOrder + ` LIMIT 1) result ON true
 WHERE c.payout_request_id=r.id AND c.seller_id=r.seller_id)`

func notifySellerBankResultTx(ctx context.Context, tx pgx.Tx, state sellerBankState, status string, returned bool) error {
	var kind, title, body string
	switch {
	case status == "paid":
		kind, title = "marketplace.payout_paid", "Bank payout confirmed"
		body = "A bank payment was confirmed for your payout request. Open the request for its current status."
	case returned:
		kind, title = "marketplace.payout_returned", "Bank payout returned"
		body = "A bank payout was returned. Funds remain reserved while the result is reviewed. Open the request for its current status."
	case oneOf(status, "failed", "canceled"):
		kind, title = "marketplace.payout_failed", "Bank payout needs review"
		body = "The bank did not complete this payout. Open the request for its current status and next steps."
	default:
		return nil
	}
	request := state.Input.PayoutRequestID
	return notifications.CreateTx(ctx, tx, notifications.CreateInput{
		UserID: state.SellerID, Kind: kind, Title: title, Body: body,
		TargetPath: "/workspace/payouts/" + request.String(), ResourceType: "seller_payout_request", ResourceID: &request,
		SourceKey: sellerBankNotificationKey(state.CommandID, kind),
	})
}

func sellerBankNotificationKey(command uuid.UUID, kind string) string {
	return "marketplace:bank-result:" + command.String() + ":" + kind
}
