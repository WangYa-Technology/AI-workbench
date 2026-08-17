package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type RiskRuleRevision struct {
	ID                     uuid.UUID  `json:"id"`
	Version                int        `json:"version"`
	ParentRevisionID       *uuid.UUID `json:"parentRevisionId,omitempty"`
	Name                   string     `json:"name"`
	TaskDisputeScore       int        `json:"taskDisputeScore"`
	TransactionRefundScore int        `json:"transactionRefundScore"`
	CommunityReportScore   int        `json:"communityReportScore"`
	MediaRejectionScore    int        `json:"mediaRejectionScore"`
	AccountLinkScore       int        `json:"accountLinkScore"`
	AccountLinkMinAccounts int        `json:"accountLinkMinAccounts"`
	AccountLinkWindowHours int        `json:"accountLinkWindowHours"`
	MediumThreshold        int        `json:"mediumThreshold"`
	HighThreshold          int        `json:"highThreshold"`
	CriticalThreshold      int        `json:"criticalThreshold"`
	Reason                 string     `json:"reason"`
	CreatedBy              *uuid.UUID `json:"createdBy,omitempty"`
	CreatedByHandle        *string    `json:"createdByHandle,omitempty"`
	CreatedAt              time.Time  `json:"createdAt"`
}

type RiskRulePolicy struct {
	Current    RiskRuleRevision   `json:"current"`
	History    []RiskRuleRevision `json:"history"`
	NextCursor *string            `json:"nextCursor,omitempty"`
}

type RiskRuleUpdate struct {
	Name                   string `json:"name"`
	TaskDisputeScore       int    `json:"taskDisputeScore"`
	TransactionRefundScore int    `json:"transactionRefundScore"`
	CommunityReportScore   int    `json:"communityReportScore"`
	MediaRejectionScore    int    `json:"mediaRejectionScore"`
	AccountLinkScore       int    `json:"accountLinkScore"`
	AccountLinkMinAccounts int    `json:"accountLinkMinAccounts"`
	AccountLinkWindowHours int    `json:"accountLinkWindowHours"`
	MediumThreshold        int    `json:"mediumThreshold"`
	HighThreshold          int    `json:"highThreshold"`
	CriticalThreshold      int    `json:"criticalThreshold"`
	Reason                 string `json:"reason"`
	ExpectedVersion        int    `json:"expectedVersion"`
	Confirmed              bool   `json:"confirmed"`
}

