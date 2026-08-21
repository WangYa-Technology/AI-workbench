package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/support"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestSupportCopyrightLifecyclePermissions(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	requesterClient := testHTTPClient(t)
	requester := registerGovernanceUser(t, requesterClient, server.URL, "support_requester")
	creatorClient := testHTTPClient(t)
	creator := registerGovernanceUser(t, creatorClient, server.URL, "support_creator")
	otherClient := testHTTPClient(t)
	registerGovernanceUser(t, otherClient, server.URL, "support_other")
	adminClient := testHTTPClient(t)
	administrator := registerGovernanceUser(t, adminClient, server.URL, "support_admin")
	ctx := context.Background()
	assetID, workID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code)
		VALUES($1,$2,'image','Copyright target','/media/support-target.jpg','image/jpeg','clean','demo','personal')`, assetID, creator.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO works(id,author_id,asset_id,title,summary,model_name,status,ai_disclosure,published_at)
		VALUES($1,$2,$3,'Copyright target work','Public target','Local Test','published','Deterministic test content.',now())`, workID, creator.ID, assetID); err != nil {
		t.Fatal(err)
	}

	var created support.Case
	response := requestJSON(t, requesterClient, http.MethodPost, server.URL+"/api/v1/support/cases", map[string]any{
		"category": "copyright", "subject": "Rights review for published work", "details": "The published work appears to reproduce material for which I control the rights.",
		"relatedResourceType": "work", "relatedResourceId": workID, "locale": "en-US", "claimantRelationship": "rights_holder",
		"rightsStatement": "I control the underlying visual material and request a bounded platform review.",
	}, &created)
	if response.StatusCode != http.StatusCreated || created.Status != "open" || created.Version != 1 || len(created.Messages) != 1 || len(created.Events) != 1 {
		t.Fatalf("create copyright case: status=%d case=%#v", response.StatusCode, created)
	}

	response = requestJSON(t, requesterClient, http.MethodPost, server.URL+"/api/v1/support/cases", map[string]any{
		"category": "billing", "subject": "Sensitive payment issue", "details": "My payment card number is 4242 4242 4242 4242 and should not be stored here.", "locale": "en-US",
	}, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("sensitive data was accepted: %d", response.StatusCode)
	}

	response = requestJSON(t, otherClient, http.MethodGet, server.URL+"/api/v1/support/cases/"+created.ID.String(), nil, nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("another account read a private case: %d", response.StatusCode)
	}
	response = requestJSON(t, requesterClient, http.MethodGet, server.URL+"/api/v1/admin/support/cases", nil, nil)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("regular account opened support operations: %d", response.StatusCode)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1`, administrator.ID); err != nil {
		t.Fatal(err)
	}

	var queue struct {
		Items []support.Case `json:"items"`
	}
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/support/cases?category=copyright&status=open", nil, &queue)
	if response.StatusCode != http.StatusOK || len(queue.Items) != 1 || queue.Items[0].ID != created.ID {
		t.Fatalf("support operations queue: status=%d items=%#v", response.StatusCode, queue.Items)
	}

	var current support.Case
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/support/cases/"+created.ID.String()+"/messages", map[string]any{
		"body": "We received the report and are reviewing the referenced publication.", "expectedVersion": 1}, &current)
	if response.StatusCode != http.StatusOK || current.Status != "in_review" || current.Version != 2 || len(current.Messages) != 2 {
		t.Fatalf("operator reply: status=%d case=%#v", response.StatusCode, current)
	}
	response = requestJSON(t, adminClient, http.MethodPatch, server.URL+"/api/v1/admin/support/cases/"+created.ID.String(), map[string]any{
		"status": "waiting_for_requester", "resolutionCode": "", "expectedVersion": 1}, nil)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("stale support mutation was accepted: %d", response.StatusCode)
	}
	response = requestJSON(t, adminClient, http.MethodPatch, server.URL+"/api/v1/admin/support/cases/"+created.ID.String(), map[string]any{
		"status": "waiting_for_requester", "resolutionCode": "", "expectedVersion": 2}, &current)
	if response.StatusCode != http.StatusOK || current.Status != "waiting_for_requester" || current.Version != 3 {
		t.Fatalf("request more information: status=%d case=%#v", response.StatusCode, current)
	}
	response = requestJSON(t, requesterClient, http.MethodPost, server.URL+"/api/v1/support/cases/"+created.ID.String()+"/messages", map[string]any{
		"body": "The underlying source was first published in my private catalog in January 2025.", "expectedVersion": 3,
	}, &current)
	if response.StatusCode != http.StatusOK || current.Status != "in_review" || current.Version != 4 || len(current.Messages) != 3 {
		t.Fatalf("requester reply: status=%d case=%#v", response.StatusCode, current)
	}
	response = requestJSON(t, adminClient, http.MethodPatch, server.URL+"/api/v1/admin/support/cases/"+created.ID.String(), map[string]any{
		"status": "resolved", "resolutionCode": "content_restricted", "expectedVersion": 4}, &current)
	if response.StatusCode != http.StatusOK || current.Status != "resolved" || current.Version != 5 || current.ResolvedAt == nil || current.ResolutionCode == nil {
		t.Fatalf("resolve support case: status=%d case=%#v", response.StatusCode, current)
	}
	response = requestJSON(t, requesterClient, http.MethodPost, server.URL+"/api/v1/support/cases/"+created.ID.String()+"/messages", map[string]any{"body": "Late reply", "expectedVersion": 5}, nil)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("resolved case accepted requester reply: %d", response.StatusCode)
	}

	var notifications int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE user_id=$1 AND kind='support.case_updated' AND resource_id=$2`, requester.ID, created.ID).Scan(&notifications); err != nil {
		t.Fatal(err)
	}
	if notifications != 3 {
		t.Fatalf("missing support notifications: notifications=%d", notifications)
	}
	if _, err := pool.Exec(ctx, `UPDATE support_messages SET body='tampered' WHERE case_id=$1`, created.ID); err == nil {
		t.Fatal("append-only support message was mutable")
	}
}

