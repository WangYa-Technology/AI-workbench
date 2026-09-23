package httpapi_test

import (
	"encoding/json"
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

func TestFinanceAdjustmentHTTPIdempotency(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	t.Cleanup(cleanup)
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	t.Cleanup(server.Close)
	client := testHTTPClient(t)
	actor := registerGovernanceUser(t, client, server.URL, "adjustment_http")
	if _, err := pool.Exec(t.Context(), `UPDATE users SET role='admin' WHERE id=$1`, actor.ID); err != nil {
		t.Fatal(err)
	}
	var before int64
	if err := pool.QueryRow(t.Context(), `SELECT balance_cents FROM billing_accounts WHERE user_id=$1 AND currency='USD'`, actor.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	path := server.URL + "/api/v1/admin/finance/accounts/" + actor.ID.String() + "/adjust"
	type result struct {
		OperationID  uuid.UUID `json:"operationId"`
		Replayed     bool      `json:"replayed"`
		BalanceCents int64     `json:"balanceCents"`
	}
	post := func(key, body string, status int) result {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != status {
			t.Fatalf("status=%d want=%d body=%s", response.StatusCode, status, raw)
		}
		var value result
		if status == 200 {
			if err := json.Unmarshal(raw, &value); err != nil {
				t.Fatal(err)
			}
		}
		return value
	}
	key := uuid.NewString()
	first := post(key, `{"deltaCents":500,"currency":"USD"}`, 200)
	replay := post(key, `{"deltaCents":500,"currency":"USD"}`, 200)
	if first.BalanceCents != before+500 || replay.BalanceCents != before+500 || first.OperationID == uuid.Nil || first.OperationID != replay.OperationID || first.Replayed || !replay.Replayed {
		t.Fatalf("duplicate command changed balance or identity: first=%+v replay=%+v before=%d", first, replay, before)
	}
	post(key, `{"deltaCents":501,"currency":"USD"}`, 409)
	post("", `{"deltaCents":500,"currency":"USD"}`, 422)
	post("bad key", `{"deltaCents":500,"currency":"USD"}`, 422)
	if _, err := pool.Exec(t.Context(), `UPDATE users SET role='member' WHERE id=$1`, actor.ID); err != nil {
		t.Fatal(err)
	}
	post(key, `{"deltaCents":500,"currency":"USD"}`, 403)
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM billing_entries WHERE user_id=$1 AND entry_type='admin_adjustment'`, actor.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate ledger: %d %v", count, err)
	}
}
