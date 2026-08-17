package admin_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/admin"
)

func TestAdminGovernanceInventoriesTraverseBeyondLegacyWindow(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	administratorID, subjectID, appellantID, targetReporterID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role,status,created_at) VALUES
		($1,$2,$3,'Governance Administrator','admin','active',now() - interval '5 hours'),
		($4,$5,$6,'Governance Subject','creator','active',now() - interval '5 hours'),
		($7,$8,$9,'Governance Appellant','creator','active',now() - interval '5 hours'),
		($10,$11,$12,'Governance Target Reporter','member','active',now() - interval '5 hours')`,
		administratorID, administratorID.String()+"@test.local", "gov_admin_"+administratorID.String()[:8],
		subjectID, subjectID.String()+"@test.local", "gov_subject_"+subjectID.String()[:8],
		appellantID, appellantID.String()+"@test.local", "gov_appellant_"+appellantID.String()[:8],
		targetReporterID, targetReporterID.String()+"@test.local", "gov_target_reporter_"+targetReporterID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(email,handle,display_name,role,status,created_at)
		SELECT 'gov-pressure-'||value||'@test.local','gov_pressure_'||value,'Governance Pressure '||value,'member','active',now() - interval '4 hours'
		FROM generate_series(1,205) value`); err != nil {
		t.Fatal(err)
	}

	assetID, workID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,created_at)
		VALUES($1,$2,'image','Governance source','/media/governance-source.jpg','image/jpeg','clean','demo','demo',now() - interval '5 hours')`, assetID, subjectID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO works(id,author_id,asset_id,title,summary,model_name,status,ai_disclosure,published_at,created_at,updated_at)
		VALUES($1,$2,$3,'Governance backlog target','Governance backlog evidence','Local Test','published','Governance backlog disclosure',now() - interval '4 hours',now() - interval '4 hours',now() - interval '4 hours')`, workID, subjectID, assetID); err != nil {
		t.Fatal(err)
	}

	targetReportID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO content_reports(id,reporter_id,resource_type,resource_id,subject_author_id,category,details,status,created_at,updated_at)
		VALUES($1,$2,'work',$3,$4,'spam','Governance backlog target details','open',now() - interval '3 hours',now() - interval '3 hours')`, targetReportID, targetReporterID, workID, subjectID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO content_reports(reporter_id,resource_type,resource_id,subject_author_id,category,details,status,created_at,updated_at)
		SELECT id,'work',$1,$2,'spam','Governance backlog pressure details','open',now() - interval '1 hour',now() - interval '1 hour'
		FROM users WHERE handle LIKE 'gov_pressure_%'`, workID, subjectID); err != nil {
		t.Fatal(err)
	}

	service := admin.NewService(pool, true)
	reportInput := admin.GovernanceReportListInput{Query: "governance backlog", ResourceType: "work", Category: "spam", Status: "open", Limit: 50}
	seenReports := map[uuid.UUID]bool{}
	foundReport := false
	for {
		page, err := service.ListReports(ctx, reportInput)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if seenReports[item.ID] {
				t.Fatalf("report repeated across cursor pages: %s", item.ID)
			}
			seenReports[item.ID] = true
			foundReport = foundReport || item.ID == targetReportID
		}
		if page.NextCursor == nil {
			break
		}
		reportInput.Cursor = *page.NextCursor
	}
	if len(seenReports) != 206 || !foundReport {
		t.Fatalf("incomplete report traversal: count=%d target=%t", len(seenReports), foundReport)
	}
	if _, err := service.ListReports(ctx, admin.GovernanceReportListInput{Cursor: "modified", Limit: 20}); !errors.Is(err, admin.ErrInvalidReportFilter) {
		t.Fatalf("modified report cursor was accepted: %v", err)
	}
	resolvedReport, err := service.ResolveReport(ctx, administratorID, targetReportID, admin.ReportResolution{
		Outcome: "no_action", Reason: "Verified older report evidence beyond the legacy governance window.", Confirmed: true,
	}, "governance-backlog-report")
	if err != nil || resolvedReport.ID != targetReportID || resolvedReport.Status != "dismissed" {
		t.Fatalf("exact older report resolution response failed: %#v %v", resolvedReport, err)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE content_reports r SET status='resolved',outcome='hidden',previous_status='published',moderator_id=$1,
		resolution_reason='Prepared bounded appeal backlog evidence.',resolved_at=now(),updated_at=now()
		FROM users reporter WHERE reporter.id=r.reporter_id AND reporter.handle LIKE 'gov_pressure_%'`, administratorID); err != nil {
		t.Fatal(err)
	}
	targetAppealID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO moderation_appeals(id,report_id,appellant_id,reason,status,created_at)
		VALUES($1,$2,$3,'Governance backlog target appeal','pending',now() - interval '3 hours')`, targetAppealID, targetReportID, appellantID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO moderation_appeals(report_id,appellant_id,reason,status,created_at)
		SELECT r.id,$1,'Governance backlog pressure appeal','pending',now() - interval '1 hour'
		FROM content_reports r JOIN users reporter ON reporter.id=r.reporter_id WHERE reporter.handle LIKE 'gov_pressure_%'`, appellantID); err != nil {
		t.Fatal(err)
	}

	appealInput := admin.GovernanceAppealListInput{Query: "governance backlog", ResourceType: "work", Status: "pending", Limit: 50}
	seenAppeals := map[uuid.UUID]bool{}
	foundAppeal := false
	for {
		page, err := service.ListAppeals(ctx, appealInput)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if seenAppeals[item.ID] {
				t.Fatalf("appeal repeated across cursor pages: %s", item.ID)
			}
			seenAppeals[item.ID] = true
			foundAppeal = foundAppeal || item.ID == targetAppealID
		}
		if page.NextCursor == nil {
			break
		}
		appealInput.Cursor = *page.NextCursor
	}
	if len(seenAppeals) != 206 || !foundAppeal {
		t.Fatalf("incomplete appeal traversal: count=%d target=%t", len(seenAppeals), foundAppeal)
	}
	if _, err := service.ListAppeals(ctx, admin.GovernanceAppealListInput{Cursor: "modified", Limit: 20}); !errors.Is(err, admin.ErrInvalidAppealFilter) {
		t.Fatalf("modified appeal cursor was accepted: %v", err)
	}
	resolvedAppeal, err := service.ResolveAppeal(ctx, administratorID, targetAppealID, admin.AppealResolution{
		Decision: "denied", Reason: "Verified older appeal evidence beyond the legacy governance window.", Confirmed: true,
	}, "governance-backlog-appeal")
	if err != nil || resolvedAppeal.ID != targetAppealID || resolvedAppeal.Status != "denied" || resolvedAppeal.ResolvedAt == nil {
		t.Fatalf("exact older appeal resolution response failed: %#v %v", resolvedAppeal, err)
	}
}
