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

func TestSellerPayoutBankHTTPPrivateBoundary(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	target := server.URL + "/api/v1/seller/payout-requests/" + uuid.NewString() + "/bank-destination"
	guest := testHTTPClient(t)
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		req, err := http.NewRequest(method, target, strings.NewReader(`{"bankDestinationId":"ba_original"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := guest.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("guest %s: %d", method, resp.StatusCode)
		}
	}
	client := testHTTPClient(t)
	registerGovernanceUser(t, client, server.URL, "bank_selection_owner")
	for _, test := range []struct {
		method, body, query string
		status              int
	}{
		{http.MethodGet, "", "", 404},
		{http.MethodPut, `{"bankDestinationId":"ba_original"}`, "", 404},
		{http.MethodPut, `{}`, "", 422},
		{http.MethodPut, `{"bankDestinationId":"card_original"}`, "", 422},
		{http.MethodPut, `{"bankDestinationId":"ba_original","sellerId":"other"}`, "", 422},
		{http.MethodPut, `{"bankDestinationId":"ba_original","bankName":"Spoofed Bank","last4":"1234"}`, "", 422},
		{http.MethodPut, `{"bankDestinationId":"ba_original"} {}`, "", 422},
		{http.MethodPut, `{"bankDestinationId":"ba_original"}`, "?sellerId=other", 422},
		{http.MethodGet, "", "?sellerId=other", 422},
		{http.MethodPut, `{"bankDestinationId":"` + strings.Repeat("a", 5000) + `"}`, "", 422},
	} {
		req, err := http.NewRequest(test.method, target+test.query, strings.NewReader(test.body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != test.status || resp.Header.Get("Cache-Control") != "private, no-store" {
			t.Fatalf("%s %s status=%d body=%s", test.method, test.query, resp.StatusCode, body)
		}
	}
}
