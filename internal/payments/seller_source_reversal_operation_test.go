package payments

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestSellerSourceReversalOperationLifecycle(t *testing.T) {
	pool, service, runtime, request, actor := sourceReversalCommandFixture(t)
	ctx := t.Context()
	before := reversalCommandInput(t, pool, request)
	view, err := service.GetSellerSourceReversalOperation(ctx, actor, request.ID)
	if err != nil || !view.CanSubmit || view.CanClose || view.Command != nil || view.Bank == nil || view.Bank.Disposition != "not_reserved" || !view.ExpectedUpdatedAt.Equal(before.ExpectedUpdatedAt) {
		t.Fatal("unstarted view", view, err)
	}
	assertSourceReversalCommandCount(t, pool, 0)
	service.config.Enabled = false
	view, err = service.GetSellerSourceReversalOperation(ctx, actor, request.ID)
	if err != nil || view.CanSubmit {
		t.Fatal("disabled writes advertised", view, err)
	}
	service.config.Enabled = true
	input := SellerSourceReversalInput{SourceTransferID: view.Request.Funding.TransferID, ExpectedUpdatedAt: view.ExpectedUpdatedAt,
		BankCommandID: view.Bank.CommandID, BankResultID: view.Bank.ResultID, Reason: "Confirm source return using the finance operation snapshot.", Confirmed: true}
	queued, err := service.SubmitSellerSourceReversal(ctx, actor, request.ID, input, "operation-reversal", "trace")
	if err != nil {
		t.Fatal(err)
	}
	view, err = service.GetSellerSourceReversalOperation(ctx, actor, request.ID)
	if err != nil || view.CanSubmit || view.CanClose || view.Command == nil || view.Command.ID != queued.Command.ID || view.JobStatus == nil || *view.JobStatus != "queued" || view.LatestRead != nil {
		t.Fatal("queued view", view, err)
	}
	if runtime.reversals.Load() != 0 {
		t.Fatal("read contacted provider")
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='cancelled' WHERE id=$1`, queued.Command.JobID); err != nil {
		t.Fatal(err)
	}
	view, err = service.GetSellerSourceReversalOperation(ctx, actor, request.ID)
	if err != nil || view.CanSubmit || view.CanClose || view.JobStatus == nil || *view.JobStatus != "cancelled" {
		t.Fatal("stopped command disguised as retryable", view, err)
	}
	// Read endpoints cannot obtain a disposition lock that increments version.
	var versionBefore, versionAfter int
	if err := pool.QueryRow(ctx, `SELECT revision FROM seller_payout_disposition_locks WHERE payout_request_id=$1`, request.ID).Scan(&versionBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetSellerSourceReversalOperation(ctx, actor, request.ID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT revision FROM seller_payout_disposition_locks WHERE payout_request_id=$1`, request.ID).Scan(&versionAfter); err != nil || versionBefore != versionAfter {
		t.Fatal("read changed financial graph", versionBefore, versionAfter, err)
	}
}

func TestSellerSourceReversalOperationClosureAndAuthority(t *testing.T) {
	pool, service, runtime, request, command, job := sourceReversalExecutionFixture(t)
	ctx := t.Context()
	if err := service.HandleSellerSourceReversalJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	actor, _ := sourceClosureInput(t, pool, command.ID)
	service.config.Enabled = false
	view, err := service.GetSellerSourceReversalOperation(ctx, actor, request.ID)
	if err != nil || !view.CanClose || view.CanSubmit || view.AcceptedReadID == nil || view.LatestRead == nil || view.RequiresReview || view.CloseResolution == nil || *view.CloseResolution != "released" {
		t.Fatal("proven return not actionable", view, err)
	}
	for _, outsider := range []uuid.UUID{uuid.Nil, uuid.New(), request.SellerID} {
		if _, err := service.GetSellerSourceReversalOperation(ctx, outsider, request.ID); !errors.Is(err, ErrFinanceForbidden) {
			t.Fatal("unprivileged reader", err)
		}
	}
	if _, err := service.GetSellerSourceReversalOperation(ctx, actor, uuid.New()); !errors.Is(err, ErrSellerPayoutNotFound) {
		t.Fatal("unknown request", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1`, request.SellerID); err != nil {
		t.Fatal(err)
	}
	self, err := service.GetSellerSourceReversalOperation(ctx, request.SellerID, request.ID)
	if err != nil || self.CanClose || self.CanSubmit {
		t.Fatal("self finance can close", self, err)
	}
	encoded, _ := json.Marshal(view)
	for _, forbidden := range []string{"provider_identity", "providerIdentity", "dispatchKey", "idempotencyKey", "evidence", "providerChargeId", "request_id"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatal("private execution data exposed", forbidden)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetSellerSourceReversalOperation(ctx, actor, request.ID); !errors.Is(err, ErrFinanceForbidden) {
		t.Fatal("revoked reader", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1`, actor); err != nil {
		t.Fatal(err)
	}
	input := SellerSourceClosureInput{ReadID: *view.AcceptedReadID, ExpectedUpdatedAt: view.ExpectedUpdatedAt, Reason: "Consume the verified complete source return from this snapshot.", Confirmed: true}
	closed, err := service.CloseSellerSourceReversal(ctx, actor, command.ID, input, "operation-close", "trace")
	if err != nil {
		t.Fatal(err)
	}
	view, err = service.GetSellerSourceReversalOperation(ctx, actor, request.ID)
	if err != nil || view.CanClose || view.CanSubmit || view.Closure == nil || view.Closure.ID != closed.Closure.ID || view.Request.Status != "cancelled" || view.CloseResolution != nil {
		t.Fatal("closed view", view, err)
	}
	assertSourceClosure(t, pool, request, 1)
	if runtime.creates.Load() != 1 || runtime.queries.Load() != 0 {
		t.Fatal("read or close called provider")
	}
}
