package payments

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRefundCheckHistoryPaginationScopeAndEvidence(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := t.Context()
	payment := operationalPaymentFixture(t, pool, false, time.Now())
	other := operationalPaymentFixture(t, pool, false, time.Now())
	service := newPaymentTestService(t, pool, ServiceConfig{}, NewRuntimeCatalog())
	ids := []uuid.UUID{
		uuid.MustParse("00000000-0000-4000-8000-000000000001"),
		uuid.MustParse("00000000-0000-4000-8000-000000000002"),
		uuid.MustParse("00000000-0000-4000-8000-000000000003"),
	}
	for i, id := range ids {
		var job uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload,status) VALUES($1,jsonb_build_object('checkId',$2::text),'succeeded') RETURNING id`, ProductRefundCheckJobKind, id).Scan(&job); err != nil {
			t.Fatal(err)
		}
		status, observations := "completed", `[{"providerId":"re_older","providerPaymentId":"pi_history","amountCents":100,"currency":"USD","status":"succeeded"}]`
		if i == 2 {
			status, observations = "failed", "[]"
		}
		if _, err := pool.Exec(ctx, `INSERT INTO product_refund_checks(id,payment_id,job_id,origin,status,observations,observed_at,completed_at,created_at,unresolved_count)
 VALUES($1,$2,$3,'automatic',$4,$5::jsonb,CASE WHEN $4='completed' THEN '2026-09-01T00:00:00Z'::timestamptz ELSE NULL END,'2026-09-01','2026-09-01',1)`, id, payment, job, status, observations); err != nil {
			t.Fatal(err)
		}
	}
	first, err := service.ListRefundChecks(ctx, payment, "all", "", 1)
	if err != nil || len(first.Items) != 1 || first.Items[0].ID != ids[2] || first.Items[0].RequiresReview || first.NextCursor == nil {
		t.Fatal("latest failed check hid the historical directory", first, err)
	}
	second, err := service.ListRefundChecks(ctx, payment, "all", *first.NextCursor, 1)
	if err != nil || len(second.Items) != 1 || second.Items[0].ID != ids[1] || !second.Items[0].RequiresReview || second.NextCursor == nil {
		t.Fatal("same-time pagination lost evidence", second, err)
	}
	last, err := service.ListRefundChecks(ctx, payment, "all", *second.NextCursor, 1)
	if err != nil || len(last.Items) != 1 || last.Items[0].ID != ids[0] || last.NextCursor != nil {
		t.Fatal(last, err)
	}
	filtered, err := service.ListRefundChecks(ctx, payment, "unresolved", "", 1)
	if err != nil || len(filtered.Items) != 1 || filtered.Items[0].ID != ids[1] || filtered.NextCursor == nil {
		t.Fatal("filter applied after pagination", filtered, err)
	}
	for _, args := range []struct {
		review, cursor string
		limit          int
	}{
		{"invalid", "", 20}, {"all", "invalid", 20}, {"all", "", -1}, {"all", "", 51},
		{"unresolved", *first.NextCursor, 1}, {"all", strings.Repeat("x", 513), 1},
	} {
		if _, err := service.ListRefundChecks(ctx, payment, args.review, args.cursor, args.limit); !errors.Is(err, ErrInvalidRefund) {
			t.Fatal("invalid or mismatched query accepted", args, err)
		}
	}
	if _, err = service.ListRefundChecks(ctx, other, "all", *first.NextCursor, 1); !errors.Is(err, ErrInvalidRefund) {
		t.Fatal("cross-payment cursor accepted", err)
	}
	if _, err = service.GetRefundCheck(ctx, other, ids[1]); !errors.Is(err, ErrRefundHistoryNotFound) {
		t.Fatal("cross-payment detail exposed", err)
	}
	detail, err := service.GetRefundCheck(ctx, payment, ids[1])
	if err != nil || len(detail.Observations) != 1 || len(detail.UnresolvedProviderRefundIDs) != 1 || detail.UnresolvedProviderRefundIDs[0] != "re_older" {
		t.Fatal("original observation unavailable", detail, err)
	}
	encoded, err := json.Marshal(first)
	if err != nil || strings.Contains(string(encoded), "observations") {
		t.Fatal("directory returned full observation arrays", err)
	}
	encoded, err = json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"requestedBy", "jobId", "checkoutUrl", "storageKey"} {
		if strings.Contains(string(encoded), private) {
			t.Fatal("private operational field leaked", private)
		}
	}
	page, err := service.ListRefundChecks(ctx, other, "", "", 0)
	if err != nil || page.Items == nil || len(page.Items) != 0 {
		t.Fatal("empty history mismatch", page, err)
	}
	if _, err = service.ListRefundChecks(ctx, uuid.New(), "all", "", 1); !errors.Is(err, ErrRefundHistoryNotFound) {
		t.Fatal("unknown payment accepted", err)
	}
}
