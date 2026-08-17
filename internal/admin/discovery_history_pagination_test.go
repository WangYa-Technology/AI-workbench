package admin_test

import (
	"context"
	"errors"
	"testing"

	"github.com/hcai-chat/hcai-chat/internal/admin"
)

func TestDiscoveryHistoriesTraverseStableEvidence(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	statements := []string{
		`INSERT INTO discovery_ranking_revisions(version,name,title_exact_weight,title_prefix_weight,title_contains_weight,creator_exact_weight,creator_match_weight,body_match_weight,secondary_match_weight,recency_weight,creator_activity_weight,work_type_boost,creator_type_boost,product_type_boost,demand_type_boost,reason)
		 SELECT version,'Ranking revision '||version,100,80,60,50,35,25,12,10,15,0,0,0,0,'Scale evidence for immutable ranking history pagination.' FROM generate_series(2,106) version`,
		`UPDATE discovery_ranking_state SET active_revision_id=(SELECT id FROM discovery_ranking_revisions WHERE version=106),candidate_revision_id=(SELECT id FROM discovery_ranking_revisions WHERE version=105),version=106 WHERE singleton=true`,
		`INSERT INTO discovery_index_runs(operation,status,document_counts,index_sizes,reason,started_at,completed_at)
		 SELECT 'analyze','succeeded','{"works":1}'::jsonb,'{"works":1024}'::jsonb,'Scale evidence for immutable index run history pagination.',timestamp '2026-01-01 00:00:00+00',timestamp '2026-01-01 00:00:01+00' FROM generate_series(1,106)`,
		`INSERT INTO discovery_ranking_evaluations(candidate_revision_id,baseline_revision_id,status,case_count,candidate_top1_hits,baseline_top1_hits,candidate_mrr,baseline_mrr,safety_violations,metrics,reason,created_at)
		 SELECT (SELECT id FROM discovery_ranking_revisions WHERE version=105),(SELECT id FROM discovery_ranking_revisions WHERE version=106),'passed',1,1,1,1,1,0,'{}'::jsonb,'Scale evidence for immutable evaluation history pagination.',timestamp '2026-01-01 00:00:02+00' FROM generate_series(1,106)`,
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	service := admin.NewService(pool, true)

	rankingSeen := map[int]bool{}
	rankingCursor := ""
	for {
		page, err := service.GetRankingPolicy(ctx, admin.RevisionHistoryInput{Cursor: rankingCursor, Limit: 20})
		if err != nil {
			t.Fatal(err)
		}
		if page.Current.Version != 106 || page.Candidate == nil || page.Candidate.Version != 105 {
			t.Fatalf("exact ranking state drifted: current=%d candidate=%#v", page.Current.Version, page.Candidate)
		}
		for _, item := range page.History {
			if rankingSeen[item.Version] {
				t.Fatalf("duplicate ranking version %d", item.Version)
			}
			rankingSeen[item.Version] = true
		}
		if page.NextCursor == nil {
			break
		}
		rankingCursor = *page.NextCursor
	}
	if len(rankingSeen) != 106 {
		t.Fatalf("ranking traversal count: %d", len(rankingSeen))
	}

	indexSeen := map[string]bool{}
	indexCursor := ""
	for {
		page, err := service.GetDiscoveryOperations(ctx, admin.DiscoveryHistoryInput{IndexCursor: indexCursor, Limit: 20})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.IndexRuns {
			key := item.ID.String()
			if indexSeen[key] {
				t.Fatalf("duplicate index run %s", key)
			}
			indexSeen[key] = true
		}
		if page.IndexNextCursor == nil {
			break
		}
		indexCursor = *page.IndexNextCursor
	}
	if len(indexSeen) != 106 {
		t.Fatalf("index traversal count: %d", len(indexSeen))
	}

	evaluationSeen := map[string]bool{}
	evaluationCursor := ""
	for {
		page, err := service.GetDiscoveryOperations(ctx, admin.DiscoveryHistoryInput{EvaluationCursor: evaluationCursor, Limit: 20})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Evaluations {
			key := item.ID.String()
			if evaluationSeen[key] {
				t.Fatalf("duplicate evaluation %s", key)
			}
			evaluationSeen[key] = true
		}
		if page.EvaluationNextCursor == nil {
			break
		}
		evaluationCursor = *page.EvaluationNextCursor
	}
	if len(evaluationSeen) != 106 {
		t.Fatalf("evaluation traversal count: %d", len(evaluationSeen))
	}

	if _, err := service.GetRankingPolicy(ctx, admin.RevisionHistoryInput{Cursor: "modified"}); !errors.Is(err, admin.ErrInvalidRankingHistory) {
		t.Fatalf("modified ranking cursor accepted: %v", err)
	}
	if _, err := service.GetDiscoveryOperations(ctx, admin.DiscoveryHistoryInput{IndexCursor: "modified"}); !errors.Is(err, admin.ErrInvalidDiscoveryHistory) {
		t.Fatalf("modified index cursor accepted: %v", err)
	}
	if _, err := service.GetDiscoveryOperations(ctx, admin.DiscoveryHistoryInput{EvaluationCursor: "modified"}); !errors.Is(err, admin.ErrInvalidDiscoveryHistory) {
		t.Fatalf("modified evaluation cursor accepted: %v", err)
	}
	if _, err := service.GetDiscoveryOperations(ctx, admin.DiscoveryHistoryInput{Limit: 51}); !errors.Is(err, admin.ErrInvalidDiscoveryHistory) {
		t.Fatalf("oversized Discovery history page accepted: %v", err)
	}
}
