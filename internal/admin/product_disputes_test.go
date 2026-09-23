package admin_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/jackc/pgx/v5/pgxpool"
)

type productDisputeFixture struct {
	pool          *pgxpool.Pool
	service       *admin.Service
	finance, user uuid.UUID
	disputes      []uuid.UUID
}

func newProductDisputeFixture(t *testing.T) productDisputeFixture {
	t.Helper()
	pool, cleanup := testPool(t)
	t.Cleanup(cleanup)
	f := productDisputeFixture{
		pool: pool, service: admin.NewService(pool, true), finance: uuid.New(), user: uuid.New(),
	}
	for _, account := range []struct {
		id   uuid.UUID
		role string
	}{{f.finance, "admin"}, {f.user, "member"}} {
		_, err := pool.Exec(t.Context(), `INSERT INTO users(id,email,handle,display_name,role)
			VALUES($1,$2,$3,$4,$5)`, account.id, account.id.String()+"@test.local",
			"dispute_"+account.id.String()[:8], "Dispute operator", account.role)
		if err != nil {
			t.Fatal(err)
		}
	}

	base := time.Date(2026, 9, 23, 4, 0, 0, 0, time.UTC)
	statuses := []string{"needs_response", "under_review", "won", "lost"}
	for i, status := range statuses {
		disputeID, eventID := uuid.New(), uuid.New()
		disputeReference := strings.ReplaceAll(disputeID.String(), "-", "_")
		f.disputes = append(f.disputes, disputeID)
		dueBy := base.Add(time.Duration(i) * time.Hour)
		_, err := pool.Exec(t.Context(), `INSERT INTO payment_provider_events(
			id,provider,provider_event_id,event_type,api_version,live_mode,occurred_at,payload_sha256,
			object_id,object_type,purpose,dispute_status,dispute_reason,dispute_network_reason_code,dispute_due_by)
			VALUES($1,'stripe',$2,'charge.dispute.updated','2026-02-25.clover',$3,$4,repeat('a',64),$5,'dispute','product',$6,'fraudulent','visa_10_4',$7)`,
			eventID, "evt_dispute_"+eventID.String(), i%2 == 0, dueBy.Add(-time.Hour), "dp_"+disputeReference, status, dueBy)
		if err != nil {
			t.Fatal(err)
		}
		_, err = pool.Exec(t.Context(), `INSERT INTO product_payment_disputes(
			id,provider,live_mode,provider_dispute_id,provider_payment_id,provider_charge_id,amount_cents,currency,
			provider_status,action_status,due_by,latest_event_at,latest_event_id)
			VALUES($1,'stripe',$2,$3,$4,$5,1900,'USD',$6,$6,$7,$8,$9)`, disputeID, i%2 == 0,
			"dp_"+disputeReference, "pi_"+disputeReference, "ch_"+disputeReference, status, dueBy, dueBy.Add(-time.Hour), eventID)
		if err != nil {
			t.Fatal(err)
		}
		_, err = pool.Exec(t.Context(), `INSERT INTO product_payment_dispute_events(
			dispute_id,provider_event_id,event_type,provider_status,reason,network_reason_code,due_by,occurred_at,applied)
			VALUES($1,$2,'charge.dispute.updated',$3,'fraudulent','visa_10_4',$4,$5,true)`,
			disputeID, eventID, status, dueBy, dueBy.Add(-time.Hour))
		if err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func disputeCommand(action, route string, version int64) admin.ProductPaymentDisputeCommand {
	return admin.ProductPaymentDisputeCommand{
		Action: action, Route: route, ExpectedVersion: version,
		Reason: "Reviewed against provider evidence.", Confirmed: true,
	}
}

func TestProductPaymentDisputeDirectoryAuthorizationAndCursor(t *testing.T) {
	f := newProductDisputeFixture(t)
	if _, err := f.service.ListProductPaymentDisputes(t.Context(), f.user, admin.ProductPaymentDisputeListInput{}); !errors.Is(err, admin.ErrForbidden) {
		t.Fatalf("member read private dispute directory: %v", err)
	}

	first, err := f.service.ListProductPaymentDisputes(t.Context(), f.finance, admin.ProductPaymentDisputeListInput{Limit: 2})
	if err != nil || len(first.Items) != 2 || first.NextCursor == nil {
		t.Fatalf("first page mismatch: %#v %v", first, err)
	}
	if first.Items[0].ActionStatus != "needs_response" || first.Items[1].ActionStatus != "under_review" {
		t.Fatalf("priority ordering mismatch: %#v", first.Items)
	}
	second, err := f.service.ListProductPaymentDisputes(t.Context(), f.finance, admin.ProductPaymentDisputeListInput{Limit: 2, Cursor: *first.NextCursor})
	if err != nil || len(second.Items) != 2 || second.Items[0].ActionStatus != "lost" || second.Items[1].ActionStatus != "won" || second.NextCursor != nil {
		t.Fatalf("second page mismatch: %#v %v", second, err)
	}
	if _, err := f.service.ListProductPaymentDisputes(t.Context(), f.finance, admin.ProductPaymentDisputeListInput{
		Limit: 2, Cursor: *first.NextCursor, Mode: "live",
	}); !errors.Is(err, admin.ErrInvalidProductDisputeFilter) {
		t.Fatalf("cross-filter cursor accepted: %v", err)
	}
	modified := *first.NextCursor
	if strings.HasSuffix(modified, "A") {
		modified = modified[:len(modified)-1] + "B"
	} else {
		modified = modified[:len(modified)-1] + "A"
	}
	if _, err := f.service.ListProductPaymentDisputes(t.Context(), f.finance, admin.ProductPaymentDisputeListInput{
		Limit: 2, Cursor: modified,
	}); !errors.Is(err, admin.ErrInvalidProductDisputeFilter) {
		t.Fatalf("modified cursor accepted: %v", err)
	}
	if _, err := f.service.GetProductPaymentDispute(t.Context(), f.user, f.disputes[0]); !errors.Is(err, admin.ErrForbidden) {
		t.Fatalf("member read private dispute detail: %v", err)
	}
}

func TestProductPaymentDisputeOperationLifecycleAndEvidence(t *testing.T) {
	f := newProductDisputeFixture(t)
	disputeID := f.disputes[0]
	before, err := f.service.GetProductPaymentDispute(t.Context(), f.finance, disputeID)
	if err != nil || len(before.Events) != 1 || before.Version != 1 {
		t.Fatalf("initial detail mismatch: %#v %v", before, err)
	}
	providerStatus, actionStatus := before.ProviderStatus, before.ActionStatus

	request := disputeCommand("request_evidence", "seller_support", 1)
	requested, err := f.service.OperateProductPaymentDispute(t.Context(), f.finance, disputeID, request, "dispute-request-1", "request-1")
	if err != nil || requested.Version != 2 || requested.ReviewStatus != "evidence_requested" || requested.EvidenceStatus != "requested" || len(requested.Operations) != 1 {
		t.Fatalf("evidence request mismatch: %#v %v", requested, err)
	}
	replayed, err := f.service.OperateProductPaymentDispute(t.Context(), f.finance, disputeID, request, "dispute-request-1", "request-replay")
	if err != nil || !replayed.Replayed || replayed.OperationID != requested.OperationID || len(replayed.Operations) != 1 {
		t.Fatalf("idempotent replay mismatch: %#v %v", replayed, err)
	}
	changed := request
	changed.Reason = "A different command must conflict."
	if _, err := f.service.OperateProductPaymentDispute(t.Context(), f.finance, disputeID, changed, "dispute-request-1", "request-changed"); !errors.Is(err, admin.ErrConflict) {
		t.Fatalf("changed idempotent command accepted: %v", err)
	}
	if _, err := f.service.OperateProductPaymentDispute(t.Context(), f.finance, disputeID, disputeCommand("route", "finance", 1), "dispute-stale-1", "request-stale"); !errors.Is(err, admin.ErrConflict) {
		t.Fatalf("stale version accepted: %v", err)
	}

	submission := disputeCommand("record_evidence_submission", "provider_review", 2)
	submission.EvidenceReference = "provider://stripe/file_123"
	submitted, err := f.service.OperateProductPaymentDispute(t.Context(), f.finance, disputeID, submission, "dispute-evidence-1", "request-2")
	if err != nil || submitted.Version != 3 || submitted.EvidenceStatus != "submitted" || len(submitted.EvidenceSubmissions) != 1 || len(submitted.Operations) != 2 {
		t.Fatalf("evidence submission mismatch: %#v %v", submitted, err)
	}
	if submitted.ProviderStatus != providerStatus || submitted.ActionStatus != actionStatus || submitted.SettlementStatus != before.SettlementStatus {
		t.Fatalf("operator command changed provider-owned financial state: before=%#v after=%#v", before, submitted.ProductPaymentDispute)
	}

	var operationCount, auditCount int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM product_payment_dispute_operations WHERE dispute_id=$1`, disputeID).Scan(&operationCount); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE resource_type='product_payment_dispute' AND resource_id=$1`, disputeID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if operationCount != 2 || auditCount != 2 {
		t.Fatalf("evidence counts mismatch: operations=%d audit=%d", operationCount, auditCount)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE product_payment_dispute_operations SET reason='mutated evidence' WHERE id=$1`, submitted.OperationID); err == nil {
		t.Fatal("operation evidence was mutable")
	}
	if _, err := f.pool.Exec(t.Context(), `DELETE FROM product_payment_dispute_evidence_submissions WHERE operation_id=$1`, submitted.OperationID); err == nil {
		t.Fatal("submitted evidence was deletable")
	}

	unrequestedID := f.disputes[1]
	premature := disputeCommand("record_evidence_submission", "provider_review", 1)
	premature.EvidenceReference = "provider://stripe/file_early"
	if _, err := f.service.OperateProductPaymentDispute(t.Context(), f.finance, unrequestedID, premature, "dispute-evidence-early", "request-early"); !errors.Is(err, admin.ErrConflict) {
		t.Fatalf("evidence accepted before request: %v", err)
	}
}

func TestProductPaymentDisputeAuditFailureRollsBack(t *testing.T) {
	f := newProductDisputeFixture(t)
	_, err := f.pool.Exec(t.Context(), `CREATE FUNCTION reject_product_dispute_audit() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN
	  IF NEW.action='admin.product_payment_dispute_operated' THEN
	    RAISE EXCEPTION 'forced audit failure' USING ERRCODE='55000';
	  END IF;
	  RETURN NEW;
	END;
	$$;
	CREATE TRIGGER reject_product_dispute_audit BEFORE INSERT ON audit_events
	FOR EACH ROW EXECUTE FUNCTION reject_product_dispute_audit()`)
	if err != nil {
		t.Fatal(err)
	}
	disputeID := f.disputes[0]
	if _, err := f.service.OperateProductPaymentDispute(t.Context(), f.finance, disputeID,
		disputeCommand("route", "finance", 1), "dispute-audit-failure", "request-audit"); err == nil {
		t.Fatal("operation survived an audit write failure")
	}
	var version int64
	var operations int
	if err := f.pool.QueryRow(t.Context(), `SELECT version FROM product_payment_disputes WHERE id=$1`, disputeID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM product_payment_dispute_operations WHERE dispute_id=$1`, disputeID).Scan(&operations); err != nil {
		t.Fatal(err)
	}
	if version != 1 || operations != 0 {
		t.Fatalf("failed audit left partial state: version=%d operations=%d", version, operations)
	}
}
