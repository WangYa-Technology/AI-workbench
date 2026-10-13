package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/payments"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
)

type sourceReversalHTTPRuntime struct {
	*bankHTTPSourceRuntime
	creates int
}

func (r *sourceReversalHTTPRuntime) CreateTransferReversal(_ context.Context, input payments.TransferReversalRequest) (payments.TransferReversalResult, error) {
	r.creates++
	return payments.TransferReversalResult{Outcome: "found",
		Transfer: payments.TransferObservation{Transfer: payments.Transfer{ProviderID: input.ProviderTransferID,
			DestinationID: input.DestinationID, AmountCents: input.AmountCents, Currency: input.Currency, TransferGroup: "hcai_" + input.PaymentID.String()},
			PaymentID: input.PaymentID, ProviderChargeID: input.ProviderChargeID, LiveMode: input.LiveMode,
			CreatedAt: input.ReservedAt.Add(-time.Hour), AmountReversed: input.AmountCents},
		Observations: []payments.TransferReversal{{ProviderID: "trr_http_returned_source", ProviderTransferID: input.ProviderTransferID,
			CommandID: input.CommandID, AmountCents: input.ReverseAmountCents, Currency: input.Currency, CreatedAt: input.ReservedAt}},
	}, nil
}

func (r *sourceReversalHTTPRuntime) LookupTransferReversal(context.Context, payments.TransferReversalRequest) (payments.TransferReversalResult, error) {
	panic("unexpected source reversal lookup")
}

