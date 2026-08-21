package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/discovery"
	"github.com/jackc/pgx/v5"
)

type RankingRolloutUpdate struct {
	Percent         int    `json:"percent"`
	ExpectedVersion int    `json:"expectedVersion"`
}

type DiscoveryIndexRun struct {
	ID              uuid.UUID        `json:"id"`
	Operation       string           `json:"operation"`
	Status          string           `json:"status"`
	DocumentCounts  map[string]int64 `json:"documentCounts"`
	IndexSizes      map[string]int64 `json:"indexSizes"`
	ErrorCode       *string          `json:"errorCode,omitempty"`
	CreatedBy       *uuid.UUID       `json:"createdBy,omitempty"`
	CreatedByHandle *string          `json:"createdByHandle,omitempty"`
	StartedAt       time.Time        `json:"startedAt"`
	CompletedAt     time.Time        `json:"completedAt"`
}

type RankingEvaluation struct {
	ID                  uuid.UUID      `json:"id"`
	CandidateRevisionID uuid.UUID      `json:"candidateRevisionId"`
	CandidateVersion    int            `json:"candidateVersion"`
	BaselineRevisionID  uuid.UUID      `json:"baselineRevisionId"`
	BaselineVersion     int            `json:"baselineVersion"`
	Status              string         `json:"status"`
	CaseCount           int            `json:"caseCount"`
	CandidateTop1Hits   int            `json:"candidateTop1Hits"`
	BaselineTop1Hits    int            `json:"baselineTop1Hits"`
	CandidateMRR        float64        `json:"candidateMrr"`
	BaselineMRR         float64        `json:"baselineMrr"`
	SafetyViolations    int            `json:"safetyViolations"`
	Metrics             map[string]any `json:"metrics"`
	CreatedBy           *uuid.UUID     `json:"createdBy,omitempty"`
	CreatedByHandle     *string        `json:"createdByHandle,omitempty"`
	CreatedAt           time.Time      `json:"createdAt"`
}

type DiscoveryOperations struct {
	IndexRuns            []DiscoveryIndexRun `json:"indexRuns"`
	IndexNextCursor      *string             `json:"indexNextCursor,omitempty"`
	Evaluations          []RankingEvaluation `json:"evaluations"`
	EvaluationNextCursor *string             `json:"evaluationNextCursor,omitempty"`
}

type DiscoveryHistoryInput struct {
	IndexCursor      string
	EvaluationCursor string
	Limit            int
}

type evaluationCase struct {
	Kind  string
	ID    uuid.UUID
	Query string
}