func (s *Service) GetRiskRulePolicy(ctx context.Context, inputs ...RevisionHistoryInput) (RiskRulePolicy, error) {
	input := RevisionHistoryInput{}
	if len(inputs) > 0 {
		input = inputs[0]
	}
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Limit < 1 || input.Limit > 50 {
		return RiskRulePolicy{}, ErrInvalidRiskRuleHistory
	}
	var cursorVersion *int
	if input.Cursor != "" {
		cursor, err := decodeRevisionHistoryCursor(input.Cursor, ErrInvalidRiskRuleHistory)
		if err != nil {
			return RiskRulePolicy{}, err
		}
		cursorVersion = &cursor.Version
	}
	var activeID uuid.UUID
	if err := s.pool.QueryRow(ctx, `SELECT active_revision_id FROM risk_rule_state WHERE singleton=true`).Scan(&activeID); errors.Is(err, pgx.ErrNoRows) {
		return RiskRulePolicy{}, ErrNotFound
	} else if err != nil {
		return RiskRulePolicy{}, err
	}
	policy := RiskRulePolicy{History: make([]RiskRuleRevision, 0)}
	err := s.pool.QueryRow(ctx, `
		SELECT r.id,r.version,r.parent_revision_id,r.name,r.task_dispute_score,r.transaction_refund_score,
		       r.community_report_score,r.media_rejection_score,r.account_link_score,r.account_link_min_accounts,r.account_link_window_hours,
		       r.medium_threshold,r.high_threshold,r.critical_threshold,r.reason,r.created_by,u.handle,r.created_at
		FROM risk_rule_revisions r LEFT JOIN users u ON u.id=r.created_by WHERE r.id=$1`, activeID).Scan(
		&policy.Current.ID, &policy.Current.Version, &policy.Current.ParentRevisionID, &policy.Current.Name, &policy.Current.TaskDisputeScore,
		&policy.Current.TransactionRefundScore, &policy.Current.CommunityReportScore, &policy.Current.MediaRejectionScore,
		&policy.Current.AccountLinkScore, &policy.Current.AccountLinkMinAccounts, &policy.Current.AccountLinkWindowHours,
		&policy.Current.MediumThreshold, &policy.Current.HighThreshold, &policy.Current.CriticalThreshold,
		&policy.Current.Reason, &policy.Current.CreatedBy, &policy.Current.CreatedByHandle, &policy.Current.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return RiskRulePolicy{}, ErrNotFound
	} else if err != nil {
		return RiskRulePolicy{}, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT r.id,r.version,r.parent_revision_id,r.name,r.task_dispute_score,r.transaction_refund_score,
		       r.community_report_score,r.media_rejection_score,r.account_link_score,r.account_link_min_accounts,r.account_link_window_hours,
		       r.medium_threshold,r.high_threshold,r.critical_threshold,r.reason,r.created_by,u.handle,r.created_at
		FROM risk_rule_revisions r LEFT JOIN users u ON u.id=r.created_by
		WHERE ($1::int IS NULL OR r.version < $1) ORDER BY r.version DESC LIMIT $2`, cursorVersion, input.Limit+1)
	if err != nil {
		return RiskRulePolicy{}, fmt.Errorf("list risk rule revisions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item RiskRuleRevision
		if err := rows.Scan(&item.ID, &item.Version, &item.ParentRevisionID, &item.Name, &item.TaskDisputeScore,
			&item.TransactionRefundScore, &item.CommunityReportScore, &item.MediaRejectionScore,
			&item.AccountLinkScore, &item.AccountLinkMinAccounts, &item.AccountLinkWindowHours,
			&item.MediumThreshold, &item.HighThreshold, &item.CriticalThreshold,
			&item.Reason, &item.CreatedBy, &item.CreatedByHandle, &item.CreatedAt); err != nil {
			return RiskRulePolicy{}, err
		}
		policy.History = append(policy.History, item)
	}
	if err := rows.Err(); err != nil {
		return policy, err
	}
	if len(policy.History) > input.Limit {
		policy.History = policy.History[:input.Limit]
		cursor := encodeRevisionHistoryCursor(policy.History[len(policy.History)-1].Version)
		policy.NextCursor = &cursor
	}
	return policy, nil
}

func (s *Service) UpdateRiskRulePolicy(ctx context.Context, actorID uuid.UUID, input RiskRuleUpdate, requestID string) (RiskRulePolicy, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Reason = strings.TrimSpace(input.Reason)
	if !input.Confirmed || input.ExpectedVersion < 1 || len(input.Name) < 3 || len(input.Name) > 80 || len(input.Reason) < 10 || len(input.Reason) > 500 ||
		input.TaskDisputeScore < 0 || input.TaskDisputeScore > 100 || input.TransactionRefundScore < 0 || input.TransactionRefundScore > 100 ||
		input.CommunityReportScore < 0 || input.CommunityReportScore > 100 || input.MediaRejectionScore < 0 || input.MediaRejectionScore > 100 ||
		input.AccountLinkScore < 0 || input.AccountLinkScore > 100 || input.AccountLinkMinAccounts < 2 || input.AccountLinkMinAccounts > 20 ||
		input.AccountLinkWindowHours < 1 || input.AccountLinkWindowHours > 168 ||
		input.MediumThreshold < 1 || input.MediumThreshold >= input.HighThreshold || input.HighThreshold >= input.CriticalThreshold || input.CriticalThreshold > 100 {
		return RiskRulePolicy{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RiskRulePolicy{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var activeID uuid.UUID
	var version int
	if err := tx.QueryRow(ctx, `SELECT active_revision_id,version FROM risk_rule_state WHERE singleton=true FOR UPDATE`).Scan(&activeID, &version); err != nil {
		return RiskRulePolicy{}, err
	}
	if version != input.ExpectedVersion {
		return RiskRulePolicy{}, ErrConflict
	}
	newID, newVersion := uuid.New(), version+1
	if _, err := tx.Exec(ctx, `
		INSERT INTO risk_rule_revisions(id,version,parent_revision_id,name,task_dispute_score,transaction_refund_score,community_report_score,media_rejection_score,account_link_score,account_link_min_accounts,account_link_window_hours,medium_threshold,high_threshold,critical_threshold,reason,created_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`, newID, newVersion, activeID, input.Name, input.TaskDisputeScore,
		input.TransactionRefundScore, input.CommunityReportScore, input.MediaRejectionScore,
		input.AccountLinkScore, input.AccountLinkMinAccounts, input.AccountLinkWindowHours,
		input.MediumThreshold, input.HighThreshold, input.CriticalThreshold, input.Reason, actorID); err != nil {
		return RiskRulePolicy{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE risk_rule_state SET active_revision_id=$1,version=$2,updated_at=now() WHERE singleton=true`, newID, newVersion); err != nil {
		return RiskRulePolicy{}, err
	}
	if err := audit(ctx, tx, actorID, "admin.risk_rules_updated", "risk_rule_revision", newID, input.Reason, requestID,
		map[string]any{"previousRevisionId": activeID, "previousVersion": version, "newVersion": newVersion}); err != nil {
		return RiskRulePolicy{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RiskRulePolicy{}, err
	}
	return s.GetRiskRulePolicy(ctx)
}
