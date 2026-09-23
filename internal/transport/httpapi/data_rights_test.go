package httpapi_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestDataRightsHTTPContractAndPermissions(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	mediaRoot := t.TempDir()
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: mediaRoot, WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	client := testHTTPClient(t)
	owner := registerGovernanceUser(t, client, server.URL, "rights_owner")

	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/account/data-rights", map[string]any{"requestType": "data_export", "identityConfirmation": "wrong_handle"}, nil)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("mismatched handle was accepted: %d", response.StatusCode)
	}
	var exportRequest datarights.Request
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/account/data-rights", map[string]any{"requestType": "data_export", "identityConfirmation": owner.Handle}, &exportRequest)
	if response.StatusCode != http.StatusCreated || exportRequest.Status != "queued" {
		t.Fatalf("create export request: status=%d item=%#v", response.StatusCode, exportRequest)
	}
	// Many ordinary owner records make the HTTP package exceed one storage part.
	if _, err := pool.Exec(context.Background(), `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id)
 SELECT $1,'export.volume_test','user',$1,repeat('owner evidence ',1000),'export-test-'||n FROM generate_series(1,450) n`, owner.ID); err != nil {
		t.Fatal(err)
	}
	jobRepository := jobs.NewRepository(pool)
	job := claimHTTPJobKind(t, context.Background(), pool, "http-data-rights", datarights.ExportJobKind)
	service := datarights.NewService(pool, mediaRoot)
	if err := service.HandleExportJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if err := jobRepository.Complete(context.Background(), job, "http-data-rights"); err != nil {
		t.Fatal(err)
	}
	downloadRequest, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/account/data-rights/"+exportRequest.ID.String()+"/export", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err = client.Do(downloadRequest)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || response.Header.Get("Digest") == "" || strings.Contains(strings.ToLower(string(body)), "token_hash") {
		t.Fatalf("private export response invalid: status=%d digest=%q body=%s err=%v", response.StatusCode, response.Header.Get("Digest"), body, err)
	}
	if len(body) <= 5<<20 || response.ContentLength != int64(len(body)) {
		t.Fatal("large HTTP export truncated or missing exact content length")
	}
	sum := sha256.Sum256(body)
	encodedDigest := base64.StdEncoding.EncodeToString(sum[:])
	if response.Header.Get("Digest") != "sha-256="+encodedDigest || response.Header.Get("Content-Digest") != "sha-256=:"+encodedDigest+":" {
		t.Fatal("download digest does not describe the actual bytes using standard Base64")
	}
	var exported struct {
		Data struct {
			Marketplace struct {
				SchemaVersion int                        `json:"schemaVersion"`
				Data          map[string]json.RawMessage `json:"data"`
			} `json:"marketplace"`
		} `json:"data"`
	}
	sections := []string{
		"checkoutCheckDispatches", "checkoutClosures", "checkoutCommands", "checkoutDispatches",
		"checkoutLookups", "checkoutRequests", "cleanupJobs", "closedCheckoutRecoveries",
		"closedCheckoutRefundConfirmations", "contracts", "deliveryRepairs", "deliverySnapshots",
		"entitlements", "identityRecoveries", "listingHistory", "orderEvents", "paymentEvents",
		"payments", "products", "providerEvents", "refundAttempts", "refundChecks", "refundReadReceipts",
		"rejectedProviderEventChecks", "rejectedProviderEvents", "sales",
		"sellerSettlements", "sellerLedger", "sellerRecoveryObligations", "sellerPayoutRequests",
		"sellerPayoutAllocations", "sellerPayoutBankTargets", "sellerPayoutReviews",
		"sellerPayoutFundingAdmissions", "sellerPayoutEvents", "sellerPayoutSourceTransfers",
		"sellerSourceReversalCommands", "sellerSourceReversalReads", "sellerSourceReversalResults", "sellerSourceReversalClosures",
		"sellerBankPayoutCommands", "sellerBankPayoutResults", "sellerBankPayoutReads", "sellerBankPayoutResumes",
		"sellerFundsReconciliation", "sellerFundsAccounts", "sellerFundsUnresolvedRecords",
	}
	if err := json.Unmarshal(body, &exported); err != nil || exported.Data.Marketplace.SchemaVersion != 1 || len(exported.Data.Marketplace.Data) != len(sections) || response.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatal("private marketplace export contract missing", err)
	}
	for _, name := range sections {
		rows, ok := exported.Data.Marketplace.Data[name]
		if !ok {
			t.Fatalf("missing public export array %s", name)
		}
		if name == "sellerFundsReconciliation" {
			var records []map[string]any
			if err := json.Unmarshal(rows, &records); err != nil || len(records) != 1 || len(records[0]) != 3 || records[0]["version"] != float64(1) || records[0]["unresolvedRecords"] != float64(0) {
				t.Fatal("empty owner financial report exposed unexpected metadata", string(rows), err)
			}
			if _, err := time.Parse(time.RFC3339Nano, records[0]["asOf"].(string)); err != nil {
				t.Fatal("financial snapshot timestamp missing", err)
			}
			continue
		}
		if string(rows) != "[]" {
			t.Fatalf("new user received unrelated marketplace %s", name)
		}
	}

	// A real server's ordinary API write deadline must not cut off the large
	// download while its verified private spool is being prepared.
	shortServer := httptest.NewUnstartedServer(server.Config.Handler)
	shortServer.Config.WriteTimeout = time.Millisecond
	shortServer.Start()
	defer shortServer.Close()
	shortResponse, err := client.Get(shortServer.URL + "/api/v1/account/data-rights/" + exportRequest.ID.String() + "/export")
	if err != nil {
		t.Fatal("large export retained ordinary write deadline", err)
	}
	shortBody, err := io.ReadAll(shortResponse.Body)
	shortResponse.Body.Close()
	if err != nil || shortResponse.StatusCode != http.StatusOK || string(shortBody) != string(body) {
		t.Fatal("deadline extension changed or truncated download", err)
	}
	firstFile, _, err := service.OpenExport(context.Background(), owner.ID, exportRequest.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer firstFile.Close()
	secondFile, _, err := service.OpenExport(context.Background(), owner.ID, exportRequest.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer secondFile.Close()
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/account/data-rights/"+exportRequest.ID.String()+"/export", nil, nil)
	if response.StatusCode != http.StatusServiceUnavailable || response.Header.Get("Retry-After") != "5" {
		t.Fatal("busy download did not fail before streaming", response.StatusCode)
	}
	firstFile.Close()
	secondFile.Close()
	response = requestJSON(t, testHTTPClient(t), http.MethodGet, server.URL+"/api/v1/account/data-rights/"+exportRequest.ID.String()+"/export", nil, nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatal("anonymous download", response.StatusCode)
	}
	otherClient := testHTTPClient(t)
	registerGovernanceUser(t, otherClient, server.URL, "rights_other")
	response = requestJSON(t, otherClient, http.MethodGet, server.URL+"/api/v1/account/data-rights/"+exportRequest.ID.String()+"/export", nil, nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("another account downloaded the export: %d", response.StatusCode)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/admin/data-rights", nil, nil)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("regular account opened Admin data rights: %d", response.StatusCode)
	}

	var deletionRequest datarights.Request
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/account/data-rights", map[string]any{"requestType": "account_deletion", "identityConfirmation": owner.Handle}, &deletionRequest)
	if response.StatusCode != http.StatusCreated || deletionRequest.Status != "scheduled" {
		t.Fatalf("schedule deletion: status=%d item=%#v", response.StatusCode, deletionRequest)
	}
	adminClient := testHTTPClient(t)
	administrator := registerGovernanceUser(t, adminClient, server.URL, "rights_admin")
	if _, err := pool.Exec(context.Background(), `UPDATE users SET role='admin' WHERE id=$1`, administrator.ID); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"/account/data-rights", "/admin/data-rights", "/admin/data-rights/holds", "/admin/data-rights/media-cleanups"} {
		for _, query := range []string{"?limit=", "?limit=0", "?limit=-1", "?limit=51", "?limit=1&limit=50"} {
			response := requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1"+endpoint+query, nil, nil)
			if response.StatusCode != http.StatusUnprocessableEntity {
				t.Fatal("invalid data-rights pagination accepted", endpoint, query, response.StatusCode)
			}
		}
		for _, query := range []string{"", "?limit=1", "?limit=50"} {
			response := requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1"+endpoint+query, nil, nil)
			if response.StatusCode != http.StatusOK {
				t.Fatal("valid data-rights pagination rejected", endpoint, query, response.StatusCode)
			}
		}
	}
	var inventory struct {
		Items []datarights.Request `json:"items"`
	}
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/data-rights", nil, &inventory)
	if response.StatusCode != http.StatusOK || len(inventory.Items) < 2 {
		t.Fatalf("Admin data rights inventory: status=%d items=%d", response.StatusCode, len(inventory.Items))
	}
	var hold datarights.Hold
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/data-rights/holds", map[string]any{
		"userId": owner.ID, "authorityReference": "LEGAL-HTTP-REFERENCE",
	}, &hold)
	if response.StatusCode != http.StatusCreated || hold.Status != "active" || len(hold.AuthorityReferenceHash) != 64 {
		t.Fatalf("create legal hold: status=%d item=%#v", response.StatusCode, hold)
	}
	var ownerRequests struct {
		Items []datarights.Request `json:"items"`
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/account/data-rights", nil, &ownerRequests)
	if response.StatusCode != http.StatusOK {
		t.Fatal(response.StatusCode)
	}
	foundBlocked := false
	for _, item := range ownerRequests.Items {
		foundBlocked = foundBlocked || item.ID == deletionRequest.ID && item.Status == "blocked"
	}
	if !foundBlocked {
		encoded, _ := json.Marshal(ownerRequests.Items)
		t.Fatalf("legal hold did not block deletion: %s", encoded)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO data_rights_requests(id,user_id,request_type,status,subject_ref,execute_after,completed_at,created_at,updated_at)
		SELECT gen_random_uuid(),$1,'data_export','completed',$2,
		       now()-interval '40 days'-make_interval(secs=>value),
		       now()-interval '40 days'-make_interval(secs=>value),
		       now()-interval '40 days'-make_interval(secs=>value),
		       now()-interval '40 days'-make_interval(secs=>value)
		FROM generate_series(1,104) value`, owner.ID, exportRequest.SubjectRef); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO data_rights_legal_holds(id,user_id,reason,authority_reference_hash,status,review_at,expires_at,created_by,released_by,released_at,created_at)
		SELECT gen_random_uuid(),$1,'Historical released legal hold evidence',repeat('a',64),'released',
		       now()+interval '50 days',now()+interval '325 days',$2,$2,
		       now()-interval '40 days'-make_interval(secs=>value),
		       now()-interval '40 days'-make_interval(secs=>value)
		FROM generate_series(1,105) value`, owner.ID, administrator.ID); err != nil {
		t.Fatal(err)
	}

	ownerSeen := make(map[string]struct{})
	ownerCursor := ""
	for {
		var page struct {
			Items      []datarights.Request `json:"items"`
			NextCursor *string              `json:"nextCursor"`
		}
		path := server.URL + "/api/v1/account/data-rights?limit=25"
		if ownerCursor != "" {
			path += "&cursor=" + ownerCursor
		}
		response = requestJSON(t, client, http.MethodGet, path, nil, &page)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("owner data-rights page failed: %d", response.StatusCode)
		}
		for _, item := range page.Items {
			if _, duplicate := ownerSeen[item.ID.String()]; duplicate {
				t.Fatalf("duplicate owner data-rights request %s", item.ID)
			}
			ownerSeen[item.ID.String()] = struct{}{}
		}
		if page.NextCursor == nil {
			break
		}
		ownerCursor = *page.NextCursor
	}
	if len(ownerSeen) != 106 {
		t.Fatalf("expected 106 owner data-rights requests, got %d", len(ownerSeen))
	}
	response = requestJSON(t, otherClient, http.MethodGet, server.URL+"/api/v1/account/data-rights?limit=50", nil, &ownerRequests)
	if response.StatusCode != http.StatusOK || len(ownerRequests.Items) != 0 {
		t.Fatalf("owner data-rights isolation failed: status=%d items=%d", response.StatusCode, len(ownerRequests.Items))
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/account/data-rights?cursor="+ownerCursor+"modified", nil, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("modified owner data-rights cursor accepted: %d", response.StatusCode)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/account/data-rights?limit=51", nil, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("oversized owner data-rights page accepted: %d", response.StatusCode)
	}

	adminSeen := make(map[string]struct{})
	adminCursor := ""
	for {
		var page struct {
			Items      []datarights.Request `json:"items"`
			NextCursor *string              `json:"nextCursor"`
		}
		path := server.URL + "/api/v1/admin/data-rights?limit=25"
		if adminCursor != "" {
			path += "&cursor=" + adminCursor
		}
		response = requestJSON(t, adminClient, http.MethodGet, path, nil, &page)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("Admin data-rights page failed: %d", response.StatusCode)
		}
		for _, item := range page.Items {
			if _, duplicate := adminSeen[item.ID.String()]; duplicate {
				t.Fatalf("duplicate Admin data-rights request %s", item.ID)
			}
			adminSeen[item.ID.String()] = struct{}{}
		}
		if page.NextCursor == nil {
			break
		}
		adminCursor = *page.NextCursor
	}
	if len(adminSeen) != 106 {
		t.Fatalf("expected 106 Admin data-rights requests, got %d", len(adminSeen))
	}
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/data-rights?cursor="+adminCursor+"modified", nil, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("modified Admin data-rights cursor accepted: %d", response.StatusCode)
	}

	holdSeen := make(map[string]struct{})
	holdCursor := ""
	firstHold := true
	for {
		var page struct {
			Items      []datarights.Hold `json:"items"`
			NextCursor *string           `json:"nextCursor"`
		}
		path := server.URL + "/api/v1/admin/data-rights/holds?limit=25"
		if holdCursor != "" {
			path += "&cursor=" + holdCursor
		}
		response = requestJSON(t, adminClient, http.MethodGet, path, nil, &page)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("Admin legal-hold page failed: %d", response.StatusCode)
		}
		if firstHold && (len(page.Items) == 0 || page.Items[0].ID != hold.ID || page.Items[0].Status != "active") {
			t.Fatalf("active legal hold lost priority: %#v", page.Items)
		}
		firstHold = false
		for _, item := range page.Items {
			if _, duplicate := holdSeen[item.ID.String()]; duplicate {
				t.Fatalf("duplicate legal hold %s", item.ID)
			}
			holdSeen[item.ID.String()] = struct{}{}
		}
		if page.NextCursor == nil {
			break
		}
		holdCursor = *page.NextCursor
	}
	if len(holdSeen) != 106 {
		t.Fatalf("expected 106 legal holds, got %d", len(holdSeen))
	}
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/data-rights/holds?cursor="+holdCursor+"modified", nil, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("modified legal-hold cursor accepted: %d", response.StatusCode)
	}
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/data-rights/holds?limit=51", nil, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("oversized legal-hold page accepted: %d", response.StatusCode)
	}
}
