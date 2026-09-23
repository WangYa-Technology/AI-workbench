package payments

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

func payoutReviewFixture(t *testing.T) (*pgxpool.Pool, *Service, uuid.UUID, SellerPayoutRequest, SellerPayoutReviewInput) {
	t.Helper()
	pool, service, _, request := sellerBankFixture(t)
	actor := uuid.New()
	if _, err := pool.Exec(t.Context(), `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Finance reviewer','admin')`, actor, actor.String()+"@test.local", "review_"+actor.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := service.BindSellerPayoutBankTarget(t.Context(), request.SellerID, request.ID, "ba_original"); err != nil {
		t.Fatal(err)
	}
	var settlement uuid.UUID
	if err := pool.QueryRow(t.Context(), `SELECT settlement_id FROM seller_payout_request_allocations WHERE payout_request_id=$1`, request.ID).Scan(&settlement); err != nil {
		t.Fatal(err)
	}
	return pool, service, actor, request, SellerPayoutReviewInput{SettlementID: settlement, AmountCents: request.AmountCents, BankDestinationID: "ba_original", Decision: "approved", Reason: "Verified the seller, order, source and selected bank.", SellerMessage: "Your reservation has been reviewed. No bank payout has been sent."}
}

func TestSellerPayoutReviewApprovalRejectionAndReplay(t *testing.T) {
	pool, s, actor, request, input := payoutReviewFixture(t)
	ctx := t.Context()
	first, err := s.ReviewSellerPayout(ctx, actor, request.ID, input, "review-first-key", "original-http-request")
	if err != nil || first.Replayed || first.Review.Revision != 1 || first.Request.Status != "under_review" {
		t.Fatalf("approval %+v: %v", first, err)
	}
	if first.Request.BankName != "Example Bank" || first.Request.Last4 != "6789" {
		t.Fatalf("review omitted bank snapshot: %+v", first.Request)
	}
	s.config.Enabled = false
	replay, err := s.ReviewSellerPayout(ctx, actor, request.ID, input, "review-first-key", "retry-http-request")
	if err != nil || !replay.Replayed || replay.Review.ID != first.Review.ID {
		t.Fatalf("replay %+v: %v", replay, err)
	}
	for _, change := range []string{"amount", "settlement", "bank", "reason", "message", "revision", "decision", "request"} {
		altered, id := input, request.ID
		switch change {
		case "amount":
			altered.AmountCents++
		case "settlement":
			altered.SettlementID = uuid.New()
		case "bank":
			altered.BankDestinationID = "ba_another"
		case "reason":
			altered.Reason = "A different review explanation."
		case "message":
			altered.SellerMessage = "A different seller-facing explanation."
		case "revision":
			altered.ExpectedRevision = 1
		case "decision":
			altered.Decision = "rejected"
		case "request":
			id = uuid.New()
		}
		if _, err := s.ReviewSellerPayout(ctx, actor, id, altered, "review-first-key", "changed"); !errors.Is(err, ErrSellerPayoutReviewConflict) {
			t.Fatalf("changed %s: %v", change, err)
		}
	}
	input.ExpectedRevision = 1
	input.Decision = "rejected"
	input.Reason = "Cancelled the prior approval before any source dispatch."
	rejected, err := s.ReviewSellerPayout(ctx, actor, request.ID, input, "review-rejection-key", "reject-http-request")
	if err != nil || rejected.Review.Revision != 2 || rejected.Request.Status != "cancelled" {
		t.Fatalf("rejection %+v: %v", rejected, err)
	}
	if _, err := s.ReviewSellerPayout(ctx, actor, request.ID, input, "review-rejection-key", "retry"); err != nil {
		t.Fatal(err)
	}
	balance, err := singleSellerFunds(t, s, ctx, request.SellerID)
	if err != nil || balance.ReservedCents != 0 || balance.WithdrawableCents != request.AmountCents {
		t.Fatalf("rejection did not release once %+v: %v", balance, err)
	}
	var reviews, audits, releases, transfers, jobs int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM seller_payout_reviews),(SELECT count(*) FROM audit_events WHERE resource_type='seller_payout_request'),
 (SELECT count(*) FROM seller_ledger_entries WHERE entry_type='payout_release'),(SELECT count(*) FROM seller_payout_transfers),
 (SELECT count(*) FROM jobs WHERE kind='payment.fund_seller_payout')`).Scan(&reviews, &audits, &releases, &transfers, &jobs); err != nil || reviews != 2 || audits != 2 || releases != 1 || transfers != 0 || jobs != 0 {
		t.Fatalf("evidence %d/%d/%d/%d/%d: %v", reviews, audits, releases, transfers, jobs, err)
	}
	var trace string
	if err := pool.QueryRow(ctx, `SELECT request_id FROM seller_payout_reviews WHERE id=$1`, first.Review.ID).Scan(&trace); err != nil || trace != "original-http-request" {
		t.Fatal("replay rewrote attribution", trace, err)
	}
	for _, sql := range []string{`DELETE FROM seller_payout_reviews`, `UPDATE seller_payout_reviews SET decision='rejected'`, `UPDATE seller_payout_reviews SET reason='Replacement reason'`} {
		if _, err := pool.Exec(ctx, sql); err == nil {
			t.Fatal("review evidence was mutable", sql)
		}
	}
	body, err := os.ReadFile("../platform/database/migrations/0160_seller_payout_reviews.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, string(body)); err == nil {
		t.Fatal("downgrade deleted review evidence")
	}
}

func TestSellerPayoutReviewDirectoryAuthorityAndCancellation(t *testing.T) {
	pool, s, actor, request, input := payoutReviewFixture(t)
	ctx := t.Context()
	if _, err := s.GetSellerPayoutReview(ctx, request.SellerID, request.ID); !errors.Is(err, ErrFinanceForbidden) {
		t.Fatal("seller read finance detail", err)
	}
	if _, err := s.SellerPayoutReviewDirectory(ctx, request.SellerID, "", 20); !errors.Is(err, ErrFinanceForbidden) {
		t.Fatal("seller read finance directory", err)
	}
	page, err := s.SellerPayoutReviewDirectory(ctx, actor, "", 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].BankDestinationID != "ba_original" || page.Items[0].Environment != "test" || page.NextCursor != "" {
		t.Fatalf("directory %+v: %v", page, err)
	}
	if _, err := s.ReviewSellerPayout(ctx, actor, request.ID, input, "review-before-cancel", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CancelSellerPayoutRequest(ctx, request.SellerID, request.ID); err != nil {
		t.Fatal(err)
	}
	replay, err := s.ReviewSellerPayout(ctx, actor, request.ID, input, "review-before-cancel", "test")
	if err != nil || !replay.Replayed || replay.Request.Status != "cancelled" {
		t.Fatalf("replay hid cancellation %+v %v", replay, err)
	}
	second, err := s.CreateSellerPayoutRequestForSettlement(ctx, request.SellerID, input.SettlementID, input.AmountCents, "new-after-reviewed-cancel")
	if err != nil {
		t.Fatal(err)
	}
	page, err = s.SellerPayoutReviewDirectory(ctx, actor, "", 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != second.ID || page.NextCursor == "" {
		t.Fatalf("first page %+v %v", page, err)
	}
	page, err = s.SellerPayoutReviewDirectory(ctx, actor, page.NextCursor, 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != request.ID || page.NextCursor != "" || page.Items[0].LatestReview == nil {
		t.Fatalf("second page %+v %v", page, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReviewSellerPayout(ctx, actor, request.ID, input, "review-before-cancel", "test"); !errors.Is(err, ErrFinanceForbidden) {
		t.Fatal("revoked actor replayed", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1`, request.SellerID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReviewSellerPayout(ctx, request.SellerID, request.ID, input, "self-review-command", "test"); !errors.Is(err, ErrFinanceForbidden) {
		t.Fatal("self review permitted", err)
	}
}

