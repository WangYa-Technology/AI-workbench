package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/billing"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestBillingStatementHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	ownerClient := testHTTPClient(t)
	owner := registerGovernanceUser(t, ownerClient, server.URL, "billowner")
	otherClient := testHTTPClient(t)
	other := registerGovernanceUser(t, otherClient, server.URL, "billother")
	ctx := context.Background()
	base := time.Date(2026, 8, 11, 4, 0, 0, 0, time.UTC)
	newestID, olderID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO billing_entries(id,user_id,operation_id,entry_type,direction,amount_cents,currency,balance_after_cents,description,created_at) VALUES
		($1,$2,$3,'generation_charge','debit',5,'USD',9995,'HTTP generation charge',$4),
		($5,$2,$6,'product_refund','credit',20,'USD',10015,'HTTP product refund',$7),
		($8,$9,$10,'product_sale','credit',50,'USD',10050,'HTTP foreign entry',$11)`,
		newestID, owner.ID, uuid.New(), base.Add(time.Minute), olderID, uuid.New(), base, uuid.New(), other.ID, uuid.New(), base.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}

	var first billing.Statement
	response := requestJSON(t, ownerClient, http.MethodGet, server.URL+"/api/v1/billing/statement?limit=1", nil, &first)
	if response.StatusCode != http.StatusOK || len(first.Entries) != 1 || first.Entries[0].ID != newestID || first.NextCursor == nil {
		t.Fatalf("first billing page: status=%d statement=%#v", response.StatusCode, first)
	}
	var second billing.Statement
	response = requestJSON(t, ownerClient, http.MethodGet, server.URL+"/api/v1/billing/statement?limit=1&cursor="+url.QueryEscape(*first.NextCursor), nil, &second)
	if response.StatusCode != http.StatusOK || len(second.Entries) != 1 || second.Entries[0].ID != olderID {
		t.Fatalf("second billing page: status=%d statement=%#v", response.StatusCode, second)
	}
	var filtered billing.Statement
	response = requestJSON(t, ownerClient, http.MethodGet, server.URL+"/api/v1/billing/statement?direction=credit&entryType=product_refund", nil, &filtered)
	if response.StatusCode != http.StatusOK || len(filtered.Entries) != 1 || filtered.Entries[0].ID != olderID {
		t.Fatalf("filtered billing statement: status=%d statement=%#v", response.StatusCode, filtered)
	}
	var otherStatement billing.Statement
	response = requestJSON(t, otherClient, http.MethodGet, server.URL+"/api/v1/billing/statement", nil, &otherStatement)
	if response.StatusCode != http.StatusOK || len(otherStatement.Entries) != 1 || otherStatement.Entries[0].Description != "HTTP foreign entry" {
		t.Fatalf("cross-account billing isolation: status=%d statement=%#v", response.StatusCode, otherStatement)
	}
	response = requestJSON(t, testHTTPClient(t), http.MethodGet, server.URL+"/api/v1/billing/statement", nil, nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous billing statement access: %d", response.StatusCode)
	}

	invalidQueries := []string{
		"direction=sideways", "entryType=unknown", "limit=51", "limit=invalid", "cursor=modified",
		"dateFrom=2026-08-12T00%3A00%3A00Z&dateTo=2026-08-11T00%3A00%3A00Z", "dateFrom=not-a-date",
	}
	for _, query := range invalidQueries {
		response = requestJSON(t, ownerClient, http.MethodGet, server.URL+"/api/v1/billing/statement?"+query, nil, nil)
		if response.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("invalid billing query accepted: %s status=%d", query, response.StatusCode)
		}
	}
}
