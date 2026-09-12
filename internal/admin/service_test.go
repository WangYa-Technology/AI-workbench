package admin_test

import (
	"context"
	"errors"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/billing"
	"github.com/hcai-chat/hcai-chat/internal/creation"
	"github.com/hcai-chat/hcai-chat/internal/payments"
	"github.com/hcai-chat/hcai-chat/internal/platform/database"
	"github.com/hcai-chat/hcai-chat/internal/risk"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestControlledOperationsAndStateHistory(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	adminID, userID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role,status) VALUES
		($1,$2,$3,'Operations Admin','admin','active'),
		($4,$5,$6,'Moderated Creator','creator','active')`,
		adminID, adminID.String()+"@test.local", "admin_"+adminID.String()[:8],
		userID, userID.String()+"@test.local", "user_"+userID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	service := admin.NewService(pool, true)
	if _, err := service.UpdateUser(ctx, adminID, adminID, admin.UserUpdate{Role: "admin", Status: "active"}, "request-self"); !errors.Is(err, admin.ErrSelfMutation) {
		t.Fatalf("expected self-mutation protection, got %v", err)
	}
	updated, err := service.UpdateUser(ctx, adminID, userID, admin.UserUpdate{Role: "creator", Status: "suspended"}, "request-user")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "suspended" {
		t.Fatalf("unexpected user state %#v", updated)
	}

	assetID, workID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code)
		VALUES($1,$2,'image','Moderation source','/media/test.jpg','image/jpeg','clean','demo','demo')`, assetID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO works(id,author_id,asset_id,title,summary,model_name,status,ai_disclosure,published_at)
		VALUES($1,$2,$3,'Moderated work','Test work','Local','published','Local test disclosure',now())`, workID, userID, assetID); err != nil {
		t.Fatal(err)
	}
	moderated, err := service.UpdateContent(ctx, adminID, workID, admin.ContentUpdate{Status: "hidden"}, "request-content")
	if err != nil || moderated.Status != "hidden" {
		t.Fatalf("content moderation failed: %#v %v", moderated, err)
	}

	provider, err := service.UpdateProvider(ctx, adminID, "local-image-v1", admin.ProviderUpdate{Enabled: false}, "request-provider")
	if err != nil || provider.AdminEnabled {
		t.Fatalf("provider disable failed: %#v %v", provider, err)
	}
	if _, err := service.UpdateProvider(ctx, adminID, "openai-image", admin.ProviderUpdate{Enabled: true}, "request-provider-external"); !errors.Is(err, admin.ErrProviderConfig) {
		t.Fatalf("expected fail-closed external provider, got %v", err)
	}

	adjusted, err := service.AdjustFinance(ctx, adminID, userID, admin.FinanceAdjustment{DeltaCents: 500, Currency: "USD"}, "request-finance")
	if err != nil {
		t.Fatal(err)
	}
	if adjusted.BalanceCents != 250500 {
		t.Fatalf("unexpected adjusted balance %d", adjusted.BalanceCents)
	}
	riskResourceID := uuid.New()
	riskTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	riskID, err := risk.RecordTx(ctx, riskTx, risk.SignalInput{
		SourceKey: "test-risk:" + riskResourceID.String(), ResourceType: "order", ResourceID: riskResourceID,
		SubjectUserID: userID, ActorUserID: &userID, SignalType: "transaction_refund", Severity: "medium", Score: 55,
		Summary: "Test transaction requires a controlled operations review.", Evidence: map[string]any{"paymentMode": "local_test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := riskTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	signals, err := service.ListRiskSignals(ctx, admin.RiskSignalFilter{})
	if err != nil || len(signals.Items) != 1 || signals.Items[0].ID != riskID || len(signals.Items[0].Events) != 1 {
		t.Fatalf("risk inventory failed: %#v %v", signals, err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO risk_signals(source_key,resource_type,resource_id,subject_user_id,signal_type,severity,score,summary,evidence)
		SELECT 'queue-pressure:'||value::text,'order',gen_random_uuid(),$1,'transaction_refund','critical',100,
		       'Higher-priority queue pressure signal.','{}'::jsonb
		FROM generate_series(1,201) value`, userID); err != nil {
		t.Fatal(err)
	}
	filteredSignals, err := service.ListRiskSignals(ctx, admin.RiskSignalFilter{ResourceType: "order", ResourceID: &riskResourceID})
	if err != nil || len(filteredSignals.Items) != 1 || filteredSignals.Items[0].ID != riskID {
		t.Fatalf("focused risk inventory was truncated by queue pressure: %#v %v", filteredSignals, err)
	}
	if _, err := service.ListRiskSignals(ctx, admin.RiskSignalFilter{ResourceType: "order"}); !errors.Is(err, admin.ErrInvalidRiskFilter) {
		t.Fatalf("unpaired risk filter was accepted: %v", err)
	}
	if _, err := service.ReviewRiskSignal(ctx, adminID, riskID, admin.RiskReview{Decision: "monitor", ExpectedVersion: 2}, "risk-stale"); !errors.Is(err, admin.ErrConflict) {
		t.Fatalf("stale risk review did not conflict: %v", err)
	}
	monitored, err := service.ReviewRiskSignal(ctx, adminID, riskID, admin.RiskReview{Decision: "monitor", ExpectedVersion: 1}, "risk-monitor")
	if err != nil || monitored.Status != "reviewing" || monitored.Version != 2 || len(monitored.Events) != 2 {
		t.Fatalf("risk monitoring failed: %#v %v", monitored, err)
	}
	escalated, err := service.ReviewRiskSignal(ctx, adminID, riskID, admin.RiskReview{Decision: "escalated", ExpectedVersion: 2}, "risk-escalate")
	if err != nil || escalated.Status != "resolved" || escalated.Version != 3 || escalated.ResolvedAt == nil {
		t.Fatalf("risk escalation failed: %#v %v", escalated, err)
	}
	if _, err := service.ReviewRiskSignal(ctx, adminID, riskID, admin.RiskReview{Decision: "no_action", ExpectedVersion: 3}, "risk-overwrite"); !errors.Is(err, admin.ErrConflict) {
		t.Fatalf("terminal risk decision was mutable: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE risk_events SET reason='tampered evidence' WHERE signal_id=$1`, riskID); err == nil {
		t.Fatal("risk evidence was not append-only")
	}
	overview, err := service.Overview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if overview.Users.Total != 2 || overview.Works.ByStatus["hidden"] != 1 {
		t.Fatalf("unexpected operations overview %#v", overview)
	}
}

