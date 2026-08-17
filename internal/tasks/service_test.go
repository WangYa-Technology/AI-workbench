package tasks_test

import (
	"context"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/database"
	"github.com/hcai-chat/hcai-chat/internal/tasks"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestTaskProposalRevisionDeliveryAndSettlement(t *testing.T) {
	pool, cleanup := taskTestPool(t)
	defer cleanup()
	ctx := context.Background()
	clientID, creatorID, outsiderID := uuid.New(), uuid.New(), uuid.New()
	assetID := uuid.New()
	seedTaskUsers(t, pool, clientID, creatorID, outsiderID, assetID)
	service := tasks.NewService(pool)

	input := validTaskInput()
	created, err := service.Create(ctx, clientID, input, "create-command-001")
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := service.Create(ctx, clientID, input, "create-command-001")
	if err != nil {
		t.Fatal(err)
	}
	if replayed.ID != created.ID {
		t.Fatalf("create replay changed task: %s != %s", replayed.ID, created.ID)
	}

	proposalInput := tasks.ProposeInput{Approach: "I will build a controlled image system with two review checkpoints.", Deliverables: "Master image and reproducible workflow", AmountCents: 70000, TimelineDays: 6}
	proposed, err := service.Propose(ctx, creatorID, created.ID, proposalInput, "proposal-command-001")
	if err != nil {
		t.Fatal(err)
	}
	if len(proposed.Proposals) != 1 {
		t.Fatalf("creator should see one proposal, got %d", len(proposed.Proposals))
	}
	if _, err := service.Propose(ctx, creatorID, created.ID, proposalInput, "proposal-command-001"); err != nil {
		t.Fatalf("proposal replay failed: %v", err)
	}

	clientView, err := service.Get(ctx, clientID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	proposalID := clientView.Proposals[0].ID
	if _, err := service.AcceptProposal(ctx, outsiderID, created.ID, proposalID, "accept-command-bad"); !errors.Is(err, tasks.ErrForbidden) {
		t.Fatalf("expected forbidden proposal acceptance, got %v", err)
	}
	assigned, err := service.AcceptProposal(ctx, clientID, created.ID, proposalID, "accept-command-001")
	if err != nil {
		t.Fatal(err)
	}
	if assigned.Status != "assigned" || assigned.Assignee == nil || assigned.Assignee.ID != creatorID {
		t.Fatalf("unexpected assignment: %#v", assigned.Summary)
	}

	delivered, err := service.Deliver(ctx, creatorID, created.ID, tasks.DeliverInput{AssetID: assetID, Note: "First review-ready delivery."}, "delivery-command-001")
	if err != nil {
		t.Fatal(err)
	}
	if delivered.Status != "submitted" || len(delivered.Deliveries) != 1 || delivered.Deliveries[0].Version != 1 {
		t.Fatalf("unexpected first delivery: %#v", delivered.Deliveries)
	}
	revision, err := service.Review(ctx, clientID, created.ID, tasks.ReviewInput{Decision: "request_revision", Note: "Increase subject separation and remove the visible edge artifact."}, "review-command-001")
	if err != nil {
		t.Fatal(err)
	}
	if revision.Status != "revision" {
		t.Fatalf("expected revision, got %s", revision.Status)
	}

	second, err := service.Deliver(ctx, creatorID, created.ID, tasks.DeliverInput{AssetID: assetID, Note: "Revised delivery with artifact removed."}, "delivery-command-002")
	if err != nil {
		t.Fatal(err)
	}
	if second.Deliveries[0].Version != 2 {
		t.Fatalf("expected delivery version 2, got %d", second.Deliveries[0].Version)
	}
	accepted, err := service.Review(ctx, clientID, created.ID, tasks.ReviewInput{Decision: "accept", Note: "All published acceptance rules are satisfied."}, "review-command-002")
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Status != "accepted" || accepted.Settlement == nil || accepted.Settlement.Mode != "local_test" {
		t.Fatalf("task not settled in local test mode: %#v", accepted.Settlement)
	}
	replayedAccept, err := service.Review(ctx, clientID, created.ID, tasks.ReviewInput{Decision: "accept", Note: "All published acceptance rules are satisfied."}, "review-command-002")
	if err != nil || replayedAccept.Settlement == nil || replayedAccept.Settlement.ID != accepted.Settlement.ID {
		t.Fatalf("accept replay was not idempotent: %v", err)
	}

	var debit, credit, entryCount int
	if err := pool.QueryRow(ctx, `SELECT COALESCE(SUM(amount_cents) FILTER (WHERE direction='debit'),0),COALESCE(SUM(amount_cents) FILTER (WHERE direction='credit'),0),COUNT(*) FROM ledger_entries WHERE operation_id=$1`, accepted.Settlement.ID).Scan(&debit, &credit, &entryCount); err != nil {
		t.Fatal(err)
	}
	if debit != credit || debit != proposalInput.AmountCents || entryCount != 2 {
		t.Fatalf("unbalanced settlement: debit=%d credit=%d entries=%d", debit, credit, entryCount)
	}
}

func TestTaskDirectClaimAndDispute(t *testing.T) {
	pool, cleanup := taskTestPool(t)
	defer cleanup()
	ctx := context.Background()
	clientID, creatorID, outsiderID, assetID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	seedTaskUsers(t, pool, clientID, creatorID, outsiderID, assetID)
	service := tasks.NewService(pool)
	input := validTaskInput()
	input.AllowDirectAccept = true
	created, err := service.Create(ctx, clientID, input, "direct-create-001")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Claim(ctx, clientID, created.ID, "direct-claim-bad"); !errors.Is(err, tasks.ErrForbidden) {
		t.Fatalf("client claimed own task: %v", err)
	}
	if _, err := service.Claim(ctx, creatorID, created.ID, "direct-claim-001"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Deliver(ctx, creatorID, created.ID, tasks.DeliverInput{AssetID: assetID, Note: "Delivery ready for dispute path."}, "direct-delivery-001"); err != nil {
		t.Fatal(err)
	}
	disputed, err := service.OpenDispute(ctx, creatorID, created.ID, "The acceptance interpretation conflicts with the published rule wording.", "direct-dispute-001")
	if err != nil {
		t.Fatal(err)
	}
	if disputed.Status != "disputed" {
		t.Fatalf("expected disputed status, got %s", disputed.Status)
	}
	if _, err := service.OpenDispute(ctx, creatorID, created.ID, "The acceptance interpretation conflicts with the published rule wording.", "direct-dispute-001"); err != nil {
		t.Fatalf("dispute replay failed: %v", err)
	}
	var riskSignals, riskEvents int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM risk_signals WHERE source_key=$1`, "task_dispute:"+created.ID.String()).Scan(&riskSignals); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM risk_events e JOIN risk_signals s ON s.id=e.signal_id WHERE s.source_key=$1`, "task_dispute:"+created.ID.String()).Scan(&riskEvents); err != nil {
		t.Fatal(err)
	}
	if riskSignals != 1 || riskEvents != 1 {
		t.Fatalf("task dispute risk signal was not idempotent: signals=%d events=%d", riskSignals, riskEvents)
	}
	if _, err := service.OpenDispute(ctx, outsiderID, created.ID, "An unrelated account must not open this dispute at any time.", "direct-dispute-bad"); !errors.Is(err, tasks.ErrForbidden) {
		t.Fatalf("outsider dispute should be forbidden: %v", err)
	}
}

func TestTaskCancellationIsOwnerOnlyIdempotentAndAudited(t *testing.T) {
	pool, cleanup := taskTestPool(t)
	defer cleanup()
	ctx := context.Background()
	clientID, creatorID, outsiderID, assetID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	seedTaskUsers(t, pool, clientID, creatorID, outsiderID, assetID)
	service := tasks.NewService(pool)

	created, err := service.Create(ctx, clientID, validTaskInput(), "cancel-create-001")
	if err != nil {
		t.Fatal(err)
	}
	proposalInput := tasks.ProposeInput{
		Approach:     "I will build the image system against the published acceptance rules.",
		Deliverables: "Master image and vertical crop",
		AmountCents:  76000,
		TimelineDays: 5,
	}
	if _, err = service.Propose(ctx, creatorID, created.ID, proposalInput, "cancel-proposal-001"); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Cancel(ctx, outsiderID, created.ID, "The unrelated account cannot cancel this task.", "cancel-command-bad"); !errors.Is(err, tasks.ErrForbidden) {
		t.Fatalf("expected outsider cancellation to be forbidden, got %v", err)
	}

	reason := "The launch schedule changed before a creator was selected."
	cancelled, err := service.Cancel(ctx, clientID, created.ID, reason, "cancel-command-001")
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != "cancelled" {
		t.Fatalf("expected cancelled status, got %s", cancelled.Status)
	}
	if len(cancelled.Proposals) != 1 || cancelled.Proposals[0].Status != "rejected" {
		t.Fatalf("pending proposal was not closed: %#v", cancelled.Proposals)
	}
	lastEvent := cancelled.Events[len(cancelled.Events)-1]
	if lastEvent.Kind != "task_cancelled" || lastEvent.Note != reason || lastEvent.ToStatus != "cancelled" {
		t.Fatalf("missing cancellation evidence: %#v", lastEvent)
	}
	replayed, err := service.Cancel(ctx, clientID, created.ID, reason, "cancel-command-001")
	if err != nil || replayed.ID != created.ID || replayed.Status != "cancelled" {
		t.Fatalf("cancel replay failed: status=%s err=%v", replayed.Status, err)
	}
	var settlementCount int
	if err = pool.QueryRow(ctx, `SELECT COUNT(*) FROM task_settlements WHERE demand_id=$1`, created.ID).Scan(&settlementCount); err != nil {
		t.Fatal(err)
	}
	if settlementCount != 0 {
		t.Fatalf("cancelled task created settlement records: %d", settlementCount)
	}
}

func validTaskInput() tasks.CreateInput {
	return tasks.CreateInput{Title: "Campaign image system for a research launch", Summary: "A precise image family for a global research report launch.", Brief: "Create a coherent visual system that remains legible across editorial, social, and presentation formats.", DeliverableType: "image", Deliverables: []string{"3000px master image", "Vertical social crop"}, AcceptanceRules: []string{"No third-party marks", "Both exports pass visual inspection"}, RightsTerms: "Worldwide campaign use for twelve months.", AIDisclosureRequirement: "List models and source media used.", BudgetCents: 80000, Currency: "USD", Deadline: time.Now().Add(14 * 24 * time.Hour), ClientTimezone: "Europe/London"}
}

func seedTaskUsers(t *testing.T, pool *pgxpool.Pool, clientID, creatorID, outsiderID, assetID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	for i, id := range []uuid.UUID{clientID, creatorID, outsiderID} {
		if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role,status) VALUES($1,$2,$3,$4,'creator','active')`, id, id.String()+"@test.local", "task_user_"+id.String()[:8], []string{"Test Publisher", "Test Creator", "Test Outsider"}[i]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,width,height,scan_status,source_type,license_code) VALUES($1,$2,'image','Owned delivery','/api/v1/assets/test/content','image/jpeg',1200,1200,'clean','demo','test')`, assetID, creatorID); err != nil {
		t.Fatal(err)
	}
}

func taskTestPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	ctx := context.Background()
	baseURL := os.Getenv("TEST_DATABASE_URL")
	if baseURL == "" {
		baseURL = "postgres://hcai:hcai@localhost:5432/hcai?sslmode=disable"
	}
	admin, err := pgxpool.New(ctx, baseURL)
	if err != nil {
		t.Skipf("PostgreSQL integration database unavailable: %v", err)
	}
	if err := admin.Ping(ctx); err != nil {
		admin.Close()
		t.Skipf("PostgreSQL integration database unavailable: %v", err)
	}
	schema := "test_tasks_" + uuid.NewString()[:8]
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
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
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	}
}
