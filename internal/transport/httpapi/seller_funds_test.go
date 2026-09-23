package httpapi_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestSellerFundsHTTPRequiresPrivateSessionAndFailsClosed(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	guest := testHTTPClient(t)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/seller/funds"},
		{http.MethodGet, "/api/v1/seller/payout-requests"},
		{http.MethodPost, "/api/v1/seller/payout-requests"},
		{http.MethodGet, "/api/v1/seller/payout-options"},
		{http.MethodGet, "/api/v1/seller/payout-requests/00000000-0000-4000-8000-000000000001/banks"},
	} {
		request, err := http.NewRequest(route.method, server.URL+route.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := guest.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("guest %s status=%d", route.path, response.StatusCode)
		}
	}
	client := testHTTPClient(t)
	owner := registerGovernanceUser(t, client, server.URL, "seller_funds_owner")
	var balance map[string]any
	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/seller/funds", nil, &balance)
	if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "private, no-store" || balance["unresolvedRecords"] != float64(0) {
		t.Fatalf("empty funds status=%d body=%v", response.StatusCode, balance)
	}
	accounts, ok := balance["accounts"].([]any)
	if !ok || len(accounts) != 0 || len(balance) != 4 {
		t.Fatalf("funds must be scoped without root totals: %v", balance)
	}
	// Unknown legacy evidence belongs only to this authenticated seller.
	if _, err := pool.Exec(t.Context(), `INSERT INTO seller_ledger_entries(seller_id,entry_type,amount_cents,currency,idempotency_key) VALUES($1,'adjustment',777,'USD','http-unclassified-funds')`, owner.ID); err != nil {
		t.Fatal(err)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/seller/funds", nil, &balance)
	if response.StatusCode != http.StatusOK || balance["unresolvedRecords"] != float64(1) || len(balance["accounts"].([]any)) != 0 {
		t.Fatalf("unclassified evidence pooled: %v", balance)
	}
	otherClient := testHTTPClient(t)
	other := registerGovernanceUser(t, otherClient, server.URL, "seller_funds_other")
	response = requestJSON(t, otherClient, http.MethodGet, server.URL+"/api/v1/seller/funds", nil, &balance)
	if response.StatusCode != http.StatusOK || balance["sellerId"] != other.ID.String() || balance["unresolvedRecords"] != float64(0) {
		t.Fatalf("foreign funds leak: %v", balance)
	}
	for _, query := range []string{"?sellerId=" + owner.ID.String(), "?provider=stripe", "?environment=live", "?currency=USD"} {
		var failure map[string]any
		response = requestJSON(t, otherClient, http.MethodGet, server.URL+"/api/v1/seller/funds"+query, nil, &failure)
		if response.StatusCode != http.StatusUnprocessableEntity || response.Header.Get("Cache-Control") != "private, no-store" {
			t.Fatalf("funds filter accepted: %s %d", query, response.StatusCode)
		}
	}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/seller/payout-requests", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Idempotency-Key", "seller-funds-invalid")
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid payout status=%d owner=%s", response.StatusCode, owner.ID)
	}
	request, err = http.NewRequest(http.MethodPost, server.URL+"/api/v1/seller/payout-requests", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Idempotency-Key", "seller-funds-empty")
	request.Header.Set("Content-Type", "application/json")
	request.Body = io.NopCloser(strings.NewReader(`{"amountCents":100}`))
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), "invalid_seller_payout_request") {
		t.Fatalf("missing selected settlement status=%d body=%s", response.StatusCode, body)
	}
	request, err = http.NewRequest(http.MethodPost, server.URL+"/api/v1/seller/payout-requests", strings.NewReader(`{"settlementId":"00000000-0000-4000-8000-000000000001","amountCents":100}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Idempotency-Key", "seller-funds-disabled")
	request.Header.Set("Content-Type", "application/json")
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(body), "payment_provider_unavailable") {
		t.Fatalf("disabled provider accepted request status=%d body=%s", response.StatusCode, body)
	}
	for _, path := range []string{"/api/v1/seller/payout-options", "/api/v1/seller/payout-requests"} {
		var page struct {
			Items []any `json:"items"`
		}
		response := requestJSON(t, client, http.MethodGet, server.URL+path, nil, &page)
		if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "private, no-store" || page.Items == nil || len(page.Items) != 0 {
			t.Fatalf("private directory %s: %d %+v", path, response.StatusCode, page)
		}
		for _, query := range []string{"?limit=0", "?limit=51", "?limit=abc", "?limit=1&limit=2", "?cursor=", "?cursor=bad", "?cursor=00000000-0000-4000-8000-000000000001", "?sellerId=another"} {
			var failure map[string]any
			response := requestJSON(t, client, http.MethodGet, server.URL+path+query, nil, &failure)
			if response.StatusCode != http.StatusUnprocessableEntity || response.Header.Get("Cache-Control") != "private, no-store" {
				t.Fatalf("invalid directory query %s%s: %d %+v", path, query, response.StatusCode, failure)
			}
		}
	}
}
