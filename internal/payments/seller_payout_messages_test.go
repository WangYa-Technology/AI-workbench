package payments

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
)

func TestSellerPayoutMessagesOwnerProjectionAndNotification(t *testing.T) {
	pool, s, actor, request, input := payoutReviewFixture(t)
	ctx := t.Context()
	input.Reason = "INTERNAL_ONLY_NOTE_do_not_disclose"
	input.SellerMessage = "Your reservation is approved, but no bank payout has been sent."
	first, err := s.ReviewSellerPayout(ctx, actor, request.ID, input, "message-approval-key", "private-trace")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReviewSellerPayout(ctx, actor, request.ID, input, "message-approval-key", "retry"); err != nil {
		t.Fatal(err)
	}
	checkOwner := func(decision string, revision int, message string) {
		t.Helper()
		detail, err := s.GetSellerPayoutRequest(ctx, request.SellerID, request.ID)
		if err != nil || detail.LatestReview == nil || detail.LatestReview.Decision != decision || detail.LatestReview.Revision != revision || detail.LatestReview.SellerMessage != message {
			t.Fatalf("owner %+v %v", detail, err)
		}
		page, err := s.ListSellerPayoutRequests(ctx, request.SellerID, "", 1)
		if err != nil || len(page.Items) != 1 || page.Items[0].LatestReview == nil || *page.Items[0].LatestReview != *detail.LatestReview {
			t.Fatalf("directory %+v %v", page, err)
		}
		body, _ := json.Marshal(detail)
		for _, private := range []string{input.Reason, actor.String(), "private-trace", "message-approval-key", `"actorId"`, `"reason"`} {
			if strings.Contains(string(body), private) {
				t.Fatalf("private review content leaked: %s", private)
			}
		}
	}
	checkOwner("approved", 1, input.SellerMessage)
	if _, err := s.GetSellerPayoutRequest(ctx, actor, request.ID); !errors.Is(err, ErrSellerPayoutNotFound) {
		t.Fatalf("foreign detail %v", err)
	}
	if _, err := s.GetSellerPayoutRequest(ctx, request.SellerID, uuid.New()); !errors.Is(err, ErrSellerPayoutNotFound) {
		t.Fatalf("missing detail %v", err)
	}
	var count, jobCount int
	if err := pool.QueryRow(ctx, `SELECT count(*),(SELECT count(*) FROM jobs j JOIN notifications n ON j.payload->>'notificationId'=n.id::text WHERE n.kind='marketplace.payout_reviewed') FROM notifications WHERE kind='marketplace.payout_reviewed'`).Scan(&count, &jobCount); err != nil || count != 1 || jobCount != 1 {
		t.Fatalf("duplicate notification %d/%d %v", count, jobCount, err)
	}
	repository := notifications.NewRepository(pool)
	var notificationID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM notifications WHERE source_key=$1`, "marketplace:payout-review:"+first.Review.ID.String()).Scan(&notificationID); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"notificationId": notificationID})
	job := jobs.Job{Kind: notifications.JobKind, Payload: payload}
	if err := repository.HandleDeliveryJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := repository.HandleDeliveryJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	inbox, err := repository.List(ctx, request.SellerID, notifications.ListInput{Kind: "marketplace.payout_reviewed"})
	if err != nil || len(inbox.Items) != 1 || inbox.Items[0].TargetPath != "/workspace/payouts/"+request.ID.String() {
		t.Fatalf("inbox %+v %v", inbox, err)
	}
	if strings.Contains(inbox.Items[0].Body, input.Reason) || strings.Contains(inbox.Items[0].Body, input.SellerMessage) {
		t.Fatal("notification copied private or free-form review text")
	}
	if _, err := repository.MarkRead(ctx, actor, notificationID); !errors.Is(err, notifications.ErrNotFound) {
		t.Fatal("foreign notification", err)
	}
	if _, err := repository.UpdatePreference(ctx, request.SellerID, "marketplace.payout_reviewed", false, 1); err != nil {
		t.Fatal(err)
	}
	input.ExpectedRevision = 1
	input.Decision = "rejected"
	input.SellerMessage = "Your request was rejected and the reservation released."
	second, err := s.ReviewSellerPayout(ctx, actor, request.ID, input, "message-rejection-key", "reject")
	if err != nil {
		t.Fatal(err)
	}
	checkOwner("rejected", 2, input.SellerMessage)
	if err := pool.QueryRow(ctx, `SELECT id FROM notifications WHERE source_key=$1`, "marketplace:payout-review:"+second.Review.ID.String()).Scan(&notificationID); err != nil {
		t.Fatal(err)
	}
	payload, _ = json.Marshal(map[string]any{"notificationId": notificationID})
	if err := repository.HandleDeliveryJob(ctx, jobs.Job{Kind: notifications.JobKind, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := pool.QueryRow(ctx, `SELECT delivery_status FROM notifications WHERE id=$1`, notificationID).Scan(&state); err != nil || state != "suppressed" {
		t.Fatal("preference not respected", state, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, request.SellerID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSellerPayoutRequest(ctx, request.SellerID, request.ID); !errors.Is(err, ErrSellerPayoutNotFound) {
		t.Fatal("suspended owner read", err)
	}
}

func TestSellerPayoutMessagesNotificationFailureRollsBackAndRetries(t *testing.T) {
	pool, s, actor, request, input := payoutReviewFixture(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_review_notification() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind='marketplace.payout_reviewed' THEN RAISE EXCEPTION 'notification unavailable'; END IF; RETURN NEW; END; $$;
 CREATE TRIGGER fail_review_notification BEFORE INSERT ON notifications FOR EACH ROW EXECUTE FUNCTION fail_review_notification()`); err != nil {
		t.Fatal(err)
	}
	before := sellerPayoutEvidenceCounts(t, pool)
	input.Decision = "rejected"
	if _, err := s.ReviewSellerPayout(ctx, actor, request.ID, input, "retry-notification-key", "test"); err == nil {
		t.Fatal("succeeded without durable notification")
	}
	if after := sellerPayoutEvidenceCounts(t, pool); before != after {
		t.Fatal("partial funds change", before, after)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM seller_payout_reviews`).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial decision", count, err)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER fail_review_notification ON notifications`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReviewSellerPayout(ctx, actor, request.ID, input, "retry-notification-key", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReviewSellerPayout(ctx, actor, request.ID, input, "retry-notification-key", "test"); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE kind='marketplace.payout_reviewed'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("retry duplicate", count, err)
	}
}

