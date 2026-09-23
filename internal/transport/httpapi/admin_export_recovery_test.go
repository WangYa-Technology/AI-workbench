package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestAdminExportRecoveryHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	ctx := context.Background()
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	memberClient, adminClient := testHTTPClient(t), testHTTPClient(t)
	member := registerGovernanceUser(t, memberClient, server.URL, "export_http_owner")
	actor := registerGovernanceUser(t, adminClient, server.URL, "export_http_admin")
	if _, err := pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1`, actor.ID); err != nil {
		t.Fatal(err)
	}
	var request datarights.Request
	response := requestJSON(t, memberClient, http.MethodPost, server.URL+"/api/v1/account/data-rights", map[string]any{"requestType": "data_export", "identityConfirmation": member.Handle}, &request)
	if response.StatusCode != 201 {
		t.Fatal(response.StatusCode)
	}
	var job uuid.UUID
	if err := pool.QueryRow(ctx, `UPDATE jobs SET status='failed',attempts=5,last_error='private/path/must-not-leak',last_error_code='handler_failed' WHERE kind=$1 AND payload->>'requestId'=$2 RETURNING id`, datarights.ExportJobKind, request.ID.String()).Scan(&job); err != nil {
		t.Fatal(err)
	}
	path := server.URL + "/api/v1/admin/data-rights/export-jobs"
	retryPath := path + "/" + job.String() + "/retry"
	input := map[string]any{"expectedAttempts": 5, "reason": "The failure was inspected and the source was repaired.", "confirmed": true}
	for _, client := range []*http.Client{testHTTPClient(t), memberClient} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			target := path
			if method == http.MethodPost {
				target = retryPath
			}
			response = requestJSON(t, client, method, target, input, nil)
			if response.StatusCode != 401 && response.StatusCode != 403 {
				t.Fatal("unauthorized recovery", response.StatusCode)
			}
		}
	}
	var requests datarights.RequestPage
	response = requestJSON(t, memberClient, http.MethodGet, server.URL+"/api/v1/account/data-rights", nil, &requests)
	if response.StatusCode != 200 || len(requests.Items) != 1 || requests.Items[0].Status != "failed" {
		t.Fatal("owner sees stuck queued request", requests, response.StatusCode)
	}
	var page datarights.ExportJobPage
	response = requestJSON(t, adminClient, http.MethodGet, path, nil, &page)
	if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "private, no-store" || len(page.Items) != 1 || !page.Items[0].CanRetry {
		t.Fatal("queue", page, response.StatusCode)
	}
	encoded, _ := json.Marshal(page)
	if strings.Contains(string(encoded), "private/path") || strings.Contains(string(encoded), member.Email) {
		t.Fatal("private data in queue")
	}
	for _, query := range []string{"?limit=0", "?limit=51", "?limit=", "?limit=2&limit=3", "?status=invalid", "?cursor=broken", "?kind=account", "?kind=export&kind=expiry", "?status=all&status=failed", "?cursor=a&cursor=b"} {
		if response = requestJSON(t, adminClient, http.MethodGet, path+query, nil, nil); response.StatusCode != 422 {
			t.Fatal("bad filter", query, response.StatusCode)
		}
	}
	for _, bad := range []map[string]any{
		{"reason": "Reason is otherwise valid.", "confirmed": true},
		{"expectedAttempts": 5, "reason": "Reason is otherwise valid.", "confirmed": false},
		{"expectedAttempts": 5, "reason": "short", "confirmed": true},
	} {
		if response = requestJSON(t, adminClient, http.MethodPost, retryPath, bad, nil); response.StatusCode != 422 {
			t.Fatal("bad command accepted", response.StatusCode)
		}
	}
	var first, again datarights.ExportJob
	response = requestJSON(t, adminClient, http.MethodPost, retryPath, input, &first)
	if response.StatusCode != 201 || first.RetryOf == nil || *first.RetryOf != job || first.RequestID == nil || *first.RequestID != request.ID {
		t.Fatal("recovery", first, response.StatusCode)
	}
	response = requestJSON(t, adminClient, http.MethodPost, retryPath, input, &again)
	if response.StatusCode != 201 || again.ID != first.ID {
		t.Fatal("lost response replay", again, response.StatusCode)
	}
	input["reason"] = "A different reason must not change the recovery command."
	response = requestJSON(t, adminClient, http.MethodPost, retryPath, input, nil)
	if response.StatusCode != 409 {
		t.Fatal("changed replay", response.StatusCode)
	}
	response = requestJSON(t, adminClient, http.MethodPost, path+"/"+uuid.NewString()+"/retry", input, nil)
	if response.StatusCode != 404 {
		t.Fatal("missing job", response.StatusCode)
	}
}
