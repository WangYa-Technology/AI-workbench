package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/transport/httpapi"
)

func TestAdminRevisionHistoriesHTTPPagination(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	client := testHTTPClient(t)
	administrator := registerGovernanceUser(t, client, server.URL, "revision_admin")
	ctx := context.Background()
	statements := []struct {
		query string
		args  []any
	}{
		{query: `UPDATE users SET role='admin' WHERE id=$1`, args: []any{administrator.ID}},
		{query: `INSERT INTO system_setting_revisions(version,name,registrations_enabled,generations_enabled,publishing_enabled,marketplace_checkout_enabled,task_creation_enabled,reason) SELECT version,'System setting revision '||version,true,true,true,true,true,'HTTP pagination evidence for system setting history.' FROM generate_series(2,22) version`},
		{query: `UPDATE system_setting_state SET active_revision_id=(SELECT id FROM system_setting_revisions WHERE version=22),version=22 WHERE singleton=true`},
		{query: `INSERT INTO risk_rule_revisions(version,name,task_dispute_score,transaction_refund_score,community_report_score,media_rejection_score,medium_threshold,high_threshold,critical_threshold,reason) SELECT version,'Risk rule revision '||version,85,55,35,75,40,70,90,'HTTP pagination evidence for risk rule history.' FROM generate_series(2,22) version`},
		{query: `UPDATE risk_rule_state SET active_revision_id=(SELECT id FROM risk_rule_revisions WHERE version=22),version=22 WHERE singleton=true`},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}

	for _, test := range []struct {
		path string
		kind string
	}{
		{path: "/api/v1/admin/settings", kind: "settings"},
		{path: "/api/v1/admin/risk/rules", kind: "risk rules"},
	} {
		var first admin.SystemSettingPolicy
		if test.kind == "settings" {
			response := requestJSON(t, client, http.MethodGet, server.URL+test.path+"?limit=20", nil, &first)
			if response.StatusCode != http.StatusOK || first.Current.Version != 22 || len(first.History) != 20 || first.NextCursor == nil {
				t.Fatalf("%s first page mismatch: status=%d policy=%#v", test.kind, response.StatusCode, first)
			}
			var second admin.SystemSettingPolicy
			response = requestJSON(t, client, http.MethodGet, server.URL+test.path+"?limit=20&cursor="+url.QueryEscape(*first.NextCursor), nil, &second)
			if response.StatusCode != http.StatusOK || second.Current.Version != 22 || len(second.History) != 2 || second.NextCursor != nil {
				t.Fatalf("%s second page mismatch: status=%d policy=%#v", test.kind, response.StatusCode, second)
			}
		} else {
			var firstRisk admin.RiskRulePolicy
			response := requestJSON(t, client, http.MethodGet, server.URL+test.path+"?limit=20", nil, &firstRisk)
			if response.StatusCode != http.StatusOK || firstRisk.Current.Version != 22 || len(firstRisk.History) != 20 || firstRisk.NextCursor == nil {
				t.Fatalf("%s first page mismatch: status=%d policy=%#v", test.kind, response.StatusCode, firstRisk)
			}
			var secondRisk admin.RiskRulePolicy
			response = requestJSON(t, client, http.MethodGet, server.URL+test.path+"?limit=20&cursor="+url.QueryEscape(*firstRisk.NextCursor), nil, &secondRisk)
			if response.StatusCode != http.StatusOK || secondRisk.Current.Version != 22 || len(secondRisk.History) != 2 || secondRisk.NextCursor != nil {
				t.Fatalf("%s second page mismatch: status=%d policy=%#v", test.kind, response.StatusCode, secondRisk)
			}
		}
		if response := requestJSON(t, client, http.MethodGet, server.URL+test.path+"?cursor=modified", nil, nil); response.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("%s modified cursor status: %d", test.kind, response.StatusCode)
		}
		if response := requestJSON(t, client, http.MethodGet, server.URL+test.path+"?limit=51", nil, nil); response.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("%s oversized page status: %d", test.kind, response.StatusCode)
		}
	}
}