func TestSellerSourceReversalHTTPConfirmationAndClosure(t *testing.T) {
	f := newSellerFundsHTTPFixture(t)
	ctx := t.Context()
	path := f.base + "/source-reversal"
	call := func(client *http.Client, method, target, body string, keys []string, want int) []byte {
		t.Helper()
		req, err := http.NewRequest(method, target, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		for _, key := range keys {
			req.Header.Add("Idempotency-Key", key)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != want || res.Header.Get("Cache-Control") != "private, no-store" {
			t.Fatalf("%s %s status=%d want=%d body=%s", method, target, res.StatusCode, want, data)
		}
		return data
	}
	read := func() payments.SellerSourceReversalOperation {
		t.Helper()
		var view payments.SellerSourceReversalOperation
		data := call(f.financeClient, http.MethodGet, path, "", nil, 200)
		if err := json.Unmarshal(data, &view); err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"dispatchKey", "idempotencyKey", "providerIdentity", "evidence", "providerChargeId", "whsec_", "sk_test_"} {
			if strings.Contains(string(data), secret) {
				t.Fatal("private execution payload exposed", secret)
			}
		}
		return view
	}
	view := read()
	if !view.CanSubmit || view.CanClose || view.Bank == nil || view.Bank.Disposition != "not_reserved" || view.Request.Funding == nil {
		t.Fatal("initial operation", view)
	}
	input := payments.SellerSourceReversalInput{SourceTransferID: view.Request.Funding.TransferID, ExpectedUpdatedAt: view.ExpectedUpdatedAt,
		BankCommandID: view.Bank.CommandID, BankResultID: view.Bank.ResultID, Reason: "Authorize return of the source with no outstanding bank payout.", Confirmed: true}
	raw, _ := json.Marshal(input)
	keys := []string{"http-source-reversal"}
	for _, entry := range []struct {
		client *http.Client
		status int
	}{{f.guest, 401}, {f.buyerClient, 403}, {f.sellerClient, 403}} {
		call(entry.client, http.MethodGet, path, "", nil, entry.status)
		call(entry.client, http.MethodPost, path, string(raw), keys, entry.status)
	}
	f.exec(`UPDATE users SET role='admin' WHERE id=$1`, f.seller.ID)
	call(f.sellerClient, http.MethodPost, path, string(raw), keys, 403)
	for _, body := range []string{`{}`, `null`, string(raw) + ` {}`, strings.Replace(string(raw), `"confirmed":true`, `"confirmed":false`, 1), strings.TrimSuffix(string(raw), "}") + `,"amountCents":1}`, strings.Repeat(" ", 16385) + string(raw)} {
		call(f.financeClient, http.MethodPost, path, body, keys, 422)
	}
	for _, badKeys := range [][]string{nil, {"short"}, {"duplicate-key", "duplicate-key"}} {
		call(f.financeClient, http.MethodPost, path, string(raw), badKeys, 422)
	}
	for _, query := range []string{"?force=true", "?%zz", "?bad;query"} {
		call(f.financeClient, http.MethodGet, path+query, "", nil, 422)
		call(f.financeClient, http.MethodPost, path+query, string(raw), keys, 422)
	}
	changed := input
	changed.ExpectedUpdatedAt = changed.ExpectedUpdatedAt.Add(time.Microsecond)
	wrong, _ := json.Marshal(changed)
	call(f.financeClient, http.MethodPost, path, string(wrong), keys, 409)
	var first, again payments.SellerSourceReversalSubmission
	if err := json.Unmarshal(call(f.financeClient, http.MethodPost, path, string(raw), keys, 200), &first); err != nil {
		t.Fatal(err)
	}
	if first.Replayed || first.Command.ID == uuid.Nil || first.Command.AmountCents != f.payout.AmountCents {
		t.Fatal("queued command", first)
	}
	if err := json.Unmarshal(call(f.financeClient, http.MethodPost, path, string(raw), keys, 200), &again); err != nil || !again.Replayed || again.Command.ID != first.Command.ID {
		t.Fatal("replay", again, err)
	}
	call(f.financeClient, http.MethodPost, path, string(wrong), keys, 409)
	call(f.financeClient, http.MethodPost, path, string(raw), []string{"another-reversal-key"}, 409)
	view = read()
	if view.CanSubmit || view.CanClose || view.Command == nil || view.LatestRead != nil {
		t.Fatal("queue is not a returned source", view)
	}
	closePath := f.server.URL + "/api/v1/admin/seller-source-reversals/" + first.Command.ID.String() + "/close"
	closeInput := payments.SellerSourceClosureInput{ReadID: uuid.New(), ExpectedUpdatedAt: view.ExpectedUpdatedAt, Reason: "Consume only a fully verified original source return.", Confirmed: true}
	closeRaw, _ := json.Marshal(closeInput)
	closeKeys := []string{"http-source-close"}
	call(f.financeClient, http.MethodPost, closePath, string(closeRaw), closeKeys, 409)
	// Execute the stored job with a simulated provider, retaining real guards,
	// financial evidence and current authenticated HTTP sessions.
	runtime := &sourceReversalHTTPRuntime{bankHTTPSourceRuntime: f.runtime}
	service := payments.NewServiceWithRuntimes(f.pool, payments.ServiceConfig{Enabled: true}, payments.NewRuntimeCatalog(runtime))
	job := jobs.Job{ID: first.Command.JobID, Kind: payments.SellerSourceReversalJobKind}
	if err := f.pool.QueryRow(ctx, `SELECT payload FROM jobs WHERE id=$1`, job.ID).Scan(&job.Payload); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleSellerSourceReversalJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	view = read()
	if view.CanClose || view.AcceptedReadID == nil || view.CloseResolution == nil || *view.CloseResolution != "released" {
		t.Fatal("verified return", view)
	}
	closeInput.ReadID, closeInput.ExpectedUpdatedAt = *view.AcceptedReadID, view.ExpectedUpdatedAt
	closeRaw, _ = json.Marshal(closeInput)
	call(f.guest, http.MethodPost, closePath, string(closeRaw), closeKeys, 401)
	call(f.buyerClient, http.MethodPost, closePath, string(closeRaw), closeKeys, 403)
	call(f.sellerClient, http.MethodPost, closePath, string(closeRaw), closeKeys, 409)
	for _, body := range []string{`{}`, `null`, string(closeRaw) + ` {}`, strings.Replace(string(closeRaw), `"confirmed":true`, `"confirmed":false`, 1), strings.TrimSuffix(string(closeRaw), "}") + `,"resolution":"released"}`, strings.Repeat(" ", 16385) + string(closeRaw)} {
		call(f.financeClient, http.MethodPost, closePath, body, closeKeys, 422)
	}
	for _, badKeys := range [][]string{nil, {"short"}, {"duplicate-key", "duplicate-key"}} {
		call(f.financeClient, http.MethodPost, closePath, string(closeRaw), badKeys, 422)
	}
	call(f.financeClient, http.MethodPost, closePath+"?%zz", string(closeRaw), closeKeys, 422)
	call(f.financeClient, http.MethodPost, closePath, string(closeRaw), closeKeys, 409)
	checkerClient := testHTTPClient(t)
	checker := registerGovernanceUser(t, checkerClient, f.server.URL, "source_close_checker")
	f.exec(`UPDATE users SET role='admin' WHERE id=$1`, checker.ID)
	var closed, replayed payments.SellerSourceClosureSubmission
	if err := json.Unmarshal(call(checkerClient, http.MethodPost, closePath, string(closeRaw), closeKeys, 200), &closed); err != nil || closed.Replayed || closed.Closure.Resolution != "released" {
		t.Fatal("closure", closed, err)
	}
	if err := json.Unmarshal(call(checkerClient, http.MethodPost, closePath, string(closeRaw), closeKeys, 200), &replayed); err != nil || !replayed.Replayed || replayed.Closure.ID != closed.Closure.ID {
		t.Fatal("closure replay", replayed, err)
	}
	call(f.financeClient, http.MethodPost, closePath, string(closeRaw), []string{"another-close-key"}, 409)
	view = read()
	if view.CanSubmit || view.CanClose || view.Closure == nil || view.Request.Status != "cancelled" {
		t.Fatal("closed operation", view)
	}
	var commands, releases, closures int
	if err := f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM seller_source_reversal_commands WHERE payout_request_id=$1),
 (SELECT count(*) FROM seller_ledger_entries WHERE payout_request_id=$1 AND entry_type='payout_release'),
 (SELECT count(*) FROM seller_source_reversal_closures WHERE payout_request_id=$1)`, f.payout.ID).Scan(&commands, &releases, &closures); err != nil || commands != 1 || releases != 1 || closures != 1 {
		t.Fatal("duplicate financial effect", commands, releases, closures, err)
	}
	f.exec(`UPDATE users SET role='member' WHERE id=$1`, f.finance.ID)
	call(f.financeClient, http.MethodGet, path, "", nil, 403)
	call(f.financeClient, http.MethodPost, path, string(raw), keys, 403)
	call(f.financeClient, http.MethodPost, closePath, string(closeRaw), closeKeys, 403)
	if f.providerCalls.Load() != 0 || runtime.creates != 1 {
		t.Fatal("HTTP query or confirmation sent funds", f.providerCalls.Load(), runtime.creates)
	}
}

func TestSourceDispositionHTTPContract(t *testing.T) {
	f := newSellerFundsHTTPFixture(t)
	call := func(client *http.Client, path, key string, input any, want int, out any) {
		t.Helper()
		res := requestPaymentJSON(t, client, http.MethodPost, path, key, input, out)
		if res.StatusCode != want {
			t.Fatalf("status=%d want=%d", res.StatusCode, want)
		}
	}
	var view payments.SellerSourceReversalOperation
	if res := requestJSON(t, f.financeClient, http.MethodGet, f.base+"/source-reversal", nil, &view); res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	in := payments.SellerSourceReversalInput{SourceTransferID: view.Request.Funding.TransferID, ExpectedUpdatedAt: view.ExpectedUpdatedAt, BankCommandID: view.Bank.CommandID, BankResultID: view.Bank.ResultID, Reason: "Return source with original bank obligation checked.", Confirmed: true}
	var command payments.SellerSourceReversalSubmission
	call(f.financeClient, f.base+"/source-reversal", "disposition-source", in, 200, &command)
	path := f.server.URL + "/api/v1/admin/seller-source-reversals/" + command.Command.ID.String() + "/dispositions"
	d := payments.SellerSourceDispositionInput{Phase: "propose", ExpectedUpdatedAt: view.ExpectedUpdatedAt, Reason: "Restrict to original Provider reads and preserve the reservation.", Confirmed: true}
	call(f.guest, path, "denied-disposition", d, 401, nil)
	call(f.buyerClient, path, "denied-disposition", d, 403, nil)
	for _, extra := range []string{"amountCents", "providerResult", "forceRelease"} {
		bad := map[string]any{"phase": "propose", "expectedUpdatedAt": d.ExpectedUpdatedAt, "reason": d.Reason, "confirmed": true, extra: 100}
		call(f.financeClient, path, "invalid-disposition", bad, 422, nil)
	}
	var proposal payments.SellerSourceDisposition
	call(f.financeClient, path, "source-proposal", d, 200, &proposal)
	d.Phase = "approve"
	d.ProposalID = &proposal.ID
	call(f.financeClient, path, "self-approve", d, 409, nil)
	checkerClient := testHTTPClient(t)
	checker := registerGovernanceUser(t, checkerClient, f.server.URL, "source_disp_checker")
	f.exec(`UPDATE users SET role='admin' WHERE id=$1`, checker.ID)
	var approval payments.SellerSourceDisposition
	call(checkerClient, path, "source-approval", d, 200, &approval)
	call(checkerClient, path, "source-approval", d, 200, &approval)
	if !approval.Replayed {
		t.Fatal("missing replay")
	}
	if res := requestJSON(t, checkerClient, http.MethodGet, f.base+"/source-reversal", nil, &view); res.StatusCode != 200 || view.Disposition == nil || view.Disposition.Phase != "approve" || view.CanClose || view.CanApproveDisposition {
		t.Fatal("unsafe projection", view, res.StatusCode)
	}
	raw, _ := json.Marshal(view)
	for _, private := range []string{"snapshot", "provider_identity", "dispatch_key"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("private projection", private)
		}
	}
	f.exec(`UPDATE users SET role='member' WHERE id=$1`, checker.ID)
	call(checkerClient, path, "source-approval", d, 403, nil)
	if f.providerCalls.Load() != 0 {
		t.Fatal("HTTP called provider")
	}
}
