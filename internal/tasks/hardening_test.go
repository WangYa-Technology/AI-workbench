package tasks_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/hcai-chat/hcai-chat/internal/creation"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/identity"
	"github.com/hcai-chat/hcai-chat/internal/payments"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/tasks"
)

func TestTaskPrivacyAndCommissionerReviewAccess(t *testing.T) {
	pool, cleanup := taskTestPool(t)
	defer cleanup()
	ctx := context.Background()
	client, creator, outsider, asset := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	seedTaskUsers(t, pool, client, creator, outsider, asset)
	if _, err := pool.Exec(ctx, `UPDATE assets SET source_type='upload',storage_backend='local_file',storage_key='delivery.png' WHERE id=$1`, asset); err != nil {
		t.Fatal(err)
	}
	service := tasks.NewService(pool)
	input := validTaskInput()
	input.AllowDirectAccept = true
	task, err := service.Create(ctx, client, input, "privacy-create-001")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Claim(ctx, creator, task.ID, "privacy-claim-001"); err != nil {
		t.Fatal(err)
	}
	media := assets.NewService(pool, t.TempDir())
	if _, err = media.Content(ctx, client, asset); !errors.Is(err, assets.ErrForbidden) {
		t.Fatalf("unsubmitted asset exposed: %v", err)
	}
	if _, err = service.Deliver(ctx, creator, task.ID, tasks.DeliverInput{RightsEvidence: "Original assets with the rights required by this brief.", AIDisclosure: "Model and source evidence supplied for review.", RightsConfirmed: true, AssetID: asset, Note: "Confidential delivery evidence."}, "privacy-deliver-001"); err != nil {
		t.Fatal(err)
	}
	for _, viewer := range []uuid.UUID{uuid.Nil, outsider} {
		detail, err := service.Get(ctx, viewer, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(detail.Deliveries) != 0 || len(detail.Events) != 0 || detail.Settlement != nil {
			t.Fatal("private fulfillment evidence leaked")
		}
		if _, err = media.Content(ctx, viewer, asset); !errors.Is(err, assets.ErrForbidden) {
			t.Fatalf("nonparticipant read asset: %v", err)
		}
	}
	detail, err := service.Get(ctx, client, task.ID)
	if err != nil || len(detail.Deliveries) != 1 || len(detail.Events) == 0 {
		t.Fatalf("missing commissioner evidence: %v", err)
	}
	if _, err = media.Content(ctx, client, asset); err != nil {
		t.Fatalf("commissioner cannot review: %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1`, outsider); err != nil {
		t.Fatal(err)
	}
	operations, err := service.Get(ctx, outsider, task.ID)
	if err != nil || operations.ViewerRole != "operator" || len(operations.Deliveries) != 1 {
		t.Fatalf("operator cannot review evidence: %v", err)
	}
	if _, err = service.OpenDispute(ctx, client, task.ID, "Reviewing a disagreement about the submitted evidence.", "privacy-dispute-key"); err != nil {
		t.Fatal(err)
	}
	if _, err = media.Content(ctx, outsider, asset); err != nil {
		t.Fatalf("authorized operator cannot inspect disputed delivery: %v", err)
	}
	operatorContent, err := media.Content(ctx, outsider, asset)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, outsider); err != nil {
		t.Fatal(err)
	}
	if _, err = operatorContent.Open(ctx, nil); !errors.Is(err, assets.ErrForbidden) {
		t.Fatalf("stale operator handle survived revoked role: %v", err)
	}
	clientContent, err := media.Content(ctx, client, asset)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, client); err != nil {
		t.Fatal(err)
	}
	if _, err = clientContent.Open(ctx, nil); !errors.Is(err, assets.ErrForbidden) {
		t.Fatalf("stale commissioner handle survived suspension: %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE users SET status='active' WHERE id=$1`, client); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE assets SET scan_status='review' WHERE id=$1`, asset); err != nil {
		t.Fatal(err)
	}
	if _, err = media.Content(ctx, client, asset); !errors.Is(err, assets.ErrNotFound) {
		t.Fatalf("task permission bypassed scan: %v", err)
	}
}

func TestTaskCommandScopeAndConcurrentCreate(t *testing.T) {
	pool, cleanup := taskTestPool(t)
	defer cleanup()
	ctx := context.Background()
	client, creator, outsider, asset := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	seedTaskUsers(t, pool, client, creator, outsider, asset)
	service := tasks.NewService(pool)
	input := validTaskInput()
	input.AllowDirectAccept = true
	var wg sync.WaitGroup
	ids := make(chan uuid.UUID, 4)
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, e := service.Create(ctx, client, input, "concurrent-create-key")
			ids <- d.ID
			errs <- e
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var id uuid.UUID
	for next := range ids {
		if id != uuid.Nil && id != next {
			t.Fatal("duplicate tasks created")
		}
		id = next
	}
	other, err := service.Create(ctx, client, input, "other-create-key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Claim(ctx, creator, id, "shared-claim-key"); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Claim(ctx, creator, other.ID, "shared-claim-key"); !errors.Is(err, tasks.ErrConflict) {
		t.Fatalf("cross-task key replay accepted: %v", err)
	}
	unchanged, err := service.Get(ctx, client, other.ID)
	if err != nil || unchanged.Status != "open" {
		t.Fatalf("second task changed: %v", err)
	}
}

func TestTaskInputBoundariesAndDirectProposalConversion(t *testing.T) {
	pool, cleanup := taskTestPool(t)
	defer cleanup()
	ctx := context.Background()
	client, creator, other, asset := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	seedTaskUsers(t, pool, client, creator, other, asset)
	service := tasks.NewService(pool)
	for _, change := range []func(*tasks.CreateInput){
		func(i *tasks.CreateInput) { i.BudgetCents = 49 }, func(i *tasks.CreateInput) { i.BudgetCents = 100000000 },
		func(i *tasks.CreateInput) { i.Deliverables = []string{"  "} }, func(i *tasks.CreateInput) { i.AcceptanceRules = []string{" "} },
		func(i *tasks.CreateInput) { i.ClientTimezone = "Invalid/Zone" }, func(i *tasks.CreateInput) { i.Title = "两个" },
	} {
		input := validTaskInput()
		change(&input)
		if _, err := service.Create(ctx, client, input, uuid.NewString()); !errors.Is(err, tasks.ErrInvalid) {
			t.Fatalf("invalid task accepted: %v", err)
		}
	}
	input := validTaskInput()
	input.AllowDirectAccept = true
	input.BudgetCents = 50
	task, err := service.Create(ctx, client, input, "conversion-create-key")
	if err != nil {
		t.Fatal(err)
	}
	proposal := tasks.ProposeInput{Approach: "A detailed and reproducible production approach.", Deliverables: "Master file and production notes", AmountCents: 50, TimelineDays: 2}
	for _, actor := range []uuid.UUID{creator, other} {
		if _, err = service.Propose(ctx, actor, task.ID, proposal, uuid.NewString()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = service.Claim(ctx, creator, task.ID, "conversion-claim-key"); err != nil {
		t.Fatal(err)
	}
	detail, err := service.Get(ctx, client, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range detail.Proposals {
		expected := "rejected"
		if p.Creator.ID == creator {
			expected = "accepted"
		}
		if p.Status != expected {
			t.Fatalf("proposal not closed: %s", p.Status)
		}
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE user_id=$1 AND kind='task.proposal_rejected'`, other).Scan(&count); err != nil || count != 1 {
		t.Fatalf("losing proposer not notified: %d %v", count, err)
	}
}

func TestDeliveryBundleGrantAndContractSnapshot(t *testing.T) {
	pool, cleanup := taskTestPool(t)
	defer cleanup()
	ctx := context.Background()
	client, creator, other, asset := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	seedTaskUsers(t, pool, client, creator, other, asset)
	if _, err := pool.Exec(ctx, `UPDATE assets SET source_type='upload',storage_backend='local_file',storage_key='master.png' WHERE id=$1`, asset); err != nil {
		t.Fatal(err)
	}
	attachment := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,storage_backend,storage_key) VALUES($1::uuid,$2,'document','Source notes','/api/v1/assets/'||$1::text||'/content','text/plain','clean','upload','local_file','notes.txt')`, attachment, creator); err != nil {
		t.Fatal(err)
	}
	service := tasks.NewService(pool)
	input := validTaskInput()
	input.AllowDirectAccept = true
	task, err := service.Create(ctx, client, input, "bundle-create-key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Claim(ctx, creator, task.ID, "bundle-claim-key"); err != nil {
		t.Fatal(err)
	}
	delivery := tasks.DeliverInput{AssetIDs: []uuid.UUID{asset, attachment}, Note: "Master file and complete source notes.", RightsEvidence: "Original source files licensed for the agreed campaign.", AIDisclosure: "Generated with documented models; source files attached.", RightsConfirmed: true}
	unconfirmed := delivery
	unconfirmed.RightsConfirmed = false
	if _, err = service.Deliver(ctx, creator, task.ID, unconfirmed, "unconfirmed-delivery-key"); !errors.Is(err, tasks.ErrInvalid) {
		t.Fatalf("unconfirmed rights allowed: %v", err)
	}
	wrongKind := delivery
	wrongKind.AssetIDs = []uuid.UUID{attachment}
	if _, err = service.Deliver(ctx, creator, task.ID, wrongKind, "wrong-kind-delivery-key"); !errors.Is(err, tasks.ErrInvalid) {
		t.Fatalf("wrong primary kind allowed: %v", err)
	}
	submitted, err := service.Deliver(ctx, creator, task.ID, delivery, "bundle-delivery-key")
	if err != nil {
		t.Fatal(err)
	}
	if len(submitted.Deliveries[0].Assets) != 2 {
		t.Fatal("bundle truncated")
	}
	root := t.TempDir()
	for _, name := range []string{"master.png", "notes.txt"} {
		if err = os.WriteFile(filepath.Join(root, name), []byte("contracted handover bytes"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	media := assets.NewService(pool, root)
	if _, err = media.Content(ctx, client, attachment); err != nil {
		t.Fatalf("supporting file not readable: %v", err)
	}
	// Scanner decisions made after submission must still prevent settlement.
	if _, err = pool.Exec(ctx, `UPDATE assets SET scan_status='review' WHERE id=$1`, attachment); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Review(ctx, client, task.ID, tasks.ReviewInput{Decision: "accept"}, "unsafe-review-key"); !errors.Is(err, tasks.ErrConflict) {
		t.Fatalf("unsafe attachment accepted: %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE assets SET scan_status='clean' WHERE id=$1`, attachment); err != nil {
		t.Fatal(err)
	}
	review := tasks.ReviewInput{Decision: "accept", Note: "All acceptance criteria verified."}
	if _, err = service.Review(ctx, client, task.ID, review, "bundle-review-key"); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Review(ctx, client, task.ID, review, "bundle-review-key"); err != nil {
		t.Fatal(err)
	}
	var count int
	var copied uuid.UUID
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM task_delivery_grants WHERE demand_id=$1`, task.ID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("grant count %d: %v", count, err)
	}
	if err = pool.QueryRow(ctx, `SELECT asset_id FROM task_delivery_grants WHERE demand_id=$1 AND source_asset_id=$2`, task.ID, asset).Scan(&copied); err != nil {
		t.Fatal(err)
	}
	owned, err := media.GetOwned(ctx, client, copied)
	if err != nil {
		t.Fatal(err)
	}
	if owned.Provenance == nil || owned.Provenance.TaskGrant == nil || owned.Provenance.TaskGrant.RightsTerms != input.RightsTerms || owned.Provenance.TaskGrant.AllowDerivativeReuse {
		t.Fatalf("wrong grant snapshot: %#v", owned.Provenance)
	}
	if _, err = media.Content(ctx, client, copied); err != nil {
		t.Fatalf("commissioner cannot download granted copy: %v", err)
	}
	if _, err = media.Content(ctx, other, copied); !errors.Is(err, assets.ErrForbidden) {
		t.Fatalf("private copy exposed: %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE task_delivery_grants SET allow_derivative_reuse=true WHERE asset_id=$1`, copied); err == nil {
		t.Fatal("grant contract mutable")
	}
	generation := creation.NewService(pool, root, filepath.Join(root, "master.png"), true)
	if _, err = generation.SubmitCommand(ctx, client, creation.SubmitInput{Mode: "image", Prompt: "Use the delivered reference for a new composition", SourceAssetIDs: []uuid.UUID{copied}}, "restricted-reuse-key", "restricted-reuse-request"); !errors.Is(err, creation.ErrInvalid) {
		t.Fatalf("restricted grant reused: %v", err)
	}
	input.AllowDerivativeReuse = true
	derivativeTask, e := service.Create(ctx, client, input, "derivative-task-create")
	if e != nil {
		t.Fatal(e)
	}
	if _, err = service.Claim(ctx, creator, derivativeTask.ID, "derivative-task-claim"); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Deliver(ctx, creator, derivativeTask.ID, delivery, "derivative-task-delivery"); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Review(ctx, client, derivativeTask.ID, review, "derivative-task-review"); err != nil {
		t.Fatal(err)
	}
	var reusable uuid.UUID
	if err = pool.QueryRow(ctx, `SELECT asset_id FROM task_delivery_grants WHERE demand_id=$1 AND source_asset_id=$2`, derivativeTask.ID, asset).Scan(&reusable); err != nil {
		t.Fatal(err)
	}
	if _, err = generation.SubmitCommand(ctx, client, creation.SubmitInput{Mode: "image", Prompt: "Use the explicitly licensed task reference", SourceAssetIDs: []uuid.UUID{reusable}}, "allowed-reuse-key", "allowed-reuse-request"); err != nil {
		t.Fatalf("explicit derivative grant not usable: %v", err)
	}
	// Simulate authenticated deletion requests in this isolated schema, including grace expiry.
	for _, actor := range []uuid.UUID{creator, client} {
		token := uuid.NewString()
		var handle string
		if err = pool.QueryRow(ctx, `SELECT handle FROM users WHERE id=$1`, actor).Scan(&handle); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO sessions(user_id,token_hash,expires_at) VALUES($1,$2,now()+interval '1 day')`, actor, identity.HashToken(token)); err != nil {
			t.Fatal(err)
		}
		rights := datarights.NewService(pool, root)
		request, e := rights.Create(ctx, actor, token, datarights.CreateInput{RequestType: "account_deletion", IdentityConfirmation: handle}, "grant-owner-deletion")
		if e != nil {
			t.Fatal(e)
		}
		if _, err = pool.Exec(ctx, `UPDATE data_rights_requests SET execute_after=now()-interval '1 minute',cancel_until=now()-interval '1 minute' WHERE id=$1`, request.ID); err != nil {
			t.Fatal(err)
		}
		payload, _ := json.Marshal(map[string]any{"requestId": request.ID})
		if err = rights.HandleDeletionJob(ctx, jobs.Job{Payload: payload}); err != nil {
			t.Fatal(err)
		}
		_, statErr := os.Stat(filepath.Join(root, "master.png"))
		if actor == creator {
			if statErr != nil {
				t.Fatalf("creator deletion erased client handover: %v", statErr)
			}
			if _, err = media.Content(ctx, client, copied); err != nil {
				t.Fatalf("client handover inaccessible after creator deletion: %v", err)
			}
		} else if !os.IsNotExist(statErr) {
			t.Fatalf("unneeded contracted bytes remain after both accounts delete: %v", statErr)
		}
	}

}

func TestTaskPaginationAndCounts(t *testing.T) {
	pool, cleanup := taskTestPool(t)
	defer cleanup()
	ctx := context.Background()
	client, creator, other, asset := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	seedTaskUsers(t, pool, client, creator, other, asset)
	if _, err := pool.Exec(ctx, `INSERT INTO demands(client_id,title,brief,deliverable_type,budget_cents,currency,deadline,status,created_at)
 SELECT $1,'Page task '||n,'Pagination scope',CASE WHEN n<=80 THEN 'image' ELSE 'video' END,5000,'USD',now()+interval '7 days','open',now() FROM generate_series(1,105) n`, client); err != nil {
		t.Fatal(err)
	}
	service := tasks.NewService(pool)
	for _, sort := range []string{"newest", "deadline", "budget_desc"} {
		filter := tasks.ListFilter{Sort: sort, Limit: 40}
		seen := map[uuid.UUID]bool{}
		for {
			page, err := service.ListPage(ctx, uuid.Nil, filter)
			if err != nil {
				t.Fatal(err)
			}
			if page.Total != 105 || page.TypeCounts["image"] != 80 || page.TypeCounts["video"] != 25 {
				t.Fatalf("truncated counts: %#v", page.TypeCounts)
			}
			for _, item := range page.Items {
				if seen[item.ID] {
					t.Fatal("duplicate across pages")
				}
				seen[item.ID] = true
			}
			if page.NextCursor == nil {
				break
			}
			filter.Cursor = *page.NextCursor
		}
		if len(seen) != 105 {
			t.Fatalf("missing rows: %d", len(seen))
		}
	}
	filter := tasks.ListFilter{DeliverableType: "image", Limit: 40}
	page, err := service.ListPage(ctx, uuid.Nil, filter)
	if err != nil || page.Total != 80 || page.TypeCounts["video"] != 25 || page.NextCursor == nil {
		t.Fatalf("filtered counts incorrect: %#v %v", page, err)
	}
	filter.Cursor = *page.NextCursor
	filter.DeliverableType = "video"
	if _, err = service.ListPage(ctx, uuid.Nil, filter); !errors.Is(err, tasks.ErrInvalid) {
		t.Fatalf("cursor escaped filter scope: %v", err)
	}
}

func TestDeadlineExpiryAgreementAndPreDeliveryDispute(t *testing.T) {
	pool, cleanup := taskTestPool(t)
	defer cleanup()
	ctx := context.Background()
	client, creator, other, asset := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	seedTaskUsers(t, pool, client, creator, other, asset)
	service := tasks.NewService(pool)
	input := validTaskInput()
	input.AllowDirectAccept = true
	expired, err := service.Create(ctx, client, input, "expiry-create-key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE demands SET deadline=now()-interval '1 minute' WHERE id=$1`, expired.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Claim(ctx, creator, expired.ID, "expiry-claim-key"); !errors.Is(err, tasks.ErrConflict) {
		t.Fatalf("expired task assigned: %v", err)
	}
	raw, _ := json.Marshal(map[string]any{"taskId": expired.ID})
	if err = service.HandleExpiryJob(ctx, jobs.Job{Payload: raw}); err != nil {
		t.Fatal(err)
	}
	if err = service.HandleExpiryJob(ctx, jobs.Job{Payload: raw}); err != nil {
		t.Fatal(err)
	}
	closed, err := service.Get(ctx, client, expired.ID)
	if err != nil || closed.Status != "cancelled" {
		t.Fatalf("not expired: %v", err)
	}
	task, err := service.Create(ctx, client, input, "extension-create-key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Claim(ctx, creator, task.ID, "extension-claim-key"); err != nil {
		t.Fatal(err)
	}
	proposed := tasks.DeadlineInput{Decision: "propose", Deadline: input.Deadline.Add(48 * time.Hour), Reason: "Additional source material needs another review cycle."}
	if _, err = service.ChangeDeadline(ctx, other, task.ID, proposed, "outsider-extension-key"); !errors.Is(err, tasks.ErrForbidden) {
		t.Fatalf("outsider proposed deadline: %v", err)
	}
	pending, err := service.ChangeDeadline(ctx, creator, task.ID, proposed, "extension-propose-key")
	if err != nil || pending.DeadlineChange == nil {
		t.Fatalf("proposal missing: %v", err)
	}
	if !pending.Deadline.Equal(task.Deadline) {
		t.Fatal("proposal unilaterally changed deadline")
	}
	approve := tasks.DeadlineInput{Decision: "accept", ChangeID: pending.DeadlineChange.ID}
	if _, err = service.ChangeDeadline(ctx, creator, task.ID, approve, "self-approve-key"); !errors.Is(err, tasks.ErrForbidden) {
		t.Fatalf("self approval accepted: %v", err)
	}
	agreed, err := service.ChangeDeadline(ctx, client, task.ID, approve, "extension-accept-key")
	if err != nil {
		t.Fatal(err)
	}
	if agreed.DeadlineChange != nil || agreed.Deadline.Sub(task.Deadline) != 48*time.Hour {
		t.Fatal("bilateral agreement not applied")
	}
	disputed, err := service.OpenDispute(ctx, client, task.ID, "The creator cannot continue; requesting reviewed cancellation before delivery.", "pre-delivery-dispute-key")
	if err != nil || disputed.Status != "disputed" {
		t.Fatalf("no pre-delivery exit: %v", err)
	}
}

func TestProviderFundedExpiryAndLateSuccess(t *testing.T) {
	for _, paidBeforeExpiry := range []bool{true, false} {
		t.Run(fmt.Sprint(paidBeforeExpiry), func(t *testing.T) {
			pool, cleanup := taskTestPool(t)
			defer cleanup()
			ctx := context.Background()
			client, creator, other, asset := uuid.New(), uuid.New(), uuid.New(), uuid.New()
			seedTaskUsers(t, pool, client, creator, other, asset)
			service := tasks.NewServiceWithPayments(pool, true)
			runtime := &taskPaymentRuntime{}
			const secret = "whsec_expiry_contract"
			paymentsService := payments.NewServiceWithRuntimes(pool, payments.ServiceConfig{Enabled: true, APIVersion: "2026-02-25.clover", WebhookSecret: secret, WebhookTolerance: 5 * time.Minute}, payments.NewRuntimeCatalog(runtime))
			input := validTaskInput()
			input.AllowDirectAccept = true
			task, err := service.Create(ctx, client, input, "expiry-funded-create")
			if err != nil {
				t.Fatal(err)
			}
			checkout, _, err := paymentsService.BeginTaskCheckout(ctx, client, task.ID, nil, "expiry-funding-key", "expiry-funding-request", "https://app.example.test/success", "https://app.example.test/cancel")
			if err != nil {
				t.Fatal(err)
			}
			fund := func() {
				receipt := receiveTaskPaymentEvent(t, paymentsService, taskPaymentSucceededEvent(checkout.PaymentID, task.ID, input.BudgetCents), secret)
				payload, _ := json.Marshal(map[string]any{"eventId": receipt.EventID})
				if err = paymentsService.HandlePaymentEventJob(ctx, jobs.Job{Payload: payload}); err != nil {
					t.Fatal(err)
				}
			}
			if paidBeforeExpiry {
				fund()
			}
			if _, err = pool.Exec(ctx, `UPDATE demands SET deadline=now()-interval '1 minute' WHERE id=$1`, task.ID); err != nil {
				t.Fatal(err)
			}
			payload, _ := json.Marshal(map[string]any{"taskId": task.ID})
			if err = service.HandleExpiryJob(ctx, jobs.Job{Payload: payload}); err != nil {
				t.Fatal(err)
			}
			if !paidBeforeExpiry {
				fund()
			}
			if err = service.HandleExpiryJob(ctx, jobs.Job{Payload: payload}); err != nil {
				t.Fatal(err)
			}
			result, err := service.Get(ctx, client, task.ID)
			if err != nil || result.Status != "cancelled" || result.Funding == nil || result.Funding.Status != "refund_pending" {
				t.Fatalf("expiry did not refund: %#v %v", result.Funding, err)
			}
			var count int
			if err = pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind=$1 AND payload->>'paymentId'=$2`, payments.TaskRefundJobKind, checkout.PaymentID.String()).Scan(&count); err != nil || count != 1 {
				t.Fatalf("refund queued %d times: %v", count, err)
			}
		})
	}
}
