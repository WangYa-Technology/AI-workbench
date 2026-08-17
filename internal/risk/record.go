package risk

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrInvalid = errors.New("invalid risk signal")

type SignalInput struct {
	SourceKey     string
	ResourceType  string
	ResourceID    uuid.UUID
	SubjectUserID uuid.UUID
	ActorUserID   *uuid.UUID
	SignalType    string
	Severity      string
	Score         int
	Summary       string
	Evidence      map[string]any
}

type ruleSnapshot struct {
	ID                     uuid.UUID
	Version                int
	TaskDisputeScore       int
	TransactionRefundScore int
	CommunityReportScore   int
	MediaRejectionScore    int
	AccountLinkScore       int
	AccountLinkMinAccounts int
	AccountLinkWindowHours int
	MediumThreshold        int
	HighThreshold          int
	CriticalThreshold      int
}

func RecordTx(ctx context.Context, tx pgx.Tx, input SignalInput) (uuid.UUID, error) {
	input.SourceKey = strings.TrimSpace(input.SourceKey)
	input.ResourceType = strings.TrimSpace(input.ResourceType)
	input.SignalType = strings.TrimSpace(input.SignalType)
	input.Severity = strings.TrimSpace(input.Severity)
	input.Summary = strings.TrimSpace(input.Summary)
	if tx == nil || len(input.SourceKey) < 8 || input.ResourceID == uuid.Nil || input.SubjectUserID == uuid.Nil ||
		!oneOf(input.ResourceType, "task", "order", "post", "asset", "user") ||
		!oneOf(input.SignalType, "task_dispute", "transaction_refund", "community_report", "media_rejection", "account_link") ||
		len(input.Summary) < 10 || len(input.Summary) > 240 {
		return uuid.Nil, ErrInvalid
	}
	rule, err := loadRule(ctx, tx)
	if err != nil {
		return uuid.Nil, err
	}
	input.Score = rule.TaskDisputeScore
	if input.SignalType == "transaction_refund" {
		input.Score = rule.TransactionRefundScore
	} else if input.SignalType == "community_report" {
		input.Score = rule.CommunityReportScore
	} else if input.SignalType == "media_rejection" {
		input.Score = rule.MediaRejectionScore
	} else if input.SignalType == "account_link" {
		input.Score = rule.AccountLinkScore
	}
	return recordWithRule(ctx, tx, input, rule)
}

func RecordAccountLinkTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, networkHash string) (uuid.UUID, error) {
	networkHash = strings.TrimSpace(networkHash)
	if tx == nil || userID == uuid.Nil || networkHash == "" {
		return uuid.Nil, nil
	}
	rule, err := loadRule(ctx, tx)
	if err != nil {
		return uuid.Nil, err
	}
	var linkedAccounts int
	if err := tx.QueryRow(ctx, `
		SELECT count(DISTINCT s.user_id)
		FROM sessions s JOIN users u ON u.id=s.user_id
		WHERE s.network_hash=$1 AND s.created_at>=now()-make_interval(hours=>$2) AND u.status='active'`,
		networkHash, rule.AccountLinkWindowHours).Scan(&linkedAccounts); err != nil {
		return uuid.Nil, err
	}
	if linkedAccounts < rule.AccountLinkMinAccounts {
		return uuid.Nil, nil
	}
	actorID := userID
	return recordWithRule(ctx, tx, SignalInput{
		SourceKey: "account_link:" + userID.String(), ResourceType: "user", ResourceID: userID,
		SubjectUserID: userID, ActorUserID: &actorID, SignalType: "account_link",
		Score: rule.AccountLinkScore, Summary: "Registration network threshold requires account review.",
		Evidence: map[string]any{
			"linkedAccountCount": linkedAccounts, "minimumAccounts": rule.AccountLinkMinAccounts,
			"windowHours": rule.AccountLinkWindowHours, "networkDataStored": false,
		},
	}, rule)
}

func loadRule(ctx context.Context, tx pgx.Tx) (ruleSnapshot, error) {
	var rule ruleSnapshot
	err := tx.QueryRow(ctx, `
		SELECT r.id,r.version,r.task_dispute_score,r.transaction_refund_score,r.community_report_score,r.media_rejection_score,
		       r.account_link_score,r.account_link_min_accounts,r.account_link_window_hours,
		       r.medium_threshold,r.high_threshold,r.critical_threshold
		FROM risk_rule_state s JOIN risk_rule_revisions r ON r.id=s.active_revision_id WHERE s.singleton=true`).Scan(
		&rule.ID, &rule.Version, &rule.TaskDisputeScore, &rule.TransactionRefundScore, &rule.CommunityReportScore, &rule.MediaRejectionScore,
		&rule.AccountLinkScore, &rule.AccountLinkMinAccounts, &rule.AccountLinkWindowHours,
		&rule.MediumThreshold, &rule.HighThreshold, &rule.CriticalThreshold)
	return rule, err
}

func recordWithRule(ctx context.Context, tx pgx.Tx, input SignalInput, rule ruleSnapshot) (uuid.UUID, error) {
	input.Severity = "low"
	if input.Score >= rule.CriticalThreshold {
		input.Severity = "critical"
	} else if input.Score >= rule.HighThreshold {
		input.Severity = "high"
	} else if input.Score >= rule.MediumThreshold {
		input.Severity = "medium"
	}
	evidenceWithRule := make(map[string]any, len(input.Evidence)+2)
	for key, value := range input.Evidence {
		evidenceWithRule[key] = value
	}
	evidenceWithRule["riskRuleRevisionId"] = rule.ID
	evidenceWithRule["riskRuleVersion"] = rule.Version
	input.Evidence = evidenceWithRule
	evidence, err := json.Marshal(input.Evidence)
	if err != nil {
		return uuid.Nil, ErrInvalid
	}
	var id uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO risk_signals(source_key,resource_type,resource_id,subject_user_id,actor_user_id,signal_type,severity,score,summary,evidence)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (source_key) DO NOTHING RETURNING id`, input.SourceKey, input.ResourceType, input.ResourceID,
		input.SubjectUserID, input.ActorUserID, input.SignalType, input.Severity, input.Score, input.Summary, evidence).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.QueryRow(ctx, `SELECT id FROM risk_signals WHERE source_key=$1`, input.SourceKey).Scan(&id); err != nil {
			return uuid.Nil, err
		}
		return id, nil
	}
	if err != nil {
		return uuid.Nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO risk_events(signal_id,actor_id,kind,to_status,reason,metadata)
		VALUES($1,$2,'detected','open',$3,jsonb_build_object('signalType',$4::text,'score',$5::integer))`,
		id, input.ActorUserID, input.Summary, input.SignalType, input.Score); err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

func oneOf(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}