func TestAdminTaskOperationsResolveDisputesAtomically(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	adminID, clientID, creatorID := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role,status) VALUES
		($1,$2,$3,'Task Operator','admin','active'),
		($4,$5,$6,'Task Client','publisher','active'),
		($7,$8,$9,'Task Creator','creator','active')`,
		adminID, adminID.String()+"@test.local", "task_admin_"+adminID.String()[:8],
		clientID, clientID.String()+"@test.local", "task_client_"+clientID.String()[:8],
		creatorID, creatorID.String()+"@test.local", "task_creator_"+creatorID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	createDispute := func(title string, amount int) (uuid.UUID, uuid.UUID) {
		t.Helper()
		taskID, assetID, disputeID := uuid.New(), uuid.New(), uuid.New()
		if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code) VALUES($1,$2,'image',$3,'/media/task-test.jpg','image/jpeg','clean','delivery','personal')`, assetID, creatorID, title+" delivery"); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO demands(id,client_id,title,brief,deliverable_type,budget_cents,currency,deadline,status,assignee_id,summary) VALUES($1,$2,$3,'A complete disputed task operations test brief.','image',$4,'USD',now()+interval '7 days','disputed',$5,'Operations test task.')`, taskID, clientID, title, amount, creatorID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO proposals(demand_id,creator_id,approach,amount_cents,status) VALUES($1,$2,'Verified operations approach',$3,'accepted')`, taskID, creatorID, amount); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO deliveries(demand_id,creator_id,asset_id,note,status,version) VALUES($1,$2,$3,'Disputed delivery evidence','disputed',1)`, taskID, creatorID, assetID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO task_disputes(id,demand_id,opened_by,reason,status,idempotency_key) VALUES($1,$2,$3,'Acceptance wording requires an operations decision.','open',$4)`, disputeID, taskID, clientID, "dispute-"+taskID.String()); err != nil {
			t.Fatal(err)
		}
		return taskID, disputeID
	}

	releaseTaskID, releaseDisputeID := createDispute("Release creator task", 32_000)
	cancelTaskID, cancelDisputeID := createDispute("Cancel without settlement task", 21_000)
	releasePaymentID := uuid.New()
	releasePaymentToken := releasePaymentID.String()[:8]
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_intents(id,provider,purpose,payer_id,payee_id,resource_id,amount_cents,currency,status,live_mode,idempotency_key,provider_payment_id,provider_charge_id,paid_at)
		VALUES($1,'stripe','task',$2,$3,$4,32000,'USD','paid',false,$5,$6,$7,now())`,
		releasePaymentID, clientID, creatorID, releaseTaskID, "task-release-"+releasePaymentToken,
		"pi_release_"+releasePaymentToken, "ch_release_"+releasePaymentToken); err != nil {
		t.Fatal(err)
	}
	service := admin.NewService(pool, true)
	page, err := service.ListTaskOperations(ctx, admin.TaskOperationListInput{})
	if err != nil || len(page.Items) != 2 || page.Items[0].DisputeVersion == nil {
		t.Fatalf("task operations inventory mismatch: %#v %v", page, err)
	}
	if _, err := service.ResolveTaskDispute(ctx, adminID, releaseTaskID, admin.TaskDisputeResolution{
		Decision: "release_creator", ExpectedVersion: 2}, "task-stale"); !errors.Is(err, admin.ErrConflict) {
		t.Fatalf("stale task decision did not conflict: %v", err)
	}
	released, err := service.ResolveTaskDispute(ctx, adminID, releaseTaskID, admin.TaskDisputeResolution{
		Decision: "release_creator", ExpectedVersion: 1}, "task-release")
	if err != nil || released.Status != "accepted" || released.DisputeStatus == nil || *released.DisputeStatus != "resolved_creator" || released.SettlementID == nil || released.DisputeVersion == nil || *released.DisputeVersion != 2 {
		t.Fatalf("creator release mismatch: %#v %v", released, err)
	}
	if _, err := service.ResolveTaskDispute(ctx, adminID, releaseTaskID, admin.TaskDisputeResolution{
		Decision: "release_creator", ExpectedVersion: 2}, "task-replay"); !errors.Is(err, admin.ErrConflict) {
		t.Fatalf("terminal task dispute remained actionable: %v", err)
	}
	cancelled, err := service.ResolveTaskDispute(ctx, adminID, cancelTaskID, admin.TaskDisputeResolution{
		Decision: "cancel_without_settlement", ExpectedVersion: 1}, "task-cancel")
	if err != nil || cancelled.Status != "cancelled" || cancelled.DisputeStatus == nil || *cancelled.DisputeStatus != "resolved_client" || cancelled.SettlementID != nil {
		t.Fatalf("client resolution mismatch: %#v %v", cancelled, err)
	}
	var settlements, legacyEntries, notificationCount, paymentEvents, transferJobs int
	var paymentStatus, settlementMode string
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM task_settlements WHERE demand_id=ANY($1)`, []uuid.UUID{releaseTaskID, cancelTaskID}).Scan(&settlements); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ledger_entries WHERE operation_id=(SELECT id FROM task_settlements WHERE demand_id=$1)`, releaseTaskID).Scan(&legacyEntries); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE source_key LIKE 'admin-task-resolution:%'`).Scan(&notificationCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT pi.status,ts.mode FROM payment_intents pi JOIN task_settlements ts ON ts.demand_id=pi.resource_id WHERE pi.id=$1`, releasePaymentID).Scan(&paymentStatus, &settlementMode); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_intent_events WHERE payment_id=$1`, releasePaymentID).Scan(&paymentEvents); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind=$1`, payments.TaskTransferJobKind).Scan(&transferJobs); err != nil {
		t.Fatal(err)
	}
	if settlements != 1 || legacyEntries != 0 || notificationCount != 4 || paymentStatus != "transfer_pending" || settlementMode != "provider_pending" || paymentEvents != 1 || transferJobs != 1 {
		t.Fatalf("task operations evidence mismatch: settlements=%d ledger=%d notifications=%d payment=%s settlementMode=%s paymentEvents=%d transferJobs=%d", settlements, legacyEntries, notificationCount, paymentStatus, settlementMode, paymentEvents, transferJobs)
	}
	if _, err := pool.Exec(ctx, `UPDATE task_events SET note='tampered task evidence' WHERE demand_id=$1`, releaseTaskID); err == nil {
		t.Fatal("task event evidence was mutable")
	}
	var releaseStatus, cancelStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM task_disputes WHERE id=$1`, releaseDisputeID).Scan(&releaseStatus); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM task_disputes WHERE id=$1`, cancelDisputeID).Scan(&cancelStatus); err != nil {
		t.Fatal(err)
	}
	if releaseStatus != "resolved_creator" || cancelStatus != "resolved_client" {
		t.Fatalf("unexpected dispute states %s %s", releaseStatus, cancelStatus)
	}
}

func TestAdminProviderTaskDisputesQueueTransferOrRefund(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	adminID, clientID, creatorID := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role,status) VALUES
		($1,$2,$3,'Provider Task Operator','admin','active'),
		($4,$5,$6,'Provider Task Client','publisher','active'),
		($7,$8,$9,'Provider Task Creator','creator','active')`,
		adminID, adminID.String()+"@test.local", "provider_task_admin_"+adminID.String()[:8],
		clientID, clientID.String()+"@test.local", "provider_task_client_"+clientID.String()[:8],
		creatorID, creatorID.String()+"@test.local", "provider_task_creator_"+creatorID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	createProviderDispute := func(title string, amount int) (uuid.UUID, uuid.UUID, uuid.UUID) {
		t.Helper()
		taskID, assetID, disputeID, paymentID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
		if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code) VALUES($1,$2,'image',$3,'/media/provider-task.jpg','image/jpeg','clean','delivery','personal')`, assetID, creatorID, title+" delivery"); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO demands(id,client_id,title,brief,deliverable_type,budget_cents,currency,deadline,status,assignee_id,summary) VALUES($1,$2,$3,'A complete Provider-funded disputed task brief.','image',$4,'USD',now()+interval '7 days','disputed',$5,'Provider dispute evidence.')`, taskID, clientID, title, amount, creatorID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO proposals(demand_id,creator_id,approach,amount_cents,status) VALUES($1,$2,'Provider-funded operations approach',$3,'accepted')`, taskID, creatorID, amount); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO deliveries(demand_id,creator_id,asset_id,note,status,version) VALUES($1,$2,$3,'Provider disputed delivery','disputed',1)`, taskID, creatorID, assetID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO task_disputes(id,demand_id,opened_by,reason,status,idempotency_key) VALUES($1,$2,$3,'Provider settlement requires an operations decision.','open',$4)`, disputeID, taskID, clientID, "provider-dispute-"+taskID.String()); err != nil {
			t.Fatal(err)
		}
		token := paymentID.String()[:8]
		if _, err := pool.Exec(ctx, `
			INSERT INTO payment_intents(id,provider,purpose,payer_id,payee_id,resource_id,amount_cents,currency,status,live_mode,idempotency_key,provider_payment_id,provider_charge_id,paid_at)
			VALUES($1,'stripe','task',$2,$3,$4,$5,'USD','paid',false,$6,$7,$8,now())`,
			paymentID, clientID, creatorID, taskID, amount, "provider-funding-"+token, "pi_provider_"+token, "ch_provider_"+token); err != nil {
			t.Fatal(err)
		}
		return taskID, disputeID, paymentID
	}

	releaseTaskID, _, releasePaymentID := createProviderDispute("Provider release task", 42_000)
	cancelTaskID, _, cancelPaymentID := createProviderDispute("Provider refund task", 31_000)
	service := admin.NewService(pool, true)
	released, err := service.ResolveTaskDispute(ctx, adminID, releaseTaskID, admin.TaskDisputeResolution{
		Decision: "release_creator", ExpectedVersion: 1}, "provider-task-release")
	if err != nil || released.Status != "accepted" || released.SettlementID == nil {
		t.Fatalf("Provider creator release mismatch: operation=%#v err=%v", released, err)
	}
	cancelled, err := service.ResolveTaskDispute(ctx, adminID, cancelTaskID, admin.TaskDisputeResolution{
		Decision: "cancel_without_settlement", ExpectedVersion: 1}, "provider-task-cancel")
	if err != nil || cancelled.Status != "cancelled" || cancelled.SettlementID != nil {
		t.Fatalf("Provider commissioner resolution mismatch: operation=%#v err=%v", cancelled, err)
	}
	var releaseStatus, settlementMode, cancelStatus string
	var refundOperationID *uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT pi.status,ts.mode FROM payment_intents pi JOIN task_settlements ts ON ts.demand_id=pi.resource_id WHERE pi.id=$1`, releasePaymentID).Scan(&releaseStatus, &settlementMode); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT status,refund_operation_id FROM payment_intents WHERE id=$1`, cancelPaymentID).Scan(&cancelStatus, &refundOperationID); err != nil {
		t.Fatal(err)
	}
	var transferJobs, refundJobs, localEntries int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind=$1`, payments.TaskTransferJobKind).Scan(&transferJobs); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind=$1`, payments.TaskRefundJobKind).Scan(&refundJobs); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM billing_entries WHERE user_id IN ($1,$2)) +
		       (SELECT count(*) FROM ledger_entries WHERE account_id IN ($1,$2))`, clientID, creatorID).Scan(&localEntries); err != nil {
		t.Fatal(err)
	}
	if releaseStatus != "transfer_pending" || settlementMode != "provider_pending" || cancelStatus != "refund_pending" || refundOperationID == nil || transferJobs != 1 || refundJobs != 1 || localEntries != 0 {
		t.Fatalf("Provider dispute evidence mismatch: release=%s settlement=%s cancel=%s operation=%v transferJobs=%d refundJobs=%d localEntries=%d", releaseStatus, settlementMode, cancelStatus, refundOperationID, transferJobs, refundJobs, localEntries)
	}
}

