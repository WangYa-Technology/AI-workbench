package admin_test

import (
	"context"
	"errors"
	"testing"

	"github.com/hcai-chat/hcai-chat/internal/admin"
)

func TestSystemSettingHistoryTraversesEveryImmutableRevision(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO system_setting_revisions(version,name,registrations_enabled,generations_enabled,publishing_enabled,marketplace_checkout_enabled,task_creation_enabled,reason,created_at)
		SELECT version,'System setting revision '||version,true,true,true,true,true,'Scale evidence for stable system setting history pagination.',now()+(version||' milliseconds')::interval
		FROM generate_series(2,106) version;
		UPDATE system_setting_state SET active_revision_id=(SELECT id FROM system_setting_revisions WHERE version=106),version=106 WHERE singleton=true`); err != nil {
		t.Fatal(err)
	}
	service := admin.NewService(pool, true)
	seen := map[int]bool{}
	cursor := ""
	previous := 107
	for {
		page, err := service.GetSystemSettingPolicy(ctx, admin.RevisionHistoryInput{Cursor: cursor, Limit: 20})
		if err != nil {
			t.Fatal(err)
		}
		if page.Current.Version != 106 {
			t.Fatalf("current revision drifted on history page: %d", page.Current.Version)
		}
		for _, item := range page.History {
			if seen[item.Version] || item.Version >= previous {
				t.Fatalf("duplicate or unstable system setting version: %d after %d", item.Version, previous)
			}
			seen[item.Version] = true
			previous = item.Version
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(seen) != 106 || previous != 1 {
		t.Fatalf("system setting traversal mismatch: count=%d oldest=%d", len(seen), previous)
	}
	if _, err := service.GetSystemSettingPolicy(ctx, admin.RevisionHistoryInput{Cursor: "modified", Limit: 20}); !errors.Is(err, admin.ErrInvalidSystemSettingHistory) {
		t.Fatalf("modified system setting cursor accepted: %v", err)
	}
	if _, err := service.GetSystemSettingPolicy(ctx, admin.RevisionHistoryInput{Limit: 51}); !errors.Is(err, admin.ErrInvalidSystemSettingHistory) {
		t.Fatalf("oversized system setting page accepted: %v", err)
	}
}

func TestRiskRuleHistoryTraversesEveryImmutableRevision(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO risk_rule_revisions(version,name,task_dispute_score,transaction_refund_score,community_report_score,media_rejection_score,medium_threshold,high_threshold,critical_threshold,reason,created_at)
		SELECT version,'Risk rule revision '||version,85,55,35,75,40,70,90,'Scale evidence for stable risk rule history pagination.',now()+(version||' milliseconds')::interval
		FROM generate_series(2,106) version;
		UPDATE risk_rule_state SET active_revision_id=(SELECT id FROM risk_rule_revisions WHERE version=106),version=106 WHERE singleton=true`); err != nil {
		t.Fatal(err)
	}
	service := admin.NewService(pool, true)
	seen := map[int]bool{}
	cursor := ""
	previous := 107
	for {
		page, err := service.GetRiskRulePolicy(ctx, admin.RevisionHistoryInput{Cursor: cursor, Limit: 20})
		if err != nil {
			t.Fatal(err)
		}
		if page.Current.Version != 106 {
			t.Fatalf("current risk rule drifted on history page: %d", page.Current.Version)
		}
		for _, item := range page.History {
			if seen[item.Version] || item.Version >= previous {
				t.Fatalf("duplicate or unstable risk rule version: %d after %d", item.Version, previous)
			}
			seen[item.Version] = true
			previous = item.Version
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(seen) != 106 || previous != 1 {
		t.Fatalf("risk rule traversal mismatch: count=%d oldest=%d", len(seen), previous)
	}
	if _, err := service.GetRiskRulePolicy(ctx, admin.RevisionHistoryInput{Cursor: "modified", Limit: 20}); !errors.Is(err, admin.ErrInvalidRiskRuleHistory) {
		t.Fatalf("modified risk rule cursor accepted: %v", err)
	}
	if _, err := service.GetRiskRulePolicy(ctx, admin.RevisionHistoryInput{Limit: 51}); !errors.Is(err, admin.ErrInvalidRiskRuleHistory) {
		t.Fatalf("oversized risk rule page accepted: %v", err)
	}
}
