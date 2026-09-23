package httpapi_test

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestSellerPayoutReviewHTTPBoundary(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	t.Cleanup(cleanup)
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	t.Cleanup(server.Close)
	base := "/api/v1/admin/seller-payout-requests"
	detail := base + "/" + uuid.NewString()
	valid := fmt.Sprintf(`{"expectedRevision":0,"settlementId":%q,"amountCents":100,"bankDestinationId":"ba_original","decision":"approved","reason":"Verified the original payment and bank.","sellerMessage":"Your reservation has been approved."}`, uuid.NewString())
	request := func(client *http.Client, method, path, body string, keys []string, want int) string {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
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
		raw, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != want || res.Header.Get("Cache-Control") != "private, no-store" {
			t.Fatalf("%s %s: %d want %d cache=%q body=%s", method, path, res.StatusCode, want, res.Header.Get("Cache-Control"), raw)
		}
		return string(raw)
	}
	guest, client := testHTTPClient(t), testHTTPClient(t)
	actor := registerGovernanceUser(t, client, server.URL, "payout_review_http")
	for _, route := range []struct{ method, path string }{{http.MethodGet, base}, {http.MethodGet, detail}, {http.MethodPost, detail + "/review"}} {
		request(guest, route.method, route.path, valid, []string{"http-review-key"}, 401)
		request(client, route.method, route.path, valid, []string{"http-review-key"}, 403)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE users SET role='admin' WHERE id=$1`, actor.ID); err != nil {
		t.Fatal(err)
	}
	if body := request(client, http.MethodGet, base, "", nil, 200); !strings.Contains(body, `"items":[]`) {
		t.Fatal("empty directory", body)
	}
	request(client, http.MethodGet, detail, "", nil, 404)
	request(client, http.MethodPost, detail+"/review", valid, []string{"http-review-key"}, 404)
	for _, query := range []string{"?limit=0", "?limit=51", "?limit=1&limit=2", "?cursor=", "?cursor=bad", "?cursor=" + uuid.NewString(), "?actorId=another"} {
		request(client, http.MethodGet, base+query, "", nil, 422)
	}
	for _, body := range []string{
		`{}`, `null`, valid + ` {}`, strings.Replace(valid, `"expectedRevision":0,`, "", 1),
		strings.Replace(valid, `"expectedRevision":0`, `"expectedRevision":null`, 1),
		strings.Replace(valid, `"expectedRevision":0`, `"expectedRevision":-1`, 1),
		strings.Replace(valid, `"expectedRevision":0`, `"expectedRevision":2147483647`, 1),
		strings.Replace(valid, `"amountCents":100`, `"amountCents":0`, 1),
		strings.Replace(valid, `"decision":"approved"`, `"decision":"paid"`, 1),
		strings.Replace(valid, `"bankDestinationId":"ba_original"`, `"bankDestinationId":""`, 1),
		strings.Replace(valid, `"reason":"Verified the original payment and bank."`, `"reason":"bad\u0000reason is rejected"`, 1),
		strings.Replace(valid, `,"sellerMessage":"Your reservation has been approved."`, "", 1),
		strings.Replace(valid, `"sellerMessage":"Your reservation has been approved."`, `"sellerMessage":"short"`, 1),
		strings.Replace(valid, `"sellerMessage":"Your reservation has been approved."`, `"sellerMessage":"bad\u0000public message"`, 1),
		strings.TrimSuffix(valid, "}") + `,"transferNow":true}`,
	} {
		request(client, http.MethodPost, detail+"/review", body, []string{"http-review-key"}, 422)
	}
	for _, keys := range [][]string{nil, {"short"}, {"two-review-keys", "two-review-keys"}} {
		request(client, http.MethodPost, detail+"/review", valid, keys, 422)
	}
	request(client, http.MethodPost, detail+"/review?dispatch=true", valid, []string{"http-review-key"}, 422)
	var reviews, transfers int
	if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM seller_payout_reviews),(SELECT count(*) FROM seller_payout_transfers)`).Scan(&reviews, &transfers); err != nil || reviews != 0 || transfers != 0 {
		t.Fatal("invalid requests wrote finance state", reviews, transfers, err)
	}
}