func TestSellerPayoutReviewConcurrentDecisions(t *testing.T) {
	pool, s, actor, request, input := payoutReviewFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	start, done := make(chan struct{}), make(chan error, 2)
	for _, decision := range []string{"approved", "rejected"} {
		go func() {
			<-start
			in := input
			in.Decision = decision
			_, err := s.ReviewSellerPayout(ctx, actor, request.ID, in, "parallel-"+decision, "test")
			done <- err
		}()
	}
	close(start)
	passed, conflict := 0, 0
	for range 2 {
		err := <-done
		if err == nil {
			passed++
		} else if errors.Is(err, ErrSellerPayoutReviewConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if passed != 1 || conflict != 1 {
		t.Fatalf("stale review committed: %d/%d", passed, conflict)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM seller_payout_reviews`).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate decision", count, err)
	}
}

func TestSellerPayoutReviewConcurrentRetries(t *testing.T) {
	pool, s, actor, request, input := payoutReviewFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	type result struct {
		value SellerPayoutReviewResult
		err   error
	}
	start, done := make(chan struct{}), make(chan result, 6)
	for range 6 {
		go func() {
			<-start
			out, err := s.ReviewSellerPayout(ctx, actor, request.ID, input, "same-concurrent-review", "test")
			done <- result{out, err}
		}()
	}
	close(start)
	var id uuid.UUID
	created := 0
	for range 6 {
		r := <-done
		if r.err != nil {
			t.Fatal(r.err)
		}
		if id == uuid.Nil {
			id = r.value.Review.ID
		}
		if id != r.value.Review.ID {
			t.Fatal("retry created another decision")
		}
		if !r.value.Replayed {
			created++
		}
	}
	var reviews, audits, notices int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM seller_payout_reviews),(SELECT count(*) FROM audit_events WHERE resource_type='seller_payout_request'),(SELECT count(*) FROM notifications WHERE kind='marketplace.payout_reviewed')`).Scan(&reviews, &audits, &notices); err != nil || reviews != 1 || audits != 1 || notices != 1 || created != 1 {
		t.Fatalf("duplicate approval: created=%d reviews=%d audits=%d notifications=%d err=%v", created, reviews, audits, notices, err)
	}
}

func TestSellerPayoutReviewRevocationDuringWait(t *testing.T) {
	for _, change := range []string{"role", "status", "permission", "refund"} {
		t.Run(change, func(t *testing.T) {
			pool, s, actor, request, input := payoutReviewFixture(t)
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			traced, entered, release := testutil.GateQuery(t, pool, "SELECT id FROM seller_payout_requests WHERE id=$1 FOR UPDATE")
			defer release()
			blocked := NewServiceWithRuntimes(traced, s.config, s.runtimes)
			done := make(chan error, 1)
			go func() {
				_, err := blocked.ReviewSellerPayout(ctx, actor, request.ID, input, "blocked-review-key", "test")
				done <- err
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			var err error
			switch change {
			case "role":
				_, err = pool.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, actor)
			case "status":
				_, err = pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, actor)
			case "permission":
				_, err = pool.Exec(ctx, `DELETE FROM role_permissions WHERE role='admin' AND permission_id='admin:finance'`)
			case "refund":
				_, err = pool.Exec(ctx, `UPDATE product_settlements SET status='refund_hold' WHERE id=$1`, input.SettlementID)
			}
			if err != nil {
				t.Fatal(err)
			}
			release()
			err = <-done
			if change == "refund" {
				if !errors.Is(err, ErrSellerPayoutBankConflict) {
					t.Fatal("refund not rechecked", err)
				}
			} else if !errors.Is(err, ErrFinanceForbidden) {
				t.Fatal("revocation not rechecked", err)
			}
			var count int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM seller_payout_reviews`).Scan(&count); err != nil || count != 0 {
				t.Fatal("unauthorized review persisted", count, err)
			}
		})
	}
}

func TestSellerPayoutReviewAuditFailureRollsBackRelease(t *testing.T) {
	pool, s, actor, request, input := payoutReviewFixture(t)
	ctx := t.Context()
	before := sellerPayoutEvidenceCounts(t, pool)
	if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_payout_review_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.resource_type='seller_payout_request' THEN RAISE EXCEPTION 'audit unavailable'; END IF; RETURN NEW; END; $$;
 CREATE TRIGGER fail_payout_review_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_payout_review_audit()`); err != nil {
		t.Fatal(err)
	}
	input.Decision = "rejected"
	if _, err := s.ReviewSellerPayout(ctx, actor, request.ID, input, "audit-failure-review", "test"); err == nil {
		t.Fatal("review succeeded without audit")
	}
	if after := sellerPayoutEvidenceCounts(t, pool); before != after {
		t.Fatal("audit failure left financial writes", before, after)
	}
	item, err := s.GetSellerPayoutReview(ctx, actor, request.ID)
	if err != nil || item.Status != "under_review" || item.LatestReview != nil {
		t.Fatalf("partial rejection %+v %v", item, err)
	}
}

func TestSellerPayoutReviewRejectsChangedBankAccount(t *testing.T) {
	pool, s, actor, request, input := payoutReviewFixture(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `UPDATE payment_destinations SET destination_id='acct_reconnected' WHERE user_id=$1`, request.SellerID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReviewSellerPayout(ctx, actor, request.ID, input, "stale-bank-review", "test"); !errors.Is(err, ErrSellerPayoutReviewConflict) {
		t.Fatal("approved old bank against new account", err)
	}
	// The database independently protects stale approvals even if a writer
	// omits the service check. Rejection must remain possible to release funds.
	_, err := pool.Exec(ctx, `INSERT INTO seller_payout_reviews(payout_request_id,revision,actor_id,idempotency_key,decision,reason,settlement_id,amount_cents,currency,bank_destination_id,request_id,seller_message)
 VALUES($1,1,$2,'direct-stale-review','approved','Checked original source and bank',$3,$4,'USD','ba_original','test','Explicit seller-facing explanation')`, request.ID, actor, input.SettlementID, input.AmountCents)
	requirePayoutConstraint(t, err)
	input.Decision = "rejected"
	if _, err := s.ReviewSellerPayout(ctx, actor, request.ID, input, "reject-stale-bank", "test"); err != nil {
		t.Fatal(err)
	}
}

func TestSellerPayoutReviewRejectsUnboundRequestWithoutProvider(t *testing.T) {
	pool, s, _, request := sellerBankFixture(t)
	ctx := t.Context()
	actor := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Finance reviewer','admin')`, actor, actor.String()+"@test.local", "review_"+actor.String()[:8]); err != nil {
		t.Fatal(err)
	}
	item, err := s.GetSellerPayoutReview(ctx, actor, request.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.config.Enabled = false
	out, err := s.ReviewSellerPayout(ctx, actor, request.ID, SellerPayoutReviewInput{SettlementID: item.SettlementID, AmountCents: item.AmountCents, Decision: "rejected", Reason: "The seller requested cancellation before bank selection.", SellerMessage: "Your request was rejected and the reservation released."}, "reject-unbound-request", "test")
	if err != nil || out.Request.Status != "cancelled" || out.Review.Decision != "rejected" {
		t.Fatalf("unbound rejection %+v %v", out, err)
	}
}

func TestSellerPayoutReviewCannotChangeDispatchedRequest(t *testing.T) {
	pool, s, actor, request, input := payoutReviewFixture(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, reserveSellerTransferSQL, request.ID, input.SettlementID); err != nil {
		t.Fatal(err)
	}
	before := sellerPayoutEvidenceCounts(t, pool)
	for _, decision := range []string{"approved", "rejected"} {
		input.Decision = decision
		if _, err := s.ReviewSellerPayout(ctx, actor, request.ID, input, "after-source-"+decision, "test"); !errors.Is(err, ErrSellerPayoutReviewConflict) {
			t.Fatalf("review altered source-reserved request: %v", err)
		}
	}
	item, err := s.GetSellerPayoutReview(ctx, actor, request.ID)
	if err != nil || item.LatestReview != nil || item.Status != "under_review" {
		t.Fatalf("partial review %+v %v", item, err)
	}
	if after := sellerPayoutEvidenceCounts(t, pool); after != before {
		t.Fatal("review released reserved source funds", before, after)
	}
}
