package admin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/jackc/pgx/v5"
)

type GovernanceReport struct {
	ID               uuid.UUID  `json:"id"`
	ReporterID       uuid.UUID  `json:"reporterId"`
	ReporterHandle   string     `json:"reporterHandle"`
	ResourceType     string     `json:"resourceType"`
	ResourceID       uuid.UUID  `json:"resourceId"`
	ResourceTitle    string     `json:"resourceTitle"`
	SubjectAuthorID  uuid.UUID  `json:"subjectAuthorId"`
	SubjectHandle    string     `json:"subjectHandle"`
	Category         string     `json:"category"`
	Details          string     `json:"details"`
	Status           string     `json:"status"`
	Outcome          *string    `json:"outcome,omitempty"`
	PreviousStatus   *string    `json:"previousStatus,omitempty"`
	ModeratorID      *uuid.UUID `json:"moderatorId,omitempty"`
	ResolutionReason *string    `json:"resolutionReason,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
	ResolvedAt       *time.Time `json:"resolvedAt,omitempty"`
}

type ReportResolution struct {
	Outcome string `json:"outcome"`
}

type GovernanceReportListInput struct {
	Query        string
	ResourceType string
	Category     string
	Status       string
	Cursor       string
	Limit        int
}

type GovernanceReportPage struct {
	Items      []GovernanceReport `json:"items"`
	NextCursor *string            `json:"nextCursor,omitempty"`
}

type GovernanceAppeal struct {
	ID               uuid.UUID  `json:"id"`
	ReportID         uuid.UUID  `json:"reportId"`
	AppellantID      uuid.UUID  `json:"appellantId"`
	AppellantHandle  string     `json:"appellantHandle"`
	ResourceType     string     `json:"resourceType"`
	ResourceID       uuid.UUID  `json:"resourceId"`
	ResourceTitle    string     `json:"resourceTitle"`
	Reason           string     `json:"reason"`
	Status           string     `json:"status"`
	ReviewerID       *uuid.UUID `json:"reviewerId,omitempty"`
	ResolutionReason *string    `json:"resolutionReason,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	ResolvedAt       *time.Time `json:"resolvedAt,omitempty"`
}

type AppealResolution struct {
	Decision string `json:"decision"`
}

type GovernanceAppealListInput struct {
	Query        string
	ResourceType string
	Status       string
	Cursor       string
	Limit        int
}

type GovernanceAppealPage struct {
	Items      []GovernanceAppeal `json:"items"`
	NextCursor *string            `json:"nextCursor,omitempty"`
}

type governanceCursor struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        uuid.UUID `json:"id"`
}

