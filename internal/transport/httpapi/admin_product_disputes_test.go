package httpapi_test

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestProductPaymentDisputeHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	t.Cleanup(cleanup)
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	t.Cleanup(server.Close)
	guest, client := testHTTPClient(t), testHTTPClient(t)
	actor := registerGovernanceUser(t, client, server.URL, "dispute_http")
	base := "/api/v1/admin/product-disputes"
	id := uuid.New()
	detail := base + "/" + id.String()
	valid := `{"action":"route","route":"finance","expectedVersion":1,"reason":"Reviewed original provider evidence.","confirmed":true}`
	request := func(c *http.Client, method, path, body string, keys []string, want int) []byte {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		for _, key := range keys {
			req.Header.Add("Idempotency-Key", key)
		}
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != want || res.Header.Get("Cache-Control") != "private, no-store" {
			t.Fatalf("%s %s: got %d want %d cache=%q body=%s", method, path, res.StatusCode, want, res.Header.Get("Cache-Control"), raw)
		}
		return raw
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, role := range []string{"member", "moderator"} {
		exec(`UPDATE users SET role=$2 WHERE id=$1`, actor.ID, role)
		for _, path := range []string{base, detail, detail + "/operations"} {
			method := http.MethodGet
			if strings.HasSuffix(path, "/operations") {
				method = http.MethodPost
			}
			request(guest, method, path, valid, []string{"http-dispute-key"}, 401)
			request(client, method, path, valid, []string{"http-dispute-key"}, 403)
		}
	}
	exec(`UPDATE users SET role='admin' WHERE id=$1`, actor.ID)
	request(client, "GET", base, "", nil, 200)
	request(client, "GET", detail, "", nil, 404)
	request(client, "POST", detail+"/operations", valid, []string{"http-dispute-key"}, 404)
	for _, query := range []string{"?limit=0", "?limit=51", "?limit=1&limit=2", "?cursor=bad", "?mode=live&mode=test", "?actorId=other", "?binding=other", "?actionStatus=paid", "?reviewStatus=paid"} {
		request(client, "GET", base+query, "", nil, 422)
	}
	request(client, "GET", detail+"?extra=true", "", nil, 422)
	request(client, "POST", detail+"/operations?extra=true", valid, []string{"http-dispute-key"}, 422)
	for _, keys := range [][]string{nil, {"short"}, {"http-dispute-key", "http-dispute-key"}, {"bad key value"}} {
		request(client, "POST", detail+"/operations", valid, keys, 422)
	}
	for _, body := range []string{`{}`, `null`, strings.Replace(valid, `"expectedVersion":1`, `"expectedVersion":0`, 1), strings.Replace(valid, `"confirmed":true`, `"confirmed":false`, 1), strings.Replace(valid, `Reviewed original provider evidence.`, `short`, 1), strings.Replace(valid, `Reviewed original provider evidence.`, `bad\u0000reason is rejected`, 1), strings.Replace(valid, `"action":"route"`, `"action":"escalate_recovery"`, 1), strings.TrimSuffix(valid, "}") + `,"evidenceReference":"file_unexpected"}`} {
		request(client, "POST", detail+"/operations", body, []string{"http-dispute-key"}, 422)
	}
	request(client, "POST", detail+"/operations", strings.TrimSuffix(valid, "}")+`,"sendFunds":true}`, []string{"http-dispute-key"}, 400)
	// No Provider calls: fixtures contain only isolated provider event/dispute rows.
	for i, disputeID := range []uuid.UUID{id, uuid.New()} {
		eventID := uuid.New()
		providerDisputeID := "dp_" + strings.ReplaceAll(disputeID.String(), "-", "")
		exec(`INSERT INTO payment_provider_events(id,provider,provider_event_id,event_type,api_version,live_mode,occurred_at,payload_sha256,object_id,object_type,purpose)
   VALUES($1,'stripe',$2,'charge.dispute.created','2026-02-25.clover',false,now(),repeat('a',64),$3,'dispute','product')`, eventID, "evt_"+eventID.String(), providerDisputeID)
		exec(`INSERT INTO product_payment_disputes(id,provider,live_mode,provider_dispute_id,provider_payment_id,provider_charge_id,amount_cents,currency,provider_status,action_status,due_by,latest_event_at,latest_event_id)
	   VALUES($1,'stripe',false,$2,$3,$4,1900,'USD','needs_response','needs_response',now()+make_interval(days=>$6),now(),$5)`, disputeID, providerDisputeID, "pi_private", "ch_private", eventID, i+1)
	}
	var first, second admin.ProductPaymentDisputePage
	if err := json.Unmarshal(request(client, "GET", base+"?limit=1", "", nil, 200), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.NextCursor == nil {
		t.Fatalf("first page: %#v", first)
	}
	request(client, "GET", base+"?mode=live&cursor="+url.QueryEscape(*first.NextCursor), "", nil, 422)
	if err := json.Unmarshal(request(client, "GET", base+"?limit=1&cursor="+url.QueryEscape(*first.NextCursor), "", nil, 200), &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.NextCursor != nil || first.Items[0].ID == second.Items[0].ID {
		t.Fatalf("second page: %#v", second)
	}
	var result admin.ProductPaymentDisputeCommandResult
	operate := func(body, key string) admin.ProductPaymentDisputeCommandResult {
		t.Helper()
		var v admin.ProductPaymentDisputeCommandResult
		if err := json.Unmarshal(request(client, "POST", detail+"/operations", body, []string{key}, 200), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	result = operate(valid, "http-dispute-key")
	if result.Version != 2 || len(result.Operations) != 1 || result.Replayed {
		t.Fatalf("operation: %#v", result)
	}
	replay := operate(valid, "http-dispute-key")
	if !replay.Replayed || replay.OperationID != result.OperationID {
		t.Fatal("replay mismatch")
	}
	request(client, "POST", detail+"/operations", strings.Replace(valid, "Reviewed original", "Different original", 1), []string{"http-dispute-key"}, 409)
	request(client, "POST", detail+"/operations", valid, []string{"http-stale-key"}, 409)
	for n, action := range []string{"request_evidence", "record_evidence_submission", "escalate_recovery"} {
		command := admin.ProductPaymentDisputeCommand{Action: action, Route: "collections", ExpectedVersion: int64(n + 2), Reason: "Verified original provider evidence.", Confirmed: true}
		if action == "record_evidence_submission" {
			command.EvidenceReference = "provider://stripe/file_123"
		}
		body, _ := json.Marshal(command)
		result = operate(string(body), fmt.Sprintf("http-operation-%d", n))
	}
	if result.Version != 5 || len(result.Operations) != 4 || len(result.EvidenceSubmissions) != 1 || result.ProviderStatus != "needs_response" || result.ActionStatus != "needs_response" {
		t.Fatalf("lifecycle mismatch: %#v", result)
	}
	replay = operate(valid, "http-dispute-key")
	if !replay.Replayed || replay.Version != 5 {
		t.Fatal("replay did not return current state")
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE resource_id=$1 AND action='admin.product_payment_dispute_operated'`, id).Scan(&count); err != nil || count != 4 {
		t.Fatal("audit count", count, err)
	}
	// Revocation affects reads, new writes AND replay under the same session cookie.
	exec(`DELETE FROM role_permissions WHERE role='admin' AND permission_id='admin:finance'`)
	request(client, "GET", base, "", nil, 403)
	request(client, "GET", detail, "", nil, 403)
	request(client, "POST", detail+"/operations", valid, []string{"http-dispute-key"}, 403)
}