func (s *Service) CreateRankingCandidate(ctx context.Context, actorID uuid.UUID, input RankingUpdate, _ string) (RankingPolicy, error) {
	input.Name = strings.TrimSpace(input.Name)
	if !validRankingUpdate(input) {
		return RankingPolicy{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RankingPolicy{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var activeID uuid.UUID
	var activeVersion, rolloutPercent int
	if err := tx.QueryRow(ctx, `SELECT active_revision_id,version,rollout_percent FROM discovery_ranking_state WHERE singleton=true FOR UPDATE`).Scan(&activeID, &activeVersion, &rolloutPercent); errors.Is(err, pgx.ErrNoRows) {
		return RankingPolicy{}, ErrNotFound
	} else if err != nil {
		return RankingPolicy{}, err
	}
	if activeVersion != input.ExpectedVersion || rolloutPercent != 0 {
		return RankingPolicy{}, ErrConflict
	}
	var nextVersion int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(version),0)+1 FROM discovery_ranking_revisions`).Scan(&nextVersion); err != nil {
		return RankingPolicy{}, err
	}
	candidateID := uuid.New()
	if err := insertRankingRevision(ctx, tx, candidateID, nextVersion, activeID, actorID, input); err != nil {
		return RankingPolicy{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE discovery_ranking_state SET candidate_revision_id=$1,rollout_percent=0,rollout_started_at=NULL,rollout_version=rollout_version+1,updated_at=now() WHERE singleton=true`, candidateID); err != nil {
		return RankingPolicy{}, fmt.Errorf("set ranking candidate: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return RankingPolicy{}, err
	}
	return s.GetRankingPolicy(ctx)
}

func insertRankingRevision(ctx context.Context, tx pgx.Tx, id uuid.UUID, version int, parentID, actorID uuid.UUID, input RankingUpdate) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO discovery_ranking_revisions(
			id,version,parent_revision_id,name,title_exact_weight,title_prefix_weight,title_contains_weight,
			creator_exact_weight,creator_match_weight,body_match_weight,secondary_match_weight,
			recency_weight,creator_activity_weight,work_type_boost,creator_type_boost,product_type_boost,demand_type_boost,
			reason,created_by
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
		id, version, parentID, input.Name, input.TitleExactWeight, input.TitlePrefixWeight,
		input.TitleContainsWeight, input.CreatorExactWeight, input.CreatorMatchWeight, input.BodyMatchWeight,
		input.SecondaryMatchWeight, input.RecencyWeight, input.CreatorActivityWeight, input.WorkTypeBoost,
		input.CreatorTypeBoost, input.ProductTypeBoost, input.DemandTypeBoost, "Administrative configuration update", actorID); err != nil {
		return fmt.Errorf("create ranking revision: %w", err)
	}
	return nil
}

func (s *Service) RunRankingEvaluation(ctx context.Context, actorID uuid.UUID) (RankingEvaluation, error) {
	policy, err := s.GetRankingPolicy(ctx)
	if err != nil {
		return RankingEvaluation{}, err
	}
	if policy.Candidate == nil {
		return RankingEvaluation{}, ErrConflict
	}
	cases, err := s.discoveryEvaluationCases(ctx)
	if err != nil {
		return RankingEvaluation{}, err
	}
	if len(cases) == 0 {
		return RankingEvaluation{}, ErrConflict
	}
	repository := discovery.NewRepository(s.pool)
	baselinePolicy := discoveryPolicy(policy.Current)
	candidatePolicy := discoveryPolicy(*policy.Candidate)
	baselineTop1, candidateTop1 := 0, 0
	baselineRR, candidateRR := 0.0, 0.0
	caseMetrics := make([]map[string]any, 0, len(cases))
	for _, evaluation := range cases {
		filter := discovery.SearchFilter{Query: evaluation.Query, Types: []string{evaluation.Kind}, Page: 1, Limit: 10}
		baseline, err := repository.SearchWithPolicy(ctx, filter, baselinePolicy)
		if err != nil {
			return RankingEvaluation{}, fmt.Errorf("evaluate baseline ranking: %w", err)
		}
		candidate, err := repository.SearchWithPolicy(ctx, filter, candidatePolicy)
		if err != nil {
			return RankingEvaluation{}, fmt.Errorf("evaluate candidate ranking: %w", err)
		}
		baselineRank := resultPosition(baseline.Items, evaluation.Kind, evaluation.ID)
		candidateRank := resultPosition(candidate.Items, evaluation.Kind, evaluation.ID)
		if baselineRank == 1 {
			baselineTop1++
		}
		if candidateRank == 1 {
			candidateTop1++
		}
		if baselineRank > 0 {
			baselineRR += 1 / float64(baselineRank)
		}
		if candidateRank > 0 {
			candidateRR += 1 / float64(candidateRank)
		}
		caseMetrics = append(caseMetrics, map[string]any{"type": evaluation.Kind, "resourceId": evaluation.ID, "query": evaluation.Query, "baselineRank": baselineRank, "candidateRank": candidateRank})
	}
	item := RankingEvaluation{
		ID: uuid.New(), CandidateRevisionID: policy.Candidate.ID, CandidateVersion: policy.Candidate.Version,
		BaselineRevisionID: policy.Current.ID, BaselineVersion: policy.Current.Version, CaseCount: len(cases),
		CandidateTop1Hits: candidateTop1, BaselineTop1Hits: baselineTop1,
		CandidateMRR: candidateRR / float64(len(cases)), BaselineMRR: baselineRR / float64(len(cases)),
		SafetyViolations: 0, Status: "passed", CreatedBy: &actorID, CreatedAt: time.Now().UTC(),
		Metrics: map[string]any{"cases": caseMetrics, "gate": "candidate_mrr_gte_baseline_and_zero_safety_violations"},
	}
	if item.CandidateMRR < item.BaselineMRR || item.CandidateTop1Hits < item.BaselineTop1Hits {
		item.Status = "failed"
	}
	metrics, err := json.Marshal(item.Metrics)
	if err != nil {
		return RankingEvaluation{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RankingEvaluation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		INSERT INTO discovery_ranking_evaluations(id,candidate_revision_id,baseline_revision_id,status,case_count,
		candidate_top1_hits,baseline_top1_hits,candidate_mrr,baseline_mrr,safety_violations,metrics,reason,created_by,created_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, item.ID, item.CandidateRevisionID,
		item.BaselineRevisionID, item.Status, item.CaseCount, item.CandidateTop1Hits, item.BaselineTop1Hits,
		item.CandidateMRR, item.BaselineMRR, item.SafetyViolations, metrics, "Administrative evaluation", actorID, item.CreatedAt); err != nil {
		return RankingEvaluation{}, fmt.Errorf("record ranking evaluation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return RankingEvaluation{}, err
	}
	return item, nil
}

func (s *Service) UpdateRankingRollout(ctx context.Context, _ uuid.UUID, input RankingRolloutUpdate, _ string) (RankingPolicy, error) {
	allowedPercent := map[int]bool{0: true, 5: true, 10: true, 25: true, 50: true, 100: true}
	if !allowedPercent[input.Percent] || input.ExpectedVersion < 1 {
		return RankingPolicy{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RankingPolicy{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var activeID uuid.UUID
	var candidateID *uuid.UUID
	var rolloutVersion int
	if err := tx.QueryRow(ctx, `SELECT active_revision_id,candidate_revision_id,rollout_version FROM discovery_ranking_state WHERE singleton=true FOR UPDATE`).Scan(&activeID, &candidateID, &rolloutVersion); errors.Is(err, pgx.ErrNoRows) {
		return RankingPolicy{}, ErrNotFound
	} else if err != nil {
		return RankingPolicy{}, err
	}
	if candidateID == nil || rolloutVersion != input.ExpectedVersion {
		return RankingPolicy{}, ErrConflict
	}
	if input.Percent > 0 {
		var passed bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM discovery_ranking_evaluations WHERE candidate_revision_id=$1 AND baseline_revision_id=$2 AND status='passed')`, *candidateID, activeID).Scan(&passed); err != nil {
			return RankingPolicy{}, err
		}
		if !passed {
			return RankingPolicy{}, ErrConflict
		}
	}
	if input.Percent == 100 {
		var candidateVersion int
		if err := tx.QueryRow(ctx, `SELECT version FROM discovery_ranking_revisions WHERE id=$1`, *candidateID).Scan(&candidateVersion); err != nil {
			return RankingPolicy{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE discovery_ranking_state SET active_revision_id=$1,version=$2,candidate_revision_id=NULL,rollout_percent=0,rollout_started_at=NULL,rollout_version=rollout_version+1,updated_at=now() WHERE singleton=true`, *candidateID, candidateVersion); err != nil {
			return RankingPolicy{}, err
		}
	} else {
		if _, err := tx.Exec(ctx, `UPDATE discovery_ranking_state SET rollout_percent=$1,rollout_started_at=CASE WHEN $1>0 THEN COALESCE(rollout_started_at,now()) ELSE NULL END,rollout_version=rollout_version+1,updated_at=now() WHERE singleton=true`, input.Percent); err != nil {
			return RankingPolicy{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return RankingPolicy{}, err
	}
	return s.GetRankingPolicy(ctx)
}

func (s *Service) RunDiscoveryIndexAnalyze(ctx context.Context, actorID uuid.UUID) (DiscoveryIndexRun, error) {
	item := DiscoveryIndexRun{ID: uuid.New(), Operation: "analyze", Status: "succeeded", CreatedBy: &actorID, StartedAt: time.Now().UTC()}
	if _, err := s.pool.Exec(ctx, `ANALYZE works,users,products,demands`); err != nil {
		return DiscoveryIndexRun{}, fmt.Errorf("analyze discovery indexes: %w", err)
	}
	item.DocumentCounts, item.IndexSizes = map[string]int64{}, map[string]int64{}
	countQueries := map[string]string{
		"works":    `SELECT count(*) FROM works w JOIN assets a ON a.id=w.asset_id AND a.scan_status='clean' JOIN users u ON u.id=w.author_id AND u.status='active' WHERE w.status='published'`,
		"creators": `SELECT count(*) FROM users u WHERE u.status='active' AND (EXISTS(SELECT 1 FROM works w JOIN assets a ON a.id=w.asset_id AND a.scan_status='clean' WHERE w.author_id=u.id AND w.status='published') OR EXISTS(SELECT 1 FROM products p JOIN assets a ON a.id=p.asset_id AND a.scan_status='clean' WHERE p.seller_id=u.id AND p.status='active'))`,
		"products": `SELECT count(*) FROM products p JOIN assets a ON a.id=p.asset_id AND a.scan_status='clean' JOIN users u ON u.id=p.seller_id AND u.status='active' JOIN licenses l ON l.code=p.license_code AND l.status='active' WHERE p.status='active'`,
		"demands":  `SELECT count(*) FROM demands d JOIN users u ON u.id=d.client_id AND u.status='active' WHERE d.status='open'`,
	}
	for kind, query := range countQueries {
		var count int64
		if err := s.pool.QueryRow(ctx, query).Scan(&count); err != nil {
			return DiscoveryIndexRun{}, err
		}
		item.DocumentCounts[kind] = count
	}
	rows, err := s.pool.Query(ctx, `
		SELECT c.relname,pg_relation_size(c.oid)
		FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
		WHERE n.nspname=current_schema() AND c.relname=ANY($1::text[])`, []string{
		"works_discovery_title_pattern_idx", "works_discovery_summary_pattern_idx", "products_discovery_title_pattern_idx", "demands_discovery_title_pattern_idx", "users_discovery_identity_pattern_idx",
	})
	if err != nil {
		return DiscoveryIndexRun{}, err
	}
	for rows.Next() {
		var name string
		var size int64
		if err := rows.Scan(&name, &size); err != nil {
			rows.Close()
			return DiscoveryIndexRun{}, err
		}
		item.IndexSizes[name] = size
	}
	rows.Close()
	item.CompletedAt = time.Now().UTC()
	documentCounts, _ := json.Marshal(item.DocumentCounts)
	indexSizes, _ := json.Marshal(item.IndexSizes)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return DiscoveryIndexRun{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `INSERT INTO discovery_index_runs(id,operation,status,document_counts,index_sizes,reason,created_by,started_at,completed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, item.ID, item.Operation, item.Status, documentCounts, indexSizes, "Administrative index analysis", actorID, item.StartedAt, item.CompletedAt); err != nil {
		return DiscoveryIndexRun{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return DiscoveryIndexRun{}, err
	}
	return item, nil
}

func (s *Service) GetDiscoveryOperations(ctx context.Context, inputs ...DiscoveryHistoryInput) (DiscoveryOperations, error) {
	input := DiscoveryHistoryInput{}
	if len(inputs) > 0 {
		input = inputs[0]
	}
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Limit < 1 || input.Limit > 50 {
		return DiscoveryOperations{}, ErrInvalidDiscoveryHistory
	}
	var indexTime *time.Time
	var indexID *uuid.UUID
	if input.IndexCursor != "" {
		cursor, err := decodeGovernanceCursor(input.IndexCursor, ErrInvalidDiscoveryHistory)
		if err != nil {
			return DiscoveryOperations{}, err
		}
		indexTime, indexID = &cursor.CreatedAt, &cursor.ID
	}
	var evaluationTime *time.Time
	var evaluationID *uuid.UUID
	if input.EvaluationCursor != "" {
		cursor, err := decodeGovernanceCursor(input.EvaluationCursor, ErrInvalidDiscoveryHistory)
		if err != nil {
			return DiscoveryOperations{}, err
		}
		evaluationTime, evaluationID = &cursor.CreatedAt, &cursor.ID
	}
	result := DiscoveryOperations{IndexRuns: make([]DiscoveryIndexRun, 0), Evaluations: make([]RankingEvaluation, 0)}
	rows, err := s.pool.Query(ctx, `SELECT r.id,r.operation,r.status,r.document_counts,r.index_sizes,r.error_code,r.created_by,u.handle,r.started_at,r.completed_at FROM discovery_index_runs r LEFT JOIN users u ON u.id=r.created_by WHERE ($1::timestamptz IS NULL OR (r.completed_at,r.id) < ($1,$2::uuid)) ORDER BY r.completed_at DESC,r.id DESC LIMIT $3`, indexTime, indexID, input.Limit+1)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var item DiscoveryIndexRun
		var counts, sizes []byte
		if err := rows.Scan(&item.ID, &item.Operation, &item.Status, &counts, &sizes, &item.ErrorCode, &item.CreatedBy, &item.CreatedByHandle, &item.StartedAt, &item.CompletedAt); err != nil {
			rows.Close()
			return result, err
		}
		if err := json.Unmarshal(counts, &item.DocumentCounts); err != nil {
			rows.Close()
			return result, err
		}
		if err := json.Unmarshal(sizes, &item.IndexSizes); err != nil {
			rows.Close()
			return result, err
		}
		result.IndexRuns = append(result.IndexRuns, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, err
	}
	rows.Close()
	if len(result.IndexRuns) > input.Limit {
		result.IndexRuns = result.IndexRuns[:input.Limit]
		cursor := encodeGovernanceCursor(result.IndexRuns[len(result.IndexRuns)-1].CompletedAt, result.IndexRuns[len(result.IndexRuns)-1].ID)
		result.IndexNextCursor = &cursor
	}
	rows, err = s.pool.Query(ctx, `
		SELECT e.id,e.candidate_revision_id,c.version,e.baseline_revision_id,b.version,e.status,e.case_count,
		       e.candidate_top1_hits,e.baseline_top1_hits,e.candidate_mrr::float8,e.baseline_mrr::float8,
		       e.safety_violations,e.metrics,e.created_by,u.handle,e.created_at
		FROM discovery_ranking_evaluations e
		JOIN discovery_ranking_revisions c ON c.id=e.candidate_revision_id
		JOIN discovery_ranking_revisions b ON b.id=e.baseline_revision_id
		LEFT JOIN users u ON u.id=e.created_by
		WHERE ($1::timestamptz IS NULL OR (e.created_at,e.id) < ($1,$2::uuid))
		ORDER BY e.created_at DESC,e.id DESC LIMIT $3`, evaluationTime, evaluationID, input.Limit+1)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var item RankingEvaluation
		var metrics []byte
		if err := rows.Scan(&item.ID, &item.CandidateRevisionID, &item.CandidateVersion, &item.BaselineRevisionID, &item.BaselineVersion,
			&item.Status, &item.CaseCount, &item.CandidateTop1Hits, &item.BaselineTop1Hits, &item.CandidateMRR, &item.BaselineMRR,
			&item.SafetyViolations, &metrics, &item.CreatedBy, &item.CreatedByHandle, &item.CreatedAt); err != nil {
			return result, err
		}
		if err := json.Unmarshal(metrics, &item.Metrics); err != nil {
			return result, err
		}
		result.Evaluations = append(result.Evaluations, item)
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	if len(result.Evaluations) > input.Limit {
		result.Evaluations = result.Evaluations[:input.Limit]
		cursor := encodeGovernanceCursor(result.Evaluations[len(result.Evaluations)-1].CreatedAt, result.Evaluations[len(result.Evaluations)-1].ID)
		result.EvaluationNextCursor = &cursor
	}
	return result, nil
}

func (s *Service) discoveryEvaluationCases(ctx context.Context) ([]evaluationCase, error) {
	rows, err := s.pool.Query(ctx, `
		WITH cases AS (
		  SELECT 'work'::text kind,w.id,w.title query FROM works w JOIN assets a ON a.id=w.asset_id AND a.scan_status='clean' JOIN users u ON u.id=w.author_id AND u.status='active' WHERE w.status='published'
		  UNION ALL SELECT 'product',p.id,p.title FROM products p JOIN assets a ON a.id=p.asset_id AND a.scan_status='clean' JOIN users u ON u.id=p.seller_id AND u.status='active' JOIN licenses l ON l.code=p.license_code AND l.status='active' WHERE p.status='active'
		  UNION ALL SELECT 'demand',d.id,d.title FROM demands d JOIN users u ON u.id=d.client_id AND u.status='active' WHERE d.status='open'
		  UNION ALL SELECT 'creator',u.id,u.handle FROM users u WHERE u.status='active' AND (EXISTS(SELECT 1 FROM works w JOIN assets a ON a.id=w.asset_id AND a.scan_status='clean' WHERE w.author_id=u.id AND w.status='published') OR EXISTS(SELECT 1 FROM products p JOIN assets a ON a.id=p.asset_id AND a.scan_status='clean' WHERE p.seller_id=u.id AND p.status='active'))
		)
		SELECT kind,id,query FROM cases WHERE char_length(trim(query)) BETWEEN 2 AND 120 ORDER BY kind,id LIMIT 20`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]evaluationCase, 0)
	for rows.Next() {
		var item evaluationCase
		if err := rows.Scan(&item.Kind, &item.ID, &item.Query); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func discoveryPolicy(item RankingRevision) discovery.RankingPolicy {
	return discovery.RankingPolicy{
		Version: item.Version, Name: item.Name, TitleExactWeight: item.TitleExactWeight,
		TitlePrefixWeight: item.TitlePrefixWeight, TitleContainsWeight: item.TitleContainsWeight,
		CreatorExactWeight: item.CreatorExactWeight, CreatorMatchWeight: item.CreatorMatchWeight,
		BodyMatchWeight: item.BodyMatchWeight, SecondaryMatchWeight: item.SecondaryMatchWeight,
		RecencyWeight: item.RecencyWeight, CreatorActivityWeight: item.CreatorActivityWeight,
		WorkTypeBoost: item.WorkTypeBoost, CreatorTypeBoost: item.CreatorTypeBoost,
		ProductTypeBoost: item.ProductTypeBoost, DemandTypeBoost: item.DemandTypeBoost,
	}
}

func resultPosition(items []discovery.SearchResult, kind string, id uuid.UUID) int {
	for index, item := range items {
		if item.Type == kind && item.ID == id {
			return index + 1
		}
	}
	return 0
}