func (s *Service) ListReports(ctx context.Context, input GovernanceReportListInput) (GovernanceReportPage, error) {
	input.Query = strings.ToLower(strings.TrimSpace(input.Query))
	input.ResourceType = strings.ToLower(strings.TrimSpace(input.ResourceType))
	input.Category = strings.ToLower(strings.TrimSpace(input.Category))
	input.Status = strings.ToLower(strings.TrimSpace(input.Status))
	if len(input.Query) > 120 || (input.ResourceType != "" && !oneOf(input.ResourceType, "work", "post", "comment")) ||
		(input.Category != "" && !oneOf(input.Category, "spam", "harassment", "copyright", "sexual", "violence", "misleading", "other")) ||
		(input.Status != "" && !oneOf(input.Status, "open", "reviewing", "resolved", "dismissed")) {
		return GovernanceReportPage{}, ErrInvalidReportFilter
	}
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Limit < 1 || input.Limit > 50 {
		return GovernanceReportPage{}, ErrInvalidReportFilter
	}
	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if input.Cursor != "" {
		cursor, err := decodeGovernanceCursor(input.Cursor, ErrInvalidReportFilter)
		if err != nil {
			return GovernanceReportPage{}, err
		}
		cursorTime, cursorID = &cursor.CreatedAt, &cursor.ID
	}
	rows, err := s.pool.Query(ctx, governanceReportSelect+`
		WHERE ($1='' OR strpos(lower(reporter.handle),$1)>0 OR strpos(lower(subject.handle),$1)>0 OR strpos(lower(r.details),$1)>0 OR
			strpos(lower(CASE r.resource_type WHEN 'post' THEN COALESCE(pw.title,'Community post') WHEN 'work' THEN COALESCE(w.title,'Work') ELSE 'Comment' END),$1)>0)
		  AND ($2='' OR r.resource_type=$2)
		  AND ($3='' OR r.category=$3)
		  AND ($4='' OR r.status=$4)
		  AND ($5::timestamptz IS NULL OR (r.created_at,r.id) < ($5,$6::uuid))
		ORDER BY r.created_at DESC,r.id DESC LIMIT $7`, input.Query, input.ResourceType, input.Category, input.Status, cursorTime, cursorID, input.Limit+1)
	if err != nil {
		return GovernanceReportPage{}, fmt.Errorf("list governance reports: %w", err)
	}
	defer rows.Close()
	items := make([]GovernanceReport, 0)
	for rows.Next() {
		item, err := scanGovernanceReport(rows)
		if err != nil {
			return GovernanceReportPage{}, fmt.Errorf("scan governance report: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return GovernanceReportPage{}, err
	}
	page := GovernanceReportPage{Items: items}
	if len(page.Items) > input.Limit {
		page.Items = page.Items[:input.Limit]
		cursor := encodeGovernanceCursor(page.Items[len(page.Items)-1].CreatedAt, page.Items[len(page.Items)-1].ID)
		page.NextCursor = &cursor
	}
	return page, nil
}

func (s *Service) ResolveReport(ctx context.Context, actorID, reportID uuid.UUID, input ReportResolution, _ string) (GovernanceReport, error) {
	input.Outcome = strings.TrimSpace(strings.ToLower(input.Outcome))
	if !oneOf(input.Outcome, "no_action", "hidden", "removed") {
		return GovernanceReport{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return GovernanceReport{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var reporterID, resourceID, subjectID uuid.UUID
	var resourceType, status string
	if err := tx.QueryRow(ctx, `
		SELECT reporter_id,resource_type,resource_id,subject_author_id,status
		FROM content_reports WHERE id=$1 FOR UPDATE`, reportID).Scan(&reporterID, &resourceType, &resourceID, &subjectID, &status); errors.Is(err, pgx.ErrNoRows) {
		return GovernanceReport{}, ErrNotFound
	} else if err != nil {
		return GovernanceReport{}, err
	}
	if status != "open" && status != "reviewing" {
		return GovernanceReport{}, ErrConflict
	}

	var previousStatus *string
	if input.Outcome != "no_action" {
		value, err := moderateReportedResource(ctx, tx, resourceType, resourceID, input.Outcome)
		if err != nil {
			return GovernanceReport{}, err
		}
		previousStatus = &value
	}
	nextStatus := "resolved"
	eventKind := "resolved"
	if input.Outcome == "no_action" {
		nextStatus = "dismissed"
		eventKind = "dismissed"
	}
	if _, err := tx.Exec(ctx, `
		UPDATE content_reports
		SET status=$2,outcome=$3,previous_status=$4,moderator_id=$5,resolution_reason=$6,updated_at=now(),resolved_at=now()
		WHERE id=$1`, reportID, nextStatus, input.Outcome, previousStatus, actorID, "Administrative decision: "+input.Outcome); err != nil {
		return GovernanceReport{}, fmt.Errorf("resolve governance report: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO governance_events(report_id,actor_id,kind,from_status,to_status,reason,metadata)
		VALUES($1,$2,$3,$4,$5,$6,jsonb_build_object('outcome',$7::text))`, reportID, actorID, eventKind, status, nextStatus, "Administrative decision: "+input.Outcome, input.Outcome); err != nil {
		return GovernanceReport{}, fmt.Errorf("record governance resolution: %w", err)
	}
	if err := notifyModerationDecision(ctx, tx, reporterID, reportID, "Report reviewed", reportDecisionBody(input.Outcome, true), "reporter"); err != nil {
		return GovernanceReport{}, err
	}
	if subjectID != reporterID {
		if err := notifyModerationDecision(ctx, tx, subjectID, reportID, "Content review completed", reportDecisionBody(input.Outcome, false), "subject"); err != nil {
			return GovernanceReport{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return GovernanceReport{}, err
	}
	return s.governanceReport(ctx, reportID)
}

func (s *Service) ListAppeals(ctx context.Context, input GovernanceAppealListInput) (GovernanceAppealPage, error) {
	input.Query = strings.ToLower(strings.TrimSpace(input.Query))
	input.ResourceType = strings.ToLower(strings.TrimSpace(input.ResourceType))
	input.Status = strings.ToLower(strings.TrimSpace(input.Status))
	if len(input.Query) > 120 || (input.ResourceType != "" && !oneOf(input.ResourceType, "work", "post", "comment")) ||
		(input.Status != "" && !oneOf(input.Status, "pending", "upheld", "denied")) {
		return GovernanceAppealPage{}, ErrInvalidAppealFilter
	}
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Limit < 1 || input.Limit > 50 {
		return GovernanceAppealPage{}, ErrInvalidAppealFilter
	}
	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if input.Cursor != "" {
		cursor, err := decodeGovernanceCursor(input.Cursor, ErrInvalidAppealFilter)
		if err != nil {
			return GovernanceAppealPage{}, err
		}
		cursorTime, cursorID = &cursor.CreatedAt, &cursor.ID
	}
	rows, err := s.pool.Query(ctx, governanceAppealSelect+`
		WHERE ($1='' OR strpos(lower(u.handle),$1)>0 OR strpos(lower(a.reason),$1)>0 OR
			strpos(lower(CASE r.resource_type WHEN 'post' THEN COALESCE(pw.title,'Community post') WHEN 'work' THEN COALESCE(w.title,'Work') ELSE 'Comment' END),$1)>0)
		  AND ($2='' OR r.resource_type=$2)
		  AND ($3='' OR a.status=$3)
		  AND ($4::timestamptz IS NULL OR (a.created_at,a.id) < ($4,$5::uuid))
		ORDER BY a.created_at DESC,a.id DESC LIMIT $6`, input.Query, input.ResourceType, input.Status, cursorTime, cursorID, input.Limit+1)
	if err != nil {
		return GovernanceAppealPage{}, fmt.Errorf("list governance appeals: %w", err)
	}
	defer rows.Close()
	items := make([]GovernanceAppeal, 0)
	for rows.Next() {
		item, err := scanGovernanceAppeal(rows)
		if err != nil {
			return GovernanceAppealPage{}, fmt.Errorf("scan governance appeal: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return GovernanceAppealPage{}, err
	}
	page := GovernanceAppealPage{Items: items}
	if len(page.Items) > input.Limit {
		page.Items = page.Items[:input.Limit]
		cursor := encodeGovernanceCursor(page.Items[len(page.Items)-1].CreatedAt, page.Items[len(page.Items)-1].ID)
		page.NextCursor = &cursor
	}
	return page, nil
}

func encodeGovernanceCursor(createdAt time.Time, id uuid.UUID) string {
	body, _ := json.Marshal(governanceCursor{CreatedAt: createdAt, ID: id})
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodeGovernanceCursor(value string, invalid error) (governanceCursor, error) {
	var cursor governanceCursor
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &cursor) != nil || cursor.ID == uuid.Nil || cursor.CreatedAt.IsZero() {
		return governanceCursor{}, invalid
	}
	return cursor, nil
}

func (s *Service) ResolveAppeal(ctx context.Context, actorID, appealID uuid.UUID, input AppealResolution, _ string) (GovernanceAppeal, error) {
	input.Decision = strings.TrimSpace(strings.ToLower(input.Decision))
	if !oneOf(input.Decision, "upheld", "denied") {
		return GovernanceAppeal{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return GovernanceAppeal{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var reportID, appellantID, resourceID uuid.UUID
	var appealStatus, reportStatus, resourceType string
	var previousStatus, outcome *string
	if err := tx.QueryRow(ctx, `
		SELECT a.report_id,a.appellant_id,a.status,r.status,r.resource_type,r.resource_id,r.previous_status,r.outcome
		FROM moderation_appeals a JOIN content_reports r ON r.id=a.report_id
		WHERE a.id=$1 FOR UPDATE OF a,r`, appealID).Scan(&reportID, &appellantID, &appealStatus, &reportStatus, &resourceType, &resourceID, &previousStatus, &outcome); errors.Is(err, pgx.ErrNoRows) {
		return GovernanceAppeal{}, ErrNotFound
	} else if err != nil {
		return GovernanceAppeal{}, err
	}
	if appealStatus != "pending" || (reportStatus != "resolved" && reportStatus != "dismissed") {
		return GovernanceAppeal{}, ErrConflict
	}
	if input.Decision == "upheld" && outcome != nil && (*outcome == "hidden" || *outcome == "removed") {
		if previousStatus == nil || !oneOf(*previousStatus, "draft", "published", "hidden", "removed") {
			return GovernanceAppeal{}, ErrConflict
		}
		if err := restoreReportedResource(ctx, tx, resourceType, resourceID, *previousStatus); err != nil {
			return GovernanceAppeal{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE content_reports SET status='dismissed',outcome='no_action',updated_at=now() WHERE id=$1`, reportID); err != nil {
			return GovernanceAppeal{}, err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE moderation_appeals SET status=$2,reviewer_id=$3,resolution_reason=$4,resolved_at=now() WHERE id=$1`, appealID, input.Decision, actorID, "Administrative decision: "+input.Decision); err != nil {
		return GovernanceAppeal{}, fmt.Errorf("resolve governance appeal: %w", err)
	}
	eventKind := "appeal_" + input.Decision
	if _, err := tx.Exec(ctx, `
		INSERT INTO governance_events(report_id,appeal_id,actor_id,kind,from_status,to_status,reason)
		VALUES($1,$2,$3,$4,$5,$6,$7)`, reportID, appealID, actorID, eventKind, appealStatus, input.Decision, "Administrative decision: "+input.Decision); err != nil {
		return GovernanceAppeal{}, fmt.Errorf("record appeal resolution: %w", err)
	}
	if err := notifyModerationDecision(ctx, tx, appellantID, reportID, "Appeal reviewed", appealDecisionBody(input.Decision), "appeal:"+appealID.String()); err != nil {
		return GovernanceAppeal{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return GovernanceAppeal{}, err
	}
	return s.governanceAppeal(ctx, appealID)
}

func moderateReportedResource(ctx context.Context, tx pgx.Tx, resourceType string, resourceID uuid.UUID, status string) (string, error) {
	var previous string
	switch resourceType {
	case "post":
		var workID *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT status,work_id FROM posts WHERE id=$1 FOR UPDATE`, resourceID).Scan(&previous, &workID); errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		} else if err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, `UPDATE posts SET status=$2,updated_at=now() WHERE id=$1`, resourceID, status); err != nil {
			return "", err
		}
		if workID != nil {
			if _, err := tx.Exec(ctx, `UPDATE works SET status=$2,updated_at=now() WHERE id=$1`, *workID, status); err != nil {
				return "", err
			}
		}
	case "work":
		if err := tx.QueryRow(ctx, `SELECT status FROM works WHERE id=$1 FOR UPDATE`, resourceID).Scan(&previous); errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		} else if err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, `UPDATE works SET status=$2,updated_at=now() WHERE id=$1`, resourceID, status); err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, `UPDATE posts SET status=$2,updated_at=now() WHERE work_id=$1`, resourceID, status); err != nil {
			return "", err
		}
	case "comment":
		if err := tx.QueryRow(ctx, `SELECT status FROM comments WHERE id=$1 FOR UPDATE`, resourceID).Scan(&previous); errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		} else if err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, `UPDATE comments SET status=$2,updated_at=now() WHERE id=$1`, resourceID, status); err != nil {
			return "", err
		}
	default:
		return "", ErrInvalid
	}
	return previous, nil
}