func TestSellerPayoutMessagesLegacyPrivacyValidationAndMigration(t *testing.T) {
	pool, s, actor, request, input := payoutReviewFixture(t)
	ctx := t.Context()
	for _, message := range []string{"", "short", strings.Repeat("🙂", 1001), "bad\x00message is invalid"} {
		changed := input
		changed.SellerMessage = message
		if _, err := s.ReviewSellerPayout(ctx, actor, request.ID, changed, "invalid-message-key", "test"); !errors.Is(err, ErrSellerPayoutReviewInvalid) {
			t.Fatalf("invalid public message accepted %v", err)
		}
	}
	// Exercise a real pre-0161 record, not an invented public-message backfill.
	down, err := os.ReadFile("../platform/database/migrations/0161_seller_payout_messages.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, err := os.ReadFile("../platform/database/migrations/0161_seller_payout_messages.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(down)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO seller_payout_reviews(payout_request_id,revision,actor_id,idempotency_key,decision,reason,settlement_id,amount_cents,currency,bank_destination_id,request_id)
 VALUES($1,1,$2,'legacy-message-key','approved',$3,$4,$5,'USD',$6,'legacy')`, request.ID, actor, input.Reason, input.SettlementID, input.AmountCents, input.BankDestinationID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(up)); err != nil {
		t.Fatal(err)
	}
	input.SellerMessage = ""
	replay, err := s.ReviewSellerPayout(ctx, actor, request.ID, input, "legacy-message-key", "retry")
	if err != nil || !replay.Replayed {
		t.Fatalf("legacy retry %+v %v", replay, err)
	}
	detail, err := s.GetSellerPayoutRequest(ctx, request.SellerID, request.ID)
	if err != nil || detail.LatestReview == nil || detail.LatestReview.SellerMessage != "" {
		t.Fatalf("legacy projection %+v %v", detail, err)
	}
	body, _ := json.Marshal(detail)
	if strings.Contains(string(body), input.Reason) {
		t.Fatal("internal legacy note leaked")
	}
	exporter, exportID, exportJob := sellerExportFixture(t, pool, request.SellerID)
	exported, exportBody := sellerExportData(t, exporter, request.SellerID, exportID, exportJob)
	if len(exported["sellerPayoutReviews"]) != 1 || exported["sellerPayoutReviews"][0]["sellerMessage"] != "" || strings.Contains(string(exportBody), input.Reason) {
		t.Fatal("legacy export omitted the decision or exposed its internal note")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO seller_payout_reviews(payout_request_id,revision,actor_id,idempotency_key,decision,reason,settlement_id,amount_cents,currency,bank_destination_id,request_id)
 VALUES($1,2,$2,'missing-message-key','rejected',$3,$4,$5,'USD',$6,'new')`, request.ID, actor, input.Reason, input.SettlementID, input.AmountCents, input.BankDestinationID); err == nil {
		t.Fatal("database accepted new review without message")
	}
	input.ExpectedRevision = 1
	input.Decision = "rejected"
	input.SellerMessage = strings.Repeat("🙂", 1000)
	if _, err := s.ReviewSellerPayout(ctx, actor, request.ID, input, "unicode-message-key", "new"); err != nil {
		t.Fatal("valid Unicode rejected", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE seller_payout_reviews SET seller_message='replacement public message'`); err == nil {
		t.Fatal("public explanation mutable")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, string(down)); err == nil {
		t.Fatal("downgrade erased public evidence")
	}
	_ = tx.Rollback(ctx)
}
