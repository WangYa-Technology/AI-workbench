package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/risk"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestAdminRiskHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	memberClient, adminClient := testHTTPClient(t), testHTTPClient(t)
	member := registerGovernanceUser(t, memberClient, server.URL, "risk_member")
	administrator := registerGovernanceUser(t, adminClient, server.URL, "risk_admin")
	if _, err := pool.Exec(context.Background(), `UPDATE users SET role='admin' WHERE id=$1`, administrator.ID); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	resourceID := uuid.New()
	signalID, err := risk.RecordTx(ctx, tx, risk.SignalInput{
		SourceKey: "http-risk:" + resourceID.String(), ResourceType: "order", ResourceID: resourceID,
		SubjectUserID: member.ID, ActorUserID: &member.ID, SignalType: "transaction_refund", Severity: "medium", Score: 55,
		Summary: "HTTP transaction requires a controlled operations review.", Evidence: map[string]any{"paymentMode": "local_test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	response := requestJSON(t, memberClient, http.MethodGet, server.URL+"/api/v1/admin/risk/signals", nil, nil)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("member accessed risk queue: %d", response.StatusCode)
	}
	var queue struct {
		Items []admin.RiskSignal `json:"items"`
	}
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/risk/signals", nil, &queue)
	if response.StatusCode != http.StatusOK || len(queue.Items) != 1 || queue.Items[0].ID != signalID || len(queue.Items[0].Events) != 1 {
		t.Fatalf("risk queue contract failed: status=%d items=%#v", response.StatusCode, queue.Items)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO risk_signals(source_key,resource_type,resource_id,subject_user_id,signal_type,severity,score,summary,evidence)
		SELECT 'http-queue-pressure:'||value::text,'order',gen_random_uuid(),$1,'transaction_refund','critical',100,
		       'Higher-priority HTTP queue pressure signal.','{}'::jsonb
		FROM generate_series(1,201) value`, member.ID); err != nil {
		t.Fatal(err)
	}
	queue.Items = nil
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/risk/signals?resourceType=order&resourceId="+resourceID.String(), nil, &queue)
	if response.StatusCode != http.StatusOK || len(queue.Items) != 1 || queue.Items[0].ID != signalID {
		t.Fatalf("focused risk queue was truncated: status=%d items=%#v", response.StatusCode, queue.Items)
	}
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/risk/signals?resourceType=order", nil, nil)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unpaired risk filter status: %d", response.StatusCode)
	}
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/risk/signals/"+signalID.String()+"/review", map[string]any{
		"decision": "monitor", "expectedVersion": 2}, nil)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("stale risk review status: %d", response.StatusCode)
	}
	var reviewed admin.RiskSignal
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/risk/signals/"+signalID.String()+"/review", map[string]any{
		"decision": "no_action", "expectedVersion": 1}, &reviewed)
	if response.StatusCode != http.StatusOK || reviewed.Status != "dismissed" || reviewed.Version != 2 || len(reviewed.Events) != 2 {
		t.Fatalf("risk review contract failed: status=%d signal=%#v", response.StatusCode, reviewed)
	}
}

func TestAdminRiskRulesHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	memberClient, adminClient := testHTTPClient(t), testHTTPClient(t)
	_ = registerGovernanceUser(t, memberClient, server.URL, "rules_member")
	administrator := registerGovernanceUser(t, adminClient, server.URL, "rules_admin")
	if _, err := pool.Exec(context.Background(), `UPDATE users SET role='admin' WHERE id=$1`, administrator.ID); err != nil {
		t.Fatal(err)
	}

	response := requestJSON(t, memberClient, http.MethodGet, server.URL+"/api/v1/admin/risk/rules", nil, nil)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("member accessed risk rule policy: %d", response.StatusCode)
	}
	var initial admin.RiskRulePolicy
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/risk/rules", nil, &initial)
	if response.StatusCode != http.StatusOK || initial.Current.Version != 1 || initial.Current.TaskDisputeScore != 85 {
		t.Fatalf("initial risk rule contract failed: status=%d policy=%#v", response.StatusCode, initial)
	}
	input := map[string]any{
		"name": "HTTP verified rule revision", "taskDisputeScore": 92, "transactionRefundScore": 46,
		"communityReportScore": 38, "mediaRejectionScore": 79,
		"accountLinkScore": 67, "accountLinkMinAccounts": 4, "accountLinkWindowHours": 48,
		"mediumThreshold": 35, "highThreshold": 65, "criticalThreshold": 90,
		"expectedVersion": 1,
	}
	input["expectedVersion"] = 2
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/risk/rules", input, nil)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("stale risk rule update status: %d", response.StatusCode)
	}
	input["expectedVersion"] = 1
	var updated admin.RiskRulePolicy
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/risk/rules", input, &updated)
	if response.StatusCode != http.StatusOK || updated.Current.Version != 2 || updated.Current.TaskDisputeScore != 92 || updated.Current.CommunityReportScore != 38 || updated.Current.MediaRejectionScore != 79 ||
		updated.Current.AccountLinkScore != 67 || updated.Current.AccountLinkMinAccounts != 4 || updated.Current.AccountLinkWindowHours != 48 || len(updated.History) != 2 {
		t.Fatalf("risk rule update contract failed: status=%d policy=%#v", response.StatusCode, updated)
	}
}

func TestRegistrationAccountLinkRiskHTTPContract(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	memberClient, adminClient, subjectClient := testHTTPClient(t), testHTTPClient(t), testHTTPClient(t)
	_ = registerGovernanceUser(t, memberClient, server.URL, "link_member")
	administrator := registerGovernanceUser(t, adminClient, server.URL, "link_admin")
	subject := registerGovernanceUser(t, subjectClient, server.URL, "link_subject")
	if _, err := pool.Exec(context.Background(), `UPDATE users SET role='admin' WHERE id=$1`, administrator.ID); err != nil {
		t.Fatal(err)
	}

	endpoint := server.URL + "/api/v1/admin/risk/signals?resourceType=user&resourceId=" + subject.ID.String()
	response := requestJSON(t, memberClient, http.MethodGet, endpoint, nil, nil)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("member accessed account-link risk evidence: %d", response.StatusCode)
	}
	var queue struct {
		Items []admin.RiskSignal `json:"items"`
	}
	response = requestJSON(t, adminClient, http.MethodGet, endpoint, nil, &queue)
	if response.StatusCode != http.StatusOK || len(queue.Items) != 1 {
		t.Fatalf("account-link risk focus failed: status=%d items=%#v", response.StatusCode, queue.Items)
	}
	signal := queue.Items[0]
	if signal.ResourceType != "user" || signal.ResourceID != subject.ID || signal.SubjectUserID != subject.ID ||
		signal.SignalType != "account_link" || signal.Score != 65 || signal.Severity != "medium" ||
		signal.ResourceTitle != "Account @"+subject.Handle || signal.TargetPath != "/admin?tab=users&q="+subject.Handle {
		t.Fatalf("account-link risk projection mismatch: %#v", signal)
	}
	if signal.Evidence["linkedAccountCount"] != float64(3) || signal.Evidence["minimumAccounts"] != float64(3) ||
		signal.Evidence["windowHours"] != float64(24) || signal.Evidence["networkDataStored"] != false || signal.Evidence["riskRuleVersion"] != float64(1) {
		t.Fatalf("account-link risk evidence mismatch: %#v", signal.Evidence)
	}
	for _, forbiddenKey := range []string{"networkHash", "ipAddress", "deviceFingerprint", "linkedAccounts"} {
		if _, present := signal.Evidence[forbiddenKey]; present {
			t.Fatalf("account-link risk evidence exposed %s: %#v", forbiddenKey, signal.Evidence)
		}
	}
}