func restoreReportedResource(ctx context.Context, tx pgx.Tx, resourceType string, resourceID uuid.UUID, status string) error {
	_, err := moderateReportedResource(ctx, tx, resourceType, resourceID, status)
	return err
}

func notifyModerationDecision(ctx context.Context, tx pgx.Tx, userID, reportID uuid.UUID, title, body, audience string) error {
	return notifications.CreateTx(ctx, tx, notifications.CreateInput{
		UserID: userID, Kind: "community.moderation", Title: title, Body: body, TargetPath: "/community",
		ResourceType: "content_report", ResourceID: &reportID, SourceKey: "moderation:" + reportID.String() + ":" + audience,
	})
}

func reportDecisionBody(outcome string, reporter bool) string {
	if reporter {
		switch outcome {
		case "hidden":
			return "Your report was reviewed and the content was hidden."
		case "removed":
			return "Your report was reviewed and the content was removed."
		default:
			return "Your report was reviewed and no content action was taken."
		}
	}
	switch outcome {
	case "hidden":
		return "Reported content was hidden after review. You may appeal this decision."
	case "removed":
		return "Reported content was removed after review. You may appeal this decision."
	default:
		return "The report was dismissed and your content remains available."
	}
}

func appealDecisionBody(decision string) string {
	if decision == "upheld" {
		return "Your appeal was upheld. Any moderation action from this report was reversed."
	}
	return "Your appeal was denied. The existing moderation decision remains in effect."
}

