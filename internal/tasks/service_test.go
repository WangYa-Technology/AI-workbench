package tasks_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/payments"
	"github.com/hcai-chat/hcai-chat/internal/platform/database"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/tasks"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type taskPaymentRuntime struct {
	checkoutCalls int
	transferCalls int
	refundCalls   int
}

func (*taskPaymentRuntime) Provider() string { return "stripe" }

func (r *taskPaymentRuntime) CreateCheckout(_ context.Context, input payments.CheckoutRequest) (payments.CheckoutSession, error) {
	r.checkoutCalls++
	token := taskProviderToken(input.PaymentID)
	return payments.CheckoutSession{
		ProviderID: "cs_task_" + token, CheckoutURL: "https://checkout.stripe.com/c/pay/task-" + token, Status: "open",
		PaymentStatus: "unpaid", ExpiresAt: time.Now().Add(time.Hour).UTC(), LiveMode: false,
	}, nil
}

func (r *taskPaymentRuntime) CreateRefund(_ context.Context, input payments.RefundRequest) (payments.Refund, error) {
	r.refundCalls++
	return payments.Refund{
		ProviderID: "re_task_" + taskProviderToken(input.PaymentID), ProviderPaymentID: input.ProviderPaymentID,
		AmountCents: input.AmountCents, Currency: "USD", Status: "pending",
	}, nil
}

func (r *taskPaymentRuntime) CreateTransfer(_ context.Context, input payments.TransferRequest) (payments.Transfer, error) {
	r.transferCalls++
	return payments.Transfer{
		ProviderID: "tr_task_" + taskProviderToken(input.PaymentID), DestinationID: input.DestinationID, AmountCents: input.AmountCents,
		Currency: input.Currency, TransferGroup: "hcai_" + input.PaymentID.String(),
	}, nil
}

func (*taskPaymentRuntime) CreateConnectAccount(context.Context, payments.ConnectAccountRequest) (payments.ConnectAccount, error) {
	return payments.ConnectAccount{}, errors.New("not expected")
}

func (*taskPaymentRuntime) CreateAccountLink(context.Context, payments.AccountLinkRequest) (payments.AccountLink, error) {
	return payments.AccountLink{}, errors.New("not expected")
}

func taskProviderToken(paymentID uuid.UUID) string {
	return strings.ReplaceAll(paymentID.String(), "-", "")[:12]
}

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