func TestSupportOwnerHistoryHTTPPaginationAndIsolation(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	ownerClient := testHTTPClient(t)
	owner := registerGovernanceUser(t, ownerClient, server.URL, "support_history")
	otherClient := testHTTPClient(t)
	other := registerGovernanceUser(t, otherClient, server.URL, "support_history_other")
	ctx := context.Background()
	for index := 0; index < 56; index++ {
		if _, err := pool.Exec(ctx, `INSERT INTO support_cases(requester_id,category,subject,details,locale,updated_at) VALUES($1,'general_support',$2,$3,'en-US',now()-($4::int * interval '1 second'))`, owner.ID, "Owned history "+uuid.NewString(), "Private owned support history pagination evidence.", index); err != nil {
			t.Fatal(err)
		}
	}
	for index := 0; index < 3; index++ {
		if _, err := pool.Exec(ctx, `INSERT INTO support_cases(requester_id,category,subject,details,locale) VALUES($1,'account',$2,$3,'en-US')`, other.ID, "Isolated history "+uuid.NewString(), "Another account private support evidence."); err != nil {
			t.Fatal(err)
		}
	}

	seen := map[uuid.UUID]bool{}
	cursor := ""
	for pageNumber := 0; ; pageNumber++ {
		var page support.Page
		endpoint := server.URL + "/api/v1/support/cases?limit=25"
		if cursor != "" {
			endpoint += "&cursor=" + cursor
		}
		response := requestJSON(t, ownerClient, http.MethodGet, endpoint, nil, &page)
		if response.StatusCode != http.StatusOK || len(page.Items) == 0 || len(page.Items) > 25 {
			t.Fatalf("owned support page %d mismatch: status=%d items=%d", pageNumber, response.StatusCode, len(page.Items))
		}
		for _, item := range page.Items {
			if item.RequesterID != owner.ID || seen[item.ID] {
				t.Fatalf("owned support isolation or duplicate failure: %#v", item)
			}
			seen[item.ID] = true
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(seen) != 56 {
		t.Fatalf("owned support traversal returned %d of 56 cases", len(seen))
	}
	response := requestJSON(t, ownerClient, http.MethodGet, server.URL+"/api/v1/support/cases?cursor="+cursor+"modified", nil, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("modified owner support cursor was accepted: %d", response.StatusCode)
	}
	response = requestJSON(t, ownerClient, http.MethodGet, server.URL+"/api/v1/support/cases?limit=51", nil, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("oversized owner support page was accepted: %d", response.StatusCode)
	}
	var isolated support.Page
	response = requestJSON(t, otherClient, http.MethodGet, server.URL+"/api/v1/support/cases?limit=25", nil, &isolated)
	if response.StatusCode != http.StatusOK || len(isolated.Items) != 3 {
		t.Fatalf("other owner support projection mismatch: status=%d items=%d", response.StatusCode, len(isolated.Items))
	}
}