const governanceReportSelect = `
	SELECT r.id,r.reporter_id,reporter.handle,r.resource_type,r.resource_id,
	       CASE r.resource_type WHEN 'post' THEN COALESCE(p.title,pw.title,'Community post') WHEN 'work' THEN COALESCE(w.title,'Work') ELSE 'Comment' END,
	       r.subject_author_id,subject.handle,r.category,r.details,r.status,r.outcome,r.previous_status,r.moderator_id,
	       r.resolution_reason,r.created_at,r.updated_at,r.resolved_at
	FROM content_reports r
	JOIN users reporter ON reporter.id=r.reporter_id
	JOIN users subject ON subject.id=r.subject_author_id
	LEFT JOIN posts p ON r.resource_type='post' AND p.id=r.resource_id
	LEFT JOIN works pw ON pw.id=p.work_id
	LEFT JOIN works w ON r.resource_type='work' AND w.id=r.resource_id`

func (s *Service) governanceReport(ctx context.Context, id uuid.UUID) (GovernanceReport, error) {
	item, err := scanGovernanceReport(s.pool.QueryRow(ctx, governanceReportSelect+` WHERE r.id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return GovernanceReport{}, ErrNotFound
	}
	return item, err
}

func scanGovernanceReport(row scanner) (GovernanceReport, error) {
	var item GovernanceReport
	err := row.Scan(&item.ID, &item.ReporterID, &item.ReporterHandle, &item.ResourceType, &item.ResourceID, &item.ResourceTitle,
		&item.SubjectAuthorID, &item.SubjectHandle, &item.Category, &item.Details, &item.Status, &item.Outcome, &item.PreviousStatus,
		&item.ModeratorID, &item.ResolutionReason, &item.CreatedAt, &item.UpdatedAt, &item.ResolvedAt)
	return item, err
}

const governanceAppealSelect = `
	SELECT a.id,a.report_id,a.appellant_id,u.handle,r.resource_type,r.resource_id,
	       CASE r.resource_type WHEN 'post' THEN COALESCE(p.title,pw.title,'Community post') WHEN 'work' THEN COALESCE(w.title,'Work') ELSE 'Comment' END,
	       a.reason,a.status,a.reviewer_id,a.resolution_reason,a.created_at,a.resolved_at
	FROM moderation_appeals a
	JOIN content_reports r ON r.id=a.report_id
	JOIN users u ON u.id=a.appellant_id
	LEFT JOIN posts p ON r.resource_type='post' AND p.id=r.resource_id
	LEFT JOIN works pw ON pw.id=p.work_id
	LEFT JOIN works w ON r.resource_type='work' AND w.id=r.resource_id`

func (s *Service) governanceAppeal(ctx context.Context, id uuid.UUID) (GovernanceAppeal, error) {
	item, err := scanGovernanceAppeal(s.pool.QueryRow(ctx, governanceAppealSelect+` WHERE a.id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return GovernanceAppeal{}, ErrNotFound
	}
	return item, err
}

func scanGovernanceAppeal(row scanner) (GovernanceAppeal, error) {
	var item GovernanceAppeal
	err := row.Scan(&item.ID, &item.ReportID, &item.AppellantID, &item.AppellantHandle, &item.ResourceType, &item.ResourceID,
		&item.ResourceTitle, &item.Reason, &item.Status, &item.ReviewerID, &item.ResolutionReason, &item.CreatedAt, &item.ResolvedAt)
	return item, err
}
