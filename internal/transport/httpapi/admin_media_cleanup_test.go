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

func TestAdminMediaCleanupHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	ctx := context.Background()
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	memberClient, adminClient := testHTTPClient(t), testHTTPClient(t)
	member := registerGovernanceUser(t, memberClient, server.URL, "cleanup_http_member")
	actor := registerGovernanceUser(t, adminClient, server.URL, "cleanup_http_admin")
	if _, err := pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1`, actor.ID); err != nil {
		t.Fatal(err)
	}
	owner := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,status) VALUES($1,'cleanup_http_deleted@test.local','cleanup_http_deleted','Deleted account','deleted')`, owner); err != nil {
		t.Fatal(err)
	}
	var jobID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO jobs(kind,payload,status,attempts,max_attempts,last_error,last_error_code) VALUES($1,jsonb_build_object('userId',$2::text,'storageKey','private/never-expose'),'failed',20,20,'private/never-expose','handler_failed') RETURNING id`, datarights.MediaCleanupJobKind, owner).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	path := server.URL + "/api/v1/admin/data-rights/media-cleanups"
	retryPath := path + "/" + jobID.String() + "/retry"
	input := map[string]any{"expectedAttempts": 20, "confirmed": true, "reason": "Verified that the storage access issue has been fixed."}
	if response := requestJSON(t, memberClient, http.MethodGet, path, nil, nil); response.StatusCode != 403 {
		t.Fatal("member listed cleanup jobs", response.StatusCode)
	}
	if response := requestJSON(t, memberClient, http.MethodPost, retryPath, input, nil); response.StatusCode != 403 {
		t.Fatal("member retried cleanup", response.StatusCode)
	}
	// A finance-only role cannot perform privacy cleanup actions.
	if _, err := pool.Exec(ctx, `INSERT INTO role_permissions(role,permission_id) VALUES('creator','admin:finance') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET role='creator' WHERE id=$1`, member.ID); err != nil {
		t.Fatal(err)
	}
	if response := requestJSON(t, memberClient, http.MethodPost, retryPath, input, nil); response.StatusCode != 403 {
		t.Fatal("finance role retried privacy cleanup", response.StatusCode)
	}
	var page datarights.MediaCleanupPage
	response := requestJSON(t, adminClient, http.MethodGet, path, nil, &page)
	if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "private, no-store" || len(page.Items) != 1 || !page.Items[0].CanRetry {
		t.Fatalf("queue projection: %d %#v", response.StatusCode, page)
	}
	encoded, _ := json.Marshal(page)
	if strings.Contains(string(encoded), "private/") {
		t.Fatal("private storage details leaked")
	}
	for _, query := range []string{"?limit=0", "?limit=51", "?limit=", "?limit=-1", "?limit=abc", "?limit=20&limit=30", "?status=unknown", "?cursor=broken", "?kind=unknown", "?kind=product&kind=account", "?status=all&status=failed", "?cursor=a&cursor=b"} {
		if response := requestJSON(t, adminClient, http.MethodGet, path+query, nil, nil); response.StatusCode != 422 {
			t.Fatal("bad filter accepted", query, response.StatusCode)
		}
	}
	if response := requestJSON(t, adminClient, http.MethodPost, retryPath, map[string]any{"confirmed": true, "reason": input["reason"]}, nil); response.StatusCode != 422 {
		t.Fatal("missing attempts accepted", response.StatusCode)
	}
	if response := requestJSON(t, adminClient, http.MethodPost, retryPath, map[string]any{"confirmed": true, "expectedAttempts": 19, "reason": input["reason"]}, nil); response.StatusCode != 409 {
		t.Fatal("stale attempts accepted", response.StatusCode)
	}
	if response := requestJSON(t, adminClient, http.MethodPost, path+"/"+uuid.NewString()+"/retry", input, nil); response.StatusCode != 404 {
		t.Fatal("unknown job accepted", response.StatusCode)
	}
	var next datarights.MediaCleanup
	response = requestJSON(t, adminClient, http.MethodPost, retryPath, input, &next)
	if response.StatusCode != 201 || next.ID == jobID || next.Status != "queued" || next.RetryOf == nil || *next.RetryOf != jobID {
		t.Fatalf("missing replacement: %d %#v", response.StatusCode, next)
	}
	if response := requestJSON(t, adminClient, http.MethodPost, retryPath, input, nil); response.StatusCode != 409 {
		t.Fatal("double retry accepted", response.StatusCode)
	}
	response = requestJSON(t, adminClient, http.MethodGet, path, nil, &page)
	if response.StatusCode != 200 || page.Items[0].CanRetry || page.Items[0].RetryJobID == nil || *page.Items[0].RetryJobID != next.ID {
		t.Fatal("original failure lost or still actionable", page)
	}
	var queued, attempts int
	var state string
	if err := pool.QueryRow(ctx, `SELECT status,attempts,(SELECT count(*) FROM jobs WHERE kind=$2 AND status='queued') FROM jobs WHERE id=$1`, jobID, datarights.MediaCleanupJobKind).Scan(&state, &attempts, &queued); err != nil || state != "failed" || attempts != 20 || queued != 1 {
		t.Fatal("request reset original evidence or performed cleanup", err, state, attempts, queued)
	}
}
