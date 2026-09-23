package httpapi_test

import (
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

func TestSellerPayoutDetailHTTPPrivacyAndOwnership(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	t.Cleanup(cleanup)
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	t.Cleanup(server.Close)
	ownerClient, foreignClient, guest := testHTTPClient(t), testHTTPClient(t), testHTTPClient(t)
	owner := registerGovernanceUser(t, ownerClient, server.URL, "payout_detail_owner")
	registerGovernanceUser(t, foreignClient, server.URL, "payout_detail_foreign")
	id := uuid.New()
	// A historical unallocated request is still readable by its owner, but
	// cannot authorize a new review or reveal somebody else's evidence.
	if _, err := pool.Exec(t.Context(), `INSERT INTO seller_payout_requests(id,seller_id,amount_cents,currency,idempotency_key,status) VALUES($1,$2,100,'USD','historical-detail-key','requested')`, id, owner.ID); err != nil {
		t.Fatal(err)
	}
	check := func(client *http.Client, path string, want int) string {
		t.Helper()
		res, err := client.Get(server.URL + "/api/v1/seller/payout-requests/" + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != want || res.Header.Get("Cache-Control") != "private, no-store" {
			t.Fatalf("status=%d want=%d cache=%q body=%s", res.StatusCode, want, res.Header.Get("Cache-Control"), raw)
		}
		return string(raw)
	}
	check(guest, id.String(), 401)
	if body := check(ownerClient, id.String(), 200); !strings.Contains(body, id.String()) || strings.Contains(body, `"actorId"`) || strings.Contains(body, `"reason"`) {
		t.Fatal("unsafe owner detail", body)
	}
	check(foreignClient, id.String(), 404)
	check(ownerClient, uuid.NewString(), 404)
	check(ownerClient, id.String()+"?sellerId="+owner.ID.String(), 422)
}
