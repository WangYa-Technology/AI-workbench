package admin_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/risk"
)

func TestRiskRuleRevisionsControlSubsequentSignals(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	service := admin.NewService(pool, true)

	policy, err := service.GetRiskRulePolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if policy.Current.Version != 1 || policy.Current.TaskDisputeScore != 85 || policy.Current.TransactionRefundScore != 55 ||
		policy.Current.CommunityReportScore != 35 || policy.Current.MediaRejectionScore != 75 ||
		policy.Current.AccountLinkScore != 65 || policy.Current.AccountLinkMinAccounts != 3 || policy.Current.AccountLinkWindowHours != 24 ||
		policy.Current.MediumThreshold != 40 || policy.Current.HighThreshold != 70 || policy.Current.CriticalThreshold != 90 || len(policy.History) != 1 {
		t.Fatalf("initial policy does not preserve verified behavior: %#v", policy)
	}

	adminID, subjectID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role,status) VALUES
		($1,$2,$3,'Risk Rule Admin','admin','active'),
		($4,$5,$6,'Risk Subject','creator','active')`,
		adminID, adminID.String()+"@test.local", "rule_admin_"+adminID.String()[:8],
		subjectID, subjectID.String()+"@test.local", "rule_subject_"+subjectID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	valid := admin.RiskRuleUpdate{
		Name: "Higher dispute sensitivity", TaskDisputeScore: 94, TransactionRefundScore: 48,
		CommunityReportScore: 42, MediaRejectionScore: 78,
		AccountLinkScore: 68, AccountLinkMinAccounts: 4, AccountLinkWindowHours: 36,
		MediumThreshold: 35, HighThreshold: 65, CriticalThreshold: 90,
		Reason: "Raise disputed-task visibility while retaining bounded refund monitoring.", ExpectedVersion: 1, Confirmed: true,
	}
	invalid := valid
	invalid.MediumThreshold, invalid.HighThreshold = 70, 60
	if _, err := service.UpdateRiskRulePolicy(ctx, adminID, invalid, "risk-rules-invalid"); !errors.Is(err, admin.ErrInvalid) {
		t.Fatalf("unordered thresholds were accepted: %v", err)
	}
	stale := valid
	stale.ExpectedVersion = 2
	if _, err := service.UpdateRiskRulePolicy(ctx, adminID, stale, "risk-rules-stale"); !errors.Is(err, admin.ErrConflict) {
		t.Fatalf("stale expected version did not conflict: %v", err)
	}
	updated, err := service.UpdateRiskRulePolicy(ctx, adminID, valid, "risk-rules-update")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Current.Version != 2 || updated.Current.ParentRevisionID == nil || *updated.Current.ParentRevisionID != policy.Current.ID ||
		updated.Current.TaskDisputeScore != 94 || updated.Current.CommunityReportScore != 42 || updated.Current.MediaRejectionScore != 78 ||
		updated.Current.AccountLinkScore != 68 || updated.Current.AccountLinkMinAccounts != 4 || updated.Current.AccountLinkWindowHours != 36 || len(updated.History) != 2 {
		t.Fatalf("unexpected activated risk rule revision: %#v", updated)
	}
	if _, err := pool.Exec(ctx, `UPDATE risk_rule_revisions SET task_dispute_score=1 WHERE id=$1`, updated.Current.ID); err == nil {
		t.Fatal("risk rule revision update was not rejected")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM risk_rule_revisions WHERE id=$1`, policy.Current.ID); err == nil {
		t.Fatal("risk rule revision delete was not rejected")
	}

	resourceID := uuid.New()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	signalID, err := risk.RecordTx(ctx, tx, risk.SignalInput{
		SourceKey: "risk-rule-test:" + resourceID.String(), ResourceType: "task", ResourceID: resourceID,
		SubjectUserID: subjectID, ActorUserID: &subjectID, SignalType: "task_dispute",
		Summary: "A subsequent task dispute must use the active immutable rule revision.", Evidence: map[string]any{"source": "test"},
	})
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var score int
	var severity, revisionID string
	var revisionVersion int
	if err := pool.QueryRow(ctx, `
		SELECT score,severity,evidence->>'riskRuleRevisionId',(evidence->>'riskRuleVersion')::integer
		FROM risk_signals WHERE id=$1`, signalID).Scan(&score, &severity, &revisionID, &revisionVersion); err != nil {
		t.Fatal(err)
	}
	if score != 94 || severity != "critical" || revisionID != updated.Current.ID.String() || revisionVersion != 2 {
		t.Fatalf("signal did not preserve active rule evidence: score=%d severity=%s revision=%s version=%d", score, severity, revisionID, revisionVersion)
	}
	var auditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='admin.risk_rules_updated' AND resource_id=$1 AND request_id='risk-rules-update'`, updated.Current.ID).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("risk rule audit evidence mismatch: count=%d err=%v", auditCount, err)
	}
}