func TestProviderFundedTaskAssignmentAndTransfer(t *testing.T) {
	pool, cleanup := taskTestPool(t)
	defer cleanup()
	ctx := context.Background()
	clientID, creatorID, outsiderID, assetID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	seedTaskUsers(t, pool, clientID, creatorID, outsiderID, assetID)
	taskService := tasks.NewServiceWithPayments(pool, true)
	runtime := &taskPaymentRuntime{}
	const webhookSecret = "whsec_task_funding_contract"
	paymentService := payments.NewServiceWithRuntimes(pool, payments.ServiceConfig{
		Enabled: true, APIVersion: "2026-02-25.clover", WebhookSecret: webhookSecret, WebhookTolerance: 5 * time.Minute,
	}, payments.NewRuntimeCatalog(runtime))

	created, err := taskService.Create(ctx, clientID, validTaskInput(), "provider-task-create-001")
	if err != nil {
		t.Fatal(err)
	}
	proposalInput := tasks.ProposeInput{
		Approach: "I will deliver a controlled image system with documented provenance.", Deliverables: "Master image and production crops",
		AmountCents: 70000, TimelineDays: 6,
	}
	if _, err := taskService.Propose(ctx, creatorID, created.ID, proposalInput, "provider-task-proposal-001"); err != nil {
		t.Fatal(err)
	}
	clientView, err := taskService.Get(ctx, clientID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	proposalID := clientView.Proposals[0].ID
	if _, err := taskService.AcceptProposal(ctx, clientID, created.ID, proposalID, "provider-task-accept-before-funding"); !errors.Is(err, tasks.ErrConflict) {
		t.Fatalf("unfunded proposal was assigned: %v", err)
	}
	checkout, started, err := paymentService.BeginTaskCheckout(ctx, clientID, created.ID, &proposalID, "provider-task-funding-001", "task-funding-request", "https://app.example.test/market/demands/task?payment=success", "https://app.example.test/market/demands/task?payment=cancelled")
	if err != nil || !started || checkout.AmountCents != proposalInput.AmountCents || checkout.RealCharge || runtime.checkoutCalls != 1 {
		t.Fatalf("task funding checkout mismatch: checkout=%#v started=%t calls=%d err=%v", checkout, started, runtime.checkoutCalls, err)
	}
	replayed, started, err := paymentService.BeginTaskCheckout(ctx, clientID, created.ID, &proposalID, "provider-task-funding-001", "task-funding-replay", "https://app.example.test/market/demands/task?payment=success", "https://app.example.test/market/demands/task?payment=cancelled")
	if err != nil || started || !replayed.AlreadyCreated || runtime.checkoutCalls != 1 {
		t.Fatalf("task funding replay mismatch: checkout=%#v started=%t calls=%d err=%v", replayed, started, runtime.checkoutCalls, err)
	}
	clientView, err = taskService.Get(ctx, clientID, created.ID)
	if err != nil || clientView.Funding == nil || clientView.Funding.Status != "checkout_open" ||
		clientView.Funding.ProposalID == nil || *clientView.Funding.ProposalID != proposalID ||
		clientView.Funding.CheckoutURL == nil || *clientView.Funding.CheckoutURL != checkout.CheckoutURL ||
		clientView.Funding.CheckoutExpiresAt == nil || clientView.Funding.PaymentMode != "stripe" || clientView.Funding.LiveMode {
		t.Fatalf("commissioner funding projection mismatch: funding=%#v err=%v", clientView.Funding, err)
	}
	publicView, err := taskService.Get(ctx, outsiderID, created.ID)
	if err != nil || publicView.Funding == nil || publicView.Funding.Status != "checkout_open" || publicView.Funding.CheckoutURL != nil || publicView.Funding.CheckoutExpiresAt != nil {
		t.Fatalf("public funding projection leaked checkout evidence: funding=%#v err=%v", publicView.Funding, err)
	}

	receipt := receiveTaskPaymentEvent(t, paymentService, taskPaymentSucceededEvent(checkout.PaymentID, created.ID, proposalInput.AmountCents), webhookSecret)
	if err := paymentService.HandlePaymentEventJob(ctx, jobs.Job{Kind: payments.PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID.String()))}); err != nil {
		t.Fatal(err)
	}
	clientView, err = taskService.Get(ctx, clientID, created.ID)
	if err != nil || clientView.Funding == nil || clientView.Funding.Status != "paid" || clientView.Funding.CheckoutURL != nil {
		t.Fatalf("confirmed task funding projection mismatch: funding=%#v err=%v", clientView.Funding, err)
	}
	assigned, err := taskService.AcceptProposal(ctx, clientID, created.ID, proposalID, "provider-task-accept-001")
	if err != nil || assigned.Status != "assigned" || assigned.Assignee == nil || assigned.Assignee.ID != creatorID {
		t.Fatalf("funded proposal assignment failed: task=%#v err=%v", assigned.Summary, err)
	}
	if _, err := taskService.Deliver(ctx, creatorID, created.ID, tasks.DeliverInput{AssetID: assetID, Note: "Provider-funded delivery ready for review."}, "provider-task-delivery-001"); err != nil {
		t.Fatal(err)
	}
	accepted, err := taskService.Review(ctx, clientID, created.ID, tasks.ReviewInput{Decision: "accept", Note: "All funded acceptance rules are satisfied."}, "provider-task-review-001")
	if err != nil || accepted.Settlement == nil || accepted.Settlement.Mode != "stripe_pending" || accepted.Funding == nil || accepted.Funding.Status != "transfer_pending" {
		t.Fatalf("Provider settlement was not queued: settlement=%#v funding=%#v err=%v", accepted.Settlement, accepted.Funding, err)
	}
	transferJob := jobs.Job{Kind: payments.TaskTransferJobKind, Payload: []byte(fmt.Sprintf(`{"paymentId":%q}`, checkout.PaymentID.String()))}
	if err := paymentService.HandleTaskTransferJob(ctx, transferJob); err == nil {
		t.Fatal("task transfer succeeded without a verified destination")
	}
	var localEntries int
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM billing_entries WHERE user_id IN ($1,$2)) +
		       (SELECT count(*) FROM ledger_entries WHERE account_id IN ($1,$2))`, clientID, creatorID).Scan(&localEntries); err != nil {
		t.Fatal(err)
	}
	if localEntries != 0 {
		t.Fatalf("Provider-funded task wrote Local Test entries: %d", localEntries)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_destinations(provider,user_id,destination_id,status,charges_enabled,payouts_enabled,details_submitted,verified_at)
		VALUES('stripe',$1,'acct_task_contract','verified',true,true,true,now())`, creatorID); err != nil {
		t.Fatal(err)
	}
	if err := paymentService.HandleTaskTransferJob(ctx, transferJob); err != nil {
		t.Fatal(err)
	}
	if err := paymentService.HandleTaskTransferJob(ctx, transferJob); err != nil {
		t.Fatalf("transfer replay failed: %v", err)
	}
	var intentStatus, settlementMode string
	if err := pool.QueryRow(ctx, `
		SELECT pi.status,ts.mode FROM payment_intents pi JOIN task_settlements ts ON ts.demand_id=pi.resource_id WHERE pi.id=$1`, checkout.PaymentID).Scan(&intentStatus, &settlementMode); err != nil {
		t.Fatal(err)
	}
	if intentStatus != "transferred" || settlementMode != "stripe_transferred" || runtime.transferCalls != 1 {
		t.Fatalf("task transfer mismatch: intent=%s settlement=%s calls=%d", intentStatus, settlementMode, runtime.transferCalls)
	}
	transferred, err := taskService.Get(ctx, creatorID, created.ID)
	if err != nil || transferred.Funding == nil || transferred.Funding.Status != "transferred" || transferred.Settlement == nil || transferred.Settlement.Mode != "stripe_transferred" {
		t.Fatalf("transferred task projection mismatch: funding=%#v settlement=%#v err=%v", transferred.Funding, transferred.Settlement, err)
	}
}

