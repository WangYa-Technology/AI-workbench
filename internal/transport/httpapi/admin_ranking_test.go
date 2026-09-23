package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/discovery"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestAdminRankingPolicyChangesPublicSearchScore(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{
		Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true,
	}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	memberClient, adminClient := testHTTPClient(t), testHTTPClient(t)
	member := registerGovernanceUser(t, memberClient, server.URL, "ranking_member")
	administrator := registerGovernanceUser(t, adminClient, server.URL, "ranking_admin")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1`, administrator.ID); err != nil {
		t.Fatal(err)
	}
	assetID, productID := uuid.New(), uuid.New()
	title := "Ranking Signal " + productID.String()[:8]
	if _, err := pool.Exec(ctx, `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code)
		VALUES($1,$2,'image',$3,'/media/ranking.jpg','image/jpeg','clean','delivery','hcai-commercial-standard-v1')`,
		assetID, member.ID, title); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status,ai_disclosure,included_files,compatibility)
		VALUES($1,$2,$3,$4,'Bounded public ranking verification','workflow',1200,'USD','hcai-commercial-standard-v1','active','Deterministic Local Test evidence.','[]','HCAI')`,
		productID, member.ID, assetID, title); err != nil {
		t.Fatal(err)
	}

	response := requestJSON(t, memberClient, http.MethodGet, server.URL+"/api/v1/admin/discovery/ranking", nil, nil)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("member accessed ranking controls: %d", response.StatusCode)
	}
	var policy admin.RankingPolicy
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/discovery/ranking", nil, &policy)
	if response.StatusCode != http.StatusOK || policy.Current.Version != 1 {
		t.Fatalf("ranking policy contract failed: status=%d policy=%#v", response.StatusCode, policy)
	}

	searchURL := server.URL + "/api/v1/search?q=" + url.QueryEscape(title) + "&types=product"
	var before discovery.SearchPage
	response = requestJSON(t, memberClient, http.MethodGet, searchURL, nil, &before)
	if response.StatusCode != http.StatusOK || len(before.Items) != 1 || before.PolicyVersion != 1 {
		t.Fatalf("baseline ranked search failed: status=%d page=%#v", response.StatusCode, before)
	}
	input := admin.RankingUpdate{
		Name: policy.Current.Name, TitleExactWeight: policy.Current.TitleExactWeight,
		TitlePrefixWeight: policy.Current.TitlePrefixWeight, TitleContainsWeight: policy.Current.TitleContainsWeight,
		CreatorExactWeight: policy.Current.CreatorExactWeight, CreatorMatchWeight: policy.Current.CreatorMatchWeight,
		BodyMatchWeight: policy.Current.BodyMatchWeight, SecondaryMatchWeight: policy.Current.SecondaryMatchWeight,
		RecencyWeight: policy.Current.RecencyWeight, CreatorActivityWeight: policy.Current.CreatorActivityWeight,
		WorkTypeBoost: policy.Current.WorkTypeBoost, CreatorTypeBoost: policy.Current.CreatorTypeBoost,
		ProductTypeBoost: policy.Current.ProductTypeBoost + 7, DemandTypeBoost: policy.Current.DemandTypeBoost,
		ExpectedVersion: 1,
	}
	stale := input
	stale.ExpectedVersion = 2
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/discovery/ranking", stale, nil)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("stale ranking update status: %d", response.StatusCode)
	}
	var updated admin.RankingPolicy
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/discovery/ranking", input, &updated)
	if response.StatusCode != http.StatusOK || updated.Current.Version != 2 || len(updated.History) != 2 {
		t.Fatalf("ranking update contract failed: status=%d policy=%#v", response.StatusCode, updated)
	}
	var after discovery.SearchPage
	response = requestJSON(t, memberClient, http.MethodGet, searchURL, nil, &after)
	if response.StatusCode != http.StatusOK || after.PolicyVersion != 2 || len(after.Items) != 1 || after.Items[0].Rank != before.Items[0].Rank+7 {
		t.Fatalf("ranking policy did not change public score: before=%#v after=%#v", before, after)
	}

	response = requestJSON(t, memberClient, http.MethodGet, server.URL+"/api/v1/admin/discovery/operations", nil, nil)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("member accessed discovery operations: %d", response.StatusCode)
	}
	candidateInput := input
	candidateInput.Name = "Staged product candidate"
	candidateInput.ProductTypeBoost = updated.Current.ProductTypeBoost + 1
	candidateInput.ExpectedVersion = updated.Current.Version
	var candidatePolicy admin.RankingPolicy
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/discovery/ranking/candidates", candidateInput, &candidatePolicy)
	if response.StatusCode != http.StatusOK || candidatePolicy.Candidate == nil || candidatePolicy.Candidate.Version != 3 || candidatePolicy.Current.Version != 2 || candidatePolicy.Rollout.Version != 2 {
		t.Fatalf("candidate contract failed: status=%d policy=%#v", response.StatusCode, candidatePolicy)
	}
	rollout := admin.RankingRolloutUpdate{Percent: 25, ExpectedVersion: candidatePolicy.Rollout.Version}
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/discovery/ranking/rollout", rollout, nil)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("unevaluated candidate rollout status: %d", response.StatusCode)
	}
	var evaluation admin.RankingEvaluation
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/discovery/ranking/evaluations", nil, &evaluation)
	if response.StatusCode != http.StatusOK || evaluation.Status != "passed" || evaluation.CandidateVersion != 3 || evaluation.CaseCount < 1 {
		t.Fatalf("evaluation contract failed: status=%d evaluation=%#v", response.StatusCode, evaluation)
	}
	var indexRun admin.DiscoveryIndexRun
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/discovery/index/analyze", nil, &indexRun)
	if response.StatusCode != http.StatusOK || indexRun.DocumentCounts["products"] != 1 || len(indexRun.IndexSizes) != 5 {
		t.Fatalf("index operation contract failed: status=%d run=%#v", response.StatusCode, indexRun)
	}
	var operations admin.DiscoveryOperations
	response = requestJSON(t, adminClient, http.MethodGet, server.URL+"/api/v1/admin/discovery/operations", nil, &operations)
	if response.StatusCode != http.StatusOK || len(operations.IndexRuns) != 1 || len(operations.Evaluations) != 1 {
		t.Fatalf("operations inventory contract failed: status=%d operations=%#v", response.StatusCode, operations)
	}
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/discovery/ranking/rollout", rollout, &candidatePolicy)
	if response.StatusCode != http.StatusOK || candidatePolicy.Rollout.Percent != 25 || candidatePolicy.Current.Version != 2 {
		t.Fatalf("staged rollout contract failed: status=%d policy=%#v", response.StatusCode, candidatePolicy)
	}
	rollout.Percent = 100
	rollout.ExpectedVersion = candidatePolicy.Rollout.Version
	candidatePolicy = admin.RankingPolicy{}
	response = requestJSON(t, adminClient, http.MethodPost, server.URL+"/api/v1/admin/discovery/ranking/rollout", rollout, &candidatePolicy)
	if response.StatusCode != http.StatusOK || candidatePolicy.Current.Version != 3 || candidatePolicy.Candidate != nil || candidatePolicy.Rollout.Percent != 0 {
		t.Fatalf("candidate promotion contract failed: status=%d policy=%#v", response.StatusCode, candidatePolicy)
	}
	var promoted discovery.SearchPage
	response = requestJSON(t, memberClient, http.MethodGet, searchURL, nil, &promoted)
	if response.StatusCode != http.StatusOK || promoted.PolicyVersion != 3 || promoted.Items[0].Rank != after.Items[0].Rank+1 {
		t.Fatalf("promoted policy not public: status=%d page=%#v", response.StatusCode, promoted)
	}
}