func TestRankingPolicyCreatesImmutableRevisions(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	adminID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role,status)
		VALUES($1,$2,$3,'Ranking Admin','admin','active')`, adminID, adminID.String()+"@test.local", "rank_"+adminID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	service := admin.NewService(pool, true)
	initial, err := service.GetRankingPolicy(ctx)
	if err != nil || initial.Current.Version != 1 || len(initial.History) != 1 {
		t.Fatalf("initial ranking policy mismatch: %#v %v", initial, err)
	}
	input := admin.RankingUpdate{
		Name: initial.Current.Name, TitleExactWeight: initial.Current.TitleExactWeight,
		TitlePrefixWeight: initial.Current.TitlePrefixWeight, TitleContainsWeight: initial.Current.TitleContainsWeight,
		CreatorExactWeight: initial.Current.CreatorExactWeight, CreatorMatchWeight: initial.Current.CreatorMatchWeight,
		BodyMatchWeight: initial.Current.BodyMatchWeight, SecondaryMatchWeight: initial.Current.SecondaryMatchWeight,
		RecencyWeight: initial.Current.RecencyWeight, CreatorActivityWeight: initial.Current.CreatorActivityWeight,
		WorkTypeBoost: initial.Current.WorkTypeBoost, CreatorTypeBoost: initial.Current.CreatorTypeBoost,
		ProductTypeBoost: initial.Current.ProductTypeBoost + 7, DemandTypeBoost: initial.Current.DemandTypeBoost,
		ExpectedVersion: 2}
	if _, err := service.UpdateRankingPolicy(ctx, adminID, input, "ranking-stale"); !errors.Is(err, admin.ErrConflict) {
		t.Fatalf("stale ranking update did not conflict: %v", err)
	}
	input.ExpectedVersion = 1
	updated, err := service.UpdateRankingPolicy(ctx, adminID, input, "ranking-update")
	if err != nil || updated.Current.Version != 2 || updated.Current.ParentRevisionID == nil ||
		updated.Current.ProductTypeBoost != initial.Current.ProductTypeBoost+7 || len(updated.History) != 2 {
		t.Fatalf("ranking revision failed: %#v %v", updated, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE discovery_ranking_revisions SET reason='tampered revision' WHERE id=$1`, initial.Current.ID); err == nil {
		t.Fatal("ranking revision was not immutable")
	}
}