func TestProviderFundedDirectClaim(t *testing.T) {
	pool, cleanup := taskTestPool(t)
	defer cleanup()
	ctx := context.Background()
	clientID, creatorID, outsiderID, assetID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	seedTaskUsers(t, pool, clientID, creatorID, outsiderID, assetID)
	taskService := tasks.NewServiceWithPayments(pool, true)
	runtime := &taskPaymentRuntime{}
	const webhookSecret = "whsec_direct_task_contract"
	paymentService := payments.NewServiceWithRuntimes(pool, payments.ServiceConfig{
		Enabled: true, APIVersion: "2026-02-25.clover", WebhookSecret: webhookSecret, WebhookTolerance: 5 * time.Minute,
	}, payments.NewRuntimeCatalog(runtime))
	input := validTaskInput()
	input.AllowDirectAccept = true
	created, err := taskService.Create(ctx, clientID, input, "provider-direct-create-001")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := taskService.Claim(ctx, creatorID, created.ID, "provider-direct-unfunded-claim"); !errors.Is(err, tasks.ErrConflict) {
		t.Fatalf("unfunded direct task was claimed: %v", err)
	}
	checkout, started, err := paymentService.BeginTaskCheckout(ctx, clientID, created.ID, nil, "provider-direct-funding-001", "direct-funding-request", "https://app.example.test/market/demands/task?payment=success", "https://app.example.test/market/demands/task?payment=cancelled")
	if err != nil || !started || checkout.ProposalID != nil || checkout.AmountCents != input.BudgetCents {
		t.Fatalf("direct task checkout mismatch: checkout=%#v started=%t err=%v", checkout, started, err)
	}
	receipt := receiveTaskPaymentEvent(t, paymentService, taskPaymentSucceededEvent(checkout.PaymentID, created.ID, input.BudgetCents), webhookSecret)
	if err := paymentService.HandlePaymentEventJob(ctx, jobs.Job{Kind: payments.PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID.String()))}); err != nil {
		t.Fatal(err)
	}
	assigned, err := taskService.Claim(ctx, creatorID, created.ID, "provider-direct-funded-claim")
	if err != nil || assigned.Status != "assigned" || assigned.Assignee == nil || assigned.Assignee.ID != creatorID || assigned.Funding == nil || assigned.Funding.Status != "paid" || assigned.Funding.ProposalID != nil {
		t.Fatalf("funded direct task assignment mismatch: detail=%#v err=%v", assigned, err)
	}
	var payeeID *uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT payee_id FROM payment_intents WHERE id=$1`, checkout.PaymentID).Scan(&payeeID); err != nil || payeeID == nil || *payeeID != creatorID {
		t.Fatalf("direct task payment payee mismatch: payee=%v err=%v", payeeID, err)
	}
}

func TestProviderFundedOpenTaskCancellationRefund(t *testing.T) {
	pool, cleanup := taskTestPool(t)
	defer cleanup()
	ctx := context.Background()
	clientID, creatorID, outsiderID, assetID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	seedTaskUsers(t, pool, clientID, creatorID, outsiderID, assetID)
	taskService := tasks.NewServiceWithPayments(pool, true)
	runtime := &taskPaymentRuntime{}
	const webhookSecret = "whsec_cancelled_task_contract"
	paymentService := payments.NewServiceWithRuntimes(pool, payments.ServiceConfig{
		Enabled: true, APIVersion: "2026-02-25.clover", WebhookSecret: webhookSecret, WebhookTolerance: 5 * time.Minute,
	}, payments.NewRuntimeCatalog(runtime))
	input := validTaskInput()
	input.AllowDirectAccept = true
	created, err := taskService.Create(ctx, clientID, input, "provider-cancel-create-001")
	if err != nil {
		t.Fatal(err)
	}
	checkout, _, err := paymentService.BeginTaskCheckout(ctx, clientID, created.ID, nil, "provider-cancel-funding-001", "cancel-funding-request", "https://app.example.test/market/demands/task?payment=success", "https://app.example.test/market/demands/task?payment=cancelled")
	if err != nil {
		t.Fatal(err)
	}
	receipt := receiveTaskPaymentEvent(t, paymentService, taskPaymentSucceededEvent(checkout.PaymentID, created.ID, input.BudgetCents), webhookSecret)
	if err := paymentService.HandlePaymentEventJob(ctx, jobs.Job{Kind: payments.PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID.String()))}); err != nil {
		t.Fatal(err)
	}
	cancelled, err := taskService.Cancel(ctx, clientID, created.ID, "Campaign scope was withdrawn before a creator accepted the funded brief.", "provider-funded-cancel-001")
	if err != nil || cancelled.Status != "cancelled" || cancelled.Funding == nil || cancelled.Funding.Status != "refund_pending" {
		t.Fatalf("funded task cancellation mismatch: detail=%#v err=%v", cancelled, err)
	}
	refundJob := jobs.Job{Kind: payments.TaskRefundJobKind, Payload: []byte(fmt.Sprintf(`{"paymentId":%q}`, checkout.PaymentID.String()))}
	if err := paymentService.HandleTaskRefundJob(ctx, refundJob); err != nil || runtime.refundCalls != 1 {
		t.Fatalf("task refund request mismatch: calls=%d err=%v", runtime.refundCalls, err)
	}
	if err := paymentService.HandleTaskRefundJob(ctx, refundJob); err != nil || runtime.refundCalls != 1 {
		t.Fatalf("task refund replay mismatch: calls=%d err=%v", runtime.refundCalls, err)
	}
	refundReceipt := receiveTaskPaymentEvent(t, paymentService, taskRefundSucceededEvent(checkout.PaymentID, created.ID, input.BudgetCents), webhookSecret)
	if err := paymentService.HandlePaymentEventJob(ctx, jobs.Job{Kind: payments.PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, refundReceipt.EventID.String()))}); err != nil {
		t.Fatal(err)
	}
	refunded, err := taskService.Get(ctx, clientID, created.ID)
	if err != nil || refunded.Funding == nil || refunded.Funding.Status != "refunded" || refunded.Status != "cancelled" {
		t.Fatalf("task refund completion mismatch: detail=%#v err=%v", refunded, err)
	}
	var localEntries int
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM billing_entries WHERE user_id IN ($1,$2)) +
		       (SELECT count(*) FROM ledger_entries WHERE account_id IN ($1,$2))`, clientID, creatorID).Scan(&localEntries); err != nil || localEntries != 0 {
		t.Fatalf("Provider task refund wrote Local Test entries: count=%d err=%v", localEntries, err)
	}
}