func TestAdminDiscoveryHistoriesHTTPPagination(t *testing.T) {
	pool, cleanup := httpTestPool(t)
	defer cleanup()
	server := httptest.NewServer(httpapi.New(config.Config{Environment: "test", MediaRoot: t.TempDir(), WebOrigin: "http://localhost:5173", LocalProviderEnabled: true}, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	client := testHTTPClient(t)
	administrator := registerGovernanceUser(t, client, server.URL, "discovery_admin")
	ctx := context.Background()
	statements := []struct {
		query string
		args  []any
	}{
		{query: `UPDATE users SET role='admin' WHERE id=$1`, args: []any{administrator.ID}},
		{query: `INSERT INTO discovery_ranking_revisions(version,name,title_exact_weight,title_prefix_weight,title_contains_weight,creator_exact_weight,creator_match_weight,body_match_weight,secondary_match_weight,recency_weight,creator_activity_weight,work_type_boost,creator_type_boost,product_type_boost,demand_type_boost,reason) SELECT version,'Ranking revision '||version,100,80,60,50,35,25,12,10,15,0,0,0,0,'HTTP evidence for ranking history pagination.' FROM generate_series(2,22) version`},
		{query: `UPDATE discovery_ranking_state SET active_revision_id=(SELECT id FROM discovery_ranking_revisions WHERE version=22),candidate_revision_id=(SELECT id FROM discovery_ranking_revisions WHERE version=21),version=22 WHERE singleton=true`},
		{query: `INSERT INTO discovery_index_runs(operation,status,document_counts,index_sizes,reason,started_at,completed_at) SELECT 'analyze','succeeded','{}'::jsonb,'{}'::jsonb,'HTTP evidence for index history pagination.',now(),now() FROM generate_series(1,22)`},
		{query: `INSERT INTO discovery_ranking_evaluations(candidate_revision_id,baseline_revision_id,status,case_count,candidate_top1_hits,baseline_top1_hits,candidate_mrr,baseline_mrr,safety_violations,metrics,reason) SELECT (SELECT id FROM discovery_ranking_revisions WHERE version=21),(SELECT id FROM discovery_ranking_revisions WHERE version=22),'passed',1,1,1,1,1,0,'{}'::jsonb,'HTTP evidence for evaluation history pagination.' FROM generate_series(1,22)`},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}

	var firstRanking admin.RankingPolicy
	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/admin/discovery/ranking?limit=20", nil, &firstRanking)
	if response.StatusCode != http.StatusOK || firstRanking.Current.Version != 22 || firstRanking.Candidate == nil || firstRanking.Candidate.Version != 21 || len(firstRanking.History) != 20 || firstRanking.NextCursor == nil {
		t.Fatalf("ranking first page mismatch: status=%d policy=%#v", response.StatusCode, firstRanking)
	}
	var secondRanking admin.RankingPolicy
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/admin/discovery/ranking?limit=20&cursor="+url.QueryEscape(*firstRanking.NextCursor), nil, &secondRanking)
	if response.StatusCode != http.StatusOK || secondRanking.Current.Version != 22 || len(secondRanking.History) != 2 || secondRanking.NextCursor != nil {
		t.Fatalf("ranking second page mismatch: status=%d policy=%#v", response.StatusCode, secondRanking)
	}

	var firstOperations admin.DiscoveryOperations
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/admin/discovery/operations?limit=20", nil, &firstOperations)
	if response.StatusCode != http.StatusOK || len(firstOperations.IndexRuns) != 20 || len(firstOperations.Evaluations) != 20 || firstOperations.IndexNextCursor == nil || firstOperations.EvaluationNextCursor == nil {
		t.Fatalf("Discovery first page mismatch: status=%d operations=%#v", response.StatusCode, firstOperations)
	}
	var indexPage admin.DiscoveryOperations
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/admin/discovery/operations?limit=20&indexCursor="+url.QueryEscape(*firstOperations.IndexNextCursor), nil, &indexPage)
	if response.StatusCode != http.StatusOK || len(indexPage.IndexRuns) != 2 || indexPage.IndexNextCursor != nil {
		t.Fatalf("index second page mismatch: status=%d operations=%#v", response.StatusCode, indexPage)
	}
	var evaluationPage admin.DiscoveryOperations
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/admin/discovery/operations?limit=20&evaluationCursor="+url.QueryEscape(*firstOperations.EvaluationNextCursor), nil, &evaluationPage)
	if response.StatusCode != http.StatusOK || len(evaluationPage.Evaluations) != 2 || evaluationPage.EvaluationNextCursor != nil {
		t.Fatalf("evaluation second page mismatch: status=%d operations=%#v", response.StatusCode, evaluationPage)
	}
	for _, path := range []string{
		"/api/v1/admin/discovery/ranking?cursor=modified",
		"/api/v1/admin/discovery/operations?indexCursor=modified",
		"/api/v1/admin/discovery/operations?evaluationCursor=modified",
		"/api/v1/admin/discovery/operations?limit=51",
	} {
		if response := requestJSON(t, client, http.MethodGet, server.URL+path, nil, nil); response.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("invalid Discovery history accepted: path=%s status=%d", path, response.StatusCode)
		}
	}
}