func TestDiscoveryCandidateEvaluationRolloutAndIndexEvidence(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	adminID, creatorID, assetID, productID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role,status) VALUES
		($1,$2,$3,'Discovery Operator','admin','active'),
		($4,$5,$6,'Indexed Creator','creator','active')`,
		adminID, adminID.String()+"@test.local", "operator_"+adminID.String()[:8],
		creatorID, creatorID.String()+"@test.local", "indexed_"+creatorID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code)
		VALUES($1,$2,'image','Indexed product source','/media/indexed.jpg','image/jpeg','clean','demo','hcai-commercial-standard-v1')`, assetID, creatorID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status,ai_disclosure,included_files,compatibility)
		VALUES($1,$2,$3,'Exact indexed workflow','Offline evaluation fixture','workflow',900,'USD','hcai-commercial-standard-v1','active','Local Test fixture.','[]','HCAI')`, productID, creatorID, assetID); err != nil {
		t.Fatal(err)
	}
	service := admin.NewService(pool, true)
	initial, err := service.GetRankingPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	candidateInput := admin.RankingUpdate{
		Name: "Candidate product calibration", TitleExactWeight: initial.Current.TitleExactWeight,
		TitlePrefixWeight: initial.Current.TitlePrefixWeight, TitleContainsWeight: initial.Current.TitleContainsWeight,
		CreatorExactWeight: initial.Current.CreatorExactWeight, CreatorMatchWeight: initial.Current.CreatorMatchWeight,
		BodyMatchWeight: initial.Current.BodyMatchWeight, SecondaryMatchWeight: initial.Current.SecondaryMatchWeight,
		RecencyWeight: initial.Current.RecencyWeight, CreatorActivityWeight: initial.Current.CreatorActivityWeight,
		WorkTypeBoost: initial.Current.WorkTypeBoost, CreatorTypeBoost: initial.Current.CreatorTypeBoost,
		ProductTypeBoost: initial.Current.ProductTypeBoost + 3, DemandTypeBoost: initial.Current.DemandTypeBoost,
		ExpectedVersion: initial.Current.Version}
	policy, err := service.CreateRankingCandidate(ctx, adminID, candidateInput, "candidate-create")
	if err != nil || policy.Candidate == nil || policy.Current.Version != 1 || policy.Candidate.Version != 2 || policy.Rollout.Version != 2 {
		t.Fatalf("candidate creation mismatch: %#v %v", policy, err)
	}
	if _, err := service.UpdateRankingRollout(ctx, adminID, admin.RankingRolloutUpdate{Percent: 25, ExpectedVersion: policy.Rollout.Version}, "rollout-without-eval"); !errors.Is(err, admin.ErrConflict) {
		t.Fatalf("unevaluated rollout did not fail closed: %v", err)
	}
	evaluation, err := service.RunRankingEvaluation(ctx, adminID)
	if err != nil || evaluation.Status != "passed" || evaluation.CaseCount < 1 || evaluation.CandidateMRR < evaluation.BaselineMRR {
		t.Fatalf("offline evaluation mismatch: %#v %v", evaluation, err)
	}
	policy, err = service.UpdateRankingRollout(ctx, adminID, admin.RankingRolloutUpdate{Percent: 25, ExpectedVersion: policy.Rollout.Version}, "rollout-25")
	if err != nil || policy.Rollout.Percent != 25 || policy.Rollout.Version != 3 || policy.Current.Version != 1 {
		t.Fatalf("staged rollout mismatch: %#v %v", policy, err)
	}
	indexRun, err := service.RunDiscoveryIndexAnalyze(ctx, adminID)
	if err != nil || indexRun.Status != "succeeded" || indexRun.DocumentCounts["products"] != 1 || len(indexRun.IndexSizes) != 5 {
		t.Fatalf("index evidence mismatch: %#v %v", indexRun, err)
	}
	operations, err := service.GetDiscoveryOperations(ctx)
	if err != nil || len(operations.IndexRuns) != 1 || len(operations.Evaluations) != 1 {
		t.Fatalf("discovery operations mismatch: %#v %v", operations, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE discovery_ranking_evaluations SET status='failed' WHERE id=$1`, evaluation.ID); err == nil {
		t.Fatal("ranking evaluation evidence was mutable")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM discovery_index_runs WHERE id=$1`, indexRun.ID); err == nil {
		t.Fatal("index run evidence was mutable")
	}
	policy, err = service.UpdateRankingRollout(ctx, adminID, admin.RankingRolloutUpdate{Percent: 100, ExpectedVersion: policy.Rollout.Version}, "rollout-promote")
	if err != nil || policy.Current.Version != 2 || policy.Candidate != nil || policy.Rollout.Percent != 0 || policy.Rollout.Version != 4 {
		t.Fatalf("candidate promotion mismatch: %#v %v", policy, err)
	}
}

func TestAdminGenerationCancellationReleasesCredits(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	adminID, ownerID, generationID := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role,status) VALUES
		($1,$2,$3,'Operations Admin','admin','active'),($4,$5,$6,'Generation Owner','creator','active')`,
		adminID, adminID.String()+"@test.local", "admin_"+adminID.String()[:8],
		ownerID, ownerID.String()+"@test.local", "owner_"+ownerID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := billing.ReserveTx(ctx, tx, ownerID, generationID, 5, "USD"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO generations(id,owner_id,mode,provider,model_name,prompt,status,estimated_cost_cents)
		VALUES($1,$2,'image','local_test','hcai-local-image-v1','Cancelable admin generation','queued',5)`, generationID, ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO jobs(kind,payload) VALUES($1,jsonb_build_object('generationId',$2::text))`, creation.JobKind, generationID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	item, err := admin.NewService(pool, true).CancelGeneration(ctx, adminID, generationID, "request-admin-cancel")
	if err != nil {
		t.Fatal(err)
	}
	if item.Status != "cancelled" {
		t.Fatalf("unexpected generation status %s", item.Status)
	}
	var balance, reserved int64
	if err := pool.QueryRow(ctx, `SELECT balance_cents,reserved_cents FROM billing_accounts WHERE user_id=$1`, ownerID).Scan(&balance, &reserved); err != nil {
		t.Fatal(err)
	}
	if balance != 250000 || reserved != 0 {
		t.Fatalf("admin cancellation did not release credits: balance=%d reserved=%d", balance, reserved)
	}
}

func TestAdminPaymentOperationsRecovery(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	adminID, clientID, creatorID := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role,status) VALUES
		($1,$2,$3,'Payment Admin','admin','active'),
		($4,$5,$6,'Payment Client','publisher','active'),
		($7,$8,$9,'Payment Creator','creator','active')`,
		adminID, adminID.String()+"@test.local", "payment_admin_"+adminID.String()[:8],
		clientID, clientID.String()+"@test.local", "payment_client_"+clientID.String()[:8],
		creatorID, creatorID.String()+"@test.local", "payment_creator_"+creatorID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	transferTaskID, refundTaskID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO demands(id,client_id,title,brief,deliverable_type,budget_cents,currency,deadline,status,assignee_id,summary) VALUES
		($1,$2,'Transfer recovery task','A funded task requiring payout recovery.','image',12000,'USD',now()+interval '7 days','accepted',$3,'Controlled payout recovery.'),
		($4,$2,'Refund recovery task','A cancelled funded task requiring refund recovery.','image',13000,'USD',now()+interval '7 days','cancelled',$3,'Controlled refund recovery.')`,
		transferTaskID, clientID, creatorID, refundTaskID); err != nil {
		t.Fatal(err)
	}
	transferID, refundID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_intents(id,provider,purpose,payer_id,payee_id,resource_id,amount_cents,currency,status,live_mode,idempotency_key,provider_payment_id,provider_charge_id)
		VALUES($1,'stripe','task',$2,$3,$4,12000,'USD','transfer_pending',false,'transfer-recovery',$5,$6),
		      ($7,'stripe','task',$2,$3,$8,13000,'USD','refund_failed',false,'refund-recovery',$9,NULL)`,
		transferID, clientID, creatorID, transferTaskID, "pi_transfer_recovery", "ch_transfer_recovery",
		refundID, refundTaskID, "pi_refund_recovery"); err != nil {
		t.Fatal(err)
	}
	service := admin.NewService(pool, true)
	page, err := service.ListPaymentOperations(ctx, admin.PaymentOperationListInput{Attention: "needs_attention"})
	if err != nil || len(page.Items) != 2 || page.Items[0].AttentionCode == "none" {
		t.Fatalf("payment operations queue mismatch: %#v %v", page, err)
	}
	createdDestination, err := service.UpdatePaymentDestination(ctx, adminID, creatorID, admin.PaymentDestinationUpdate{
		DestinationID: "acct_payment_recovery", Enabled: true, ExpectedVersion: 0}, "destination-create")
	if err != nil || createdDestination.Status != "verified" || createdDestination.Version != 1 {
		t.Fatalf("destination creation mismatch: %#v %v", createdDestination, err)
	}
	disabledDestination, err := service.UpdatePaymentDestination(ctx, adminID, creatorID, admin.PaymentDestinationUpdate{
		DestinationID: "acct_payment_recovery", Enabled: false, ExpectedVersion: 1}, "destination-disable")
	if err != nil || disabledDestination.Version != 2 || disabledDestination.Status != "disabled" {
		t.Fatalf("destination disable mismatch: %#v %v", disabledDestination, err)
	}
	updatedDestination, err := service.UpdatePaymentDestination(ctx, adminID, creatorID, admin.PaymentDestinationUpdate{
		DestinationID: "acct_payment_recovery", Enabled: true, ExpectedVersion: 2}, "destination-enable")
	if err != nil || updatedDestination.Version != 3 || !updatedDestination.ChargesEnabled || !updatedDestination.PayoutsEnabled {
		t.Fatalf("destination re-enable mismatch: %#v %v", updatedDestination, err)
	}
	transfer, err := service.RecoverPayment(ctx, adminID, transferID, admin.PaymentRecovery{
		Action: "retry_transfer", ExpectedVersion: 1}, "transfer-recovery")
	if err != nil || transfer.Status != "transfer_pending" || transfer.Job == nil || transfer.Job.Kind != payments.TaskTransferJobKind || transfer.Job.Status != "queued" || transfer.Version != 2 {
		t.Fatalf("transfer recovery mismatch: %#v %v", transfer, err)
	}
	if _, err := service.RecoverPayment(ctx, adminID, transferID, admin.PaymentRecovery{
		Action: "retry_transfer", ExpectedVersion: 2}, "transfer-duplicate"); !errors.Is(err, admin.ErrConflict) {
		t.Fatalf("duplicate transfer recovery was accepted: %v", err)
	}
	refunded, err := service.RecoverPayment(ctx, adminID, refundID, admin.PaymentRecovery{
		Action: "retry_refund", ExpectedVersion: 1}, "refund-recovery")
	if err != nil || refunded.Status != "refund_pending" || refunded.Job == nil || refunded.Job.Kind != payments.TaskRefundJobKind || refunded.Version != 2 {
		t.Fatalf("refund recovery mismatch: %#v %v", refunded, err)
	}
	eventID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_provider_events(id,provider,provider_event_id,event_type,api_version,live_mode,occurred_at,payload_sha256,object_id,object_type,payment_id,purpose)
		VALUES($1,'stripe','evt_payment_replay','payment_intent.succeeded','2026-02-25.clover',false,now(),repeat('a',64),'pi_payment_replay','payment_intent',$2,'task')`, eventID, transferID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO payment_provider_event_processing(event_id,status) VALUES($1,'received')`, eventID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO jobs(kind,payload,status,attempts,max_attempts,last_error,last_error_code)
		VALUES('payment.process_event',jsonb_build_object('eventId',$1::text),'failed',8,8,'payment_response_invalid','payment_response_invalid')`, eventID); err != nil {
		t.Fatal(err)
	}
	replayed, err := service.ReplayPaymentEvent(ctx, adminID, eventID, admin.PaymentEventReplay{
		ExpectedVersion: 1}, "event-replay")
	if err != nil || replayed.ProviderEvent == nil || replayed.ProviderEvent.ReplayCount != 1 || replayed.ProviderEvent.Job == nil || replayed.ProviderEvent.Job.Status != "queued" {
		t.Fatalf("payment event replay mismatch: %#v %v", replayed, err)
	}
}

func testPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	ctx := context.Background()
	baseURL := os.Getenv("TEST_DATABASE_URL")
	if baseURL == "" {
		baseURL = "postgres://hcai:hcai@localhost:5432/hcai?sslmode=disable"
	}
	root, err := pgxpool.New(ctx, baseURL)
	if err != nil {
		testutil.DatabaseUnavailable(t, err)
	}
	if err := root.Ping(ctx); err != nil {
		root.Close()
		testutil.DatabaseUnavailable(t, err)
	}
	schema := "test_admin_" + uuid.NewString()[:8]
	if _, err := root.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(baseURL)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	pool, err := database.Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = root.Exec(cleanupCtx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		root.Close()
	}
}