func receiveTaskPaymentEvent(t *testing.T, service *payments.Service, body []byte, secret string) payments.Receipt {
	t.Helper()
	now := time.Now().UTC().Unix()
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.", now)
	_, _ = mac.Write(body)
	receipt, err := service.ReceiveStripeWebhook(context.Background(), body, "t="+fmt.Sprint(now)+",v1="+hex.EncodeToString(mac.Sum(nil)))
	if err != nil || receipt.Status != "received" {
		t.Fatalf("receive task payment event: receipt=%#v err=%v", receipt, err)
	}
	return receipt
}

func taskPaymentSucceededEvent(paymentID, taskID uuid.UUID, amount int) []byte {
	token := taskProviderToken(paymentID)
	return []byte(fmt.Sprintf(`{"id":"evt_task_funded_%s","object":"event","api_version":"2026-02-25.clover","created":%d,"livemode":false,"type":"payment_intent.succeeded","data":{"object":{"id":"pi_task_%s","object":"payment_intent","status":"succeeded","amount_received":%d,"currency":"usd","latest_charge":"ch_task_%s","metadata":{"hcai_payment_id":%q,"hcai_resource_id":%q,"hcai_purpose":"task"}}}}`, token, time.Now().UTC().Unix(), token, amount, token, paymentID.String(), taskID.String()))
}

func taskRefundSucceededEvent(paymentID, taskID uuid.UUID, amount int) []byte {
	token := taskProviderToken(paymentID)
	return []byte(fmt.Sprintf(`{"id":"evt_task_refunded_%s","object":"event","api_version":"2026-02-25.clover","created":%d,"livemode":false,"type":"refund.updated","data":{"object":{"id":"re_task_%s","object":"refund","status":"succeeded","amount":%d,"currency":"usd","payment_intent":"pi_task_%s","metadata":{"hcai_payment_id":%q,"hcai_resource_id":%q,"hcai_purpose":"task"}}}}`, token, time.Now().UTC().Unix(), token, amount, token, paymentID.String(), taskID.String()))
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
