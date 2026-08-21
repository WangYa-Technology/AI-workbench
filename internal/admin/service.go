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
	"github.com/hcai-chat/hcai-chat/internal/billing"
	"github.com/hcai-chat/hcai-chat/internal/creation"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound                    = errors.New("admin resource not found")
	ErrInvalid                     = errors.New("invalid admin command")
	ErrConflict                    = errors.New("admin resource state conflict")
	ErrSelfMutation                = errors.New("administrator cannot mutate own access")
	ErrProviderConfig              = errors.New("provider requires external configuration")
	ErrInvalidRiskFilter           = errors.New("invalid admin risk filter")
	ErrInvalidUserFilter           = errors.New("invalid admin user filter")
	ErrInvalidContentFilter        = errors.New("invalid admin content filter")
	ErrInvalidMediaFilter          = errors.New("invalid admin media filter")
	ErrInvalidReportFilter         = errors.New("invalid admin governance report filter")
	ErrInvalidAppealFilter         = errors.New("invalid admin governance appeal filter")
	ErrInvalidTaskFilter           = errors.New("invalid admin task filter")
	ErrInvalidGenerationFilter     = errors.New("invalid admin generation filter")
	ErrInvalidFinanceFilter        = errors.New("invalid admin finance filter")
	ErrInvalidPaymentFilter        = errors.New("invalid admin payment filter")
	ErrInvalidDestinationFilter    = errors.New("invalid admin payment destination filter")
	ErrInvalidSystemSettingHistory = errors.New("invalid admin system setting history filter")
	ErrInvalidRiskRuleHistory      = errors.New("invalid admin risk rule history filter")
	ErrInvalidRankingHistory       = errors.New("invalid admin ranking history filter")
	ErrInvalidDiscoveryHistory     = errors.New("invalid admin discovery operation history filter")
	ErrInvalidModelRouteHistory    = errors.New("invalid admin model route history filter")
)

type Overview struct {
	Users       CountBreakdown `json:"users"`
	Works       CountBreakdown `json:"works"`
	Generations CountBreakdown `json:"generations"`
	Orders      CountBreakdown `json:"orders"`
	Tasks       CountBreakdown `json:"tasks"`
	Risks       CountBreakdown `json:"risks"`
	Providers   CountBreakdown `json:"providers"`
	AsOf        time.Time      `json:"asOf"`
}

type CountBreakdown struct {
	Total       int64            `json:"total"`
	ByStatus    map[string]int64 `json:"byStatus"`
	AmountCents int64            `json:"amountCents,omitempty"`
}

type User struct {
	ID          uuid.UUID  `json:"id"`
	Email       string     `json:"email"`
	Handle      string     `json:"handle"`
	DisplayName string     `json:"displayName"`
	Role        string     `json:"role"`
	Status      string     `json:"status"`
	Locale      string     `json:"locale"`
	Timezone    string     `json:"timezone"`
	LastSeenAt  *time.Time `json:"lastSeenAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

type UserListInput struct {
	Query  string
	Role   string
	Status string
	Cursor string
	Limit  int
}

type UserPage struct {
	Items      []User  `json:"items"`
	NextCursor *string `json:"nextCursor,omitempty"`
}

type userCursor struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        uuid.UUID `json:"id"`
}

type UserUpdate struct {
	Role   string `json:"role"`
	Status string `json:"status"`
}

type ContentItem struct {
	ID           uuid.UUID  `json:"id"`
	ResourceType string     `json:"resourceType"`
	Title        string     `json:"title"`
	AuthorID     uuid.UUID  `json:"authorId"`
	AuthorHandle string     `json:"authorHandle"`
	Status       string     `json:"status"`
	AIDisclosure string     `json:"aiDisclosure"`
	PublishedAt  *time.Time `json:"publishedAt,omitempty"`
	UpdatedAt    time.Time  `json:"updatedAt"`
}

type ContentListInput struct {
	Query        string
	ResourceType string
	Status       string
	Cursor       string
	Limit        int
}

type ContentPage struct {
	Items      []ContentItem `json:"items"`
	NextCursor *string       `json:"nextCursor,omitempty"`
}

type contentCursor struct {
	UpdatedAt time.Time `json:"updatedAt"`
	ID        uuid.UUID `json:"id"`
}

type ContentUpdate struct {
	Status string `json:"status"`
}

type GenerationItem struct {
	creation.Generation
	OwnerEmail  string `json:"ownerEmail"`
	OwnerHandle string `json:"ownerHandle"`
}

type Provider struct {
	ID                 string    `json:"id"`
	Mode               string    `json:"mode"`
	Provider           string    `json:"provider"`
	ModelName          string    `json:"modelName"`
	DisplayName        string    `json:"displayName"`
	Description        string    `json:"description"`
	EstimatedCostCents int       `json:"estimatedCostCents"`
	Currency           string    `json:"currency"`
	LocalTest          bool      `json:"localTest"`
	AdminEnabled       bool      `json:"adminEnabled"`
	RuntimeAvailable   bool      `json:"runtimeAvailable"`
	EffectiveEnabled   bool      `json:"effectiveEnabled"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

type ProviderUpdate struct {
	Enabled            bool    `json:"enabled"`
	ModelName          *string `json:"modelName,omitempty"`
	DisplayName        *string `json:"displayName,omitempty"`
	Description        *string `json:"description,omitempty"`
	EstimatedCostCents *int    `json:"estimatedCostCents,omitempty"`
}

type FinanceAccount struct {
	billing.Account
	Email       string `json:"email"`
	Handle      string `json:"handle"`
	DisplayName string `json:"displayName"`
}

type FinanceAdjustment struct {
	DeltaCents int    `json:"deltaCents"`
	Currency   string `json:"currency"`
}

type RiskEvent struct {
	ID          uuid.UUID      `json:"id"`
	ActorID     *uuid.UUID     `json:"actorId,omitempty"`
	ActorHandle *string        `json:"actorHandle,omitempty"`
	Kind        string         `json:"kind"`
	FromStatus  *string        `json:"fromStatus,omitempty"`
	ToStatus    string         `json:"toStatus"`
	Reason      string         `json:"reason"`
	Metadata    map[string]any `json:"metadata"`
	CreatedAt   time.Time      `json:"createdAt"`
}

type RiskSignal struct {
	ID                uuid.UUID      `json:"id"`
	ResourceType      string         `json:"resourceType"`
	ResourceID        uuid.UUID      `json:"resourceId"`
	ResourceTitle     string         `json:"resourceTitle"`
	TargetPath        string         `json:"targetPath"`
	SubjectUserID     uuid.UUID      `json:"subjectUserId"`
	SubjectHandle     string         `json:"subjectHandle"`
	SignalType        string         `json:"signalType"`
	Severity          string         `json:"severity"`
	Score             int            `json:"score"`
	Status            string         `json:"status"`
	Summary           string         `json:"summary"`
	Evidence          map[string]any `json:"evidence"`
	Version           int            `json:"version"`
	ReviewerID        *uuid.UUID     `json:"reviewerId,omitempty"`
	ReviewerHandle    *string        `json:"reviewerHandle,omitempty"`
	ResolutionOutcome *string        `json:"resolutionOutcome,omitempty"`
	ResolutionReason  *string        `json:"resolutionReason,omitempty"`
	DetectedAt        time.Time      `json:"detectedAt"`
	UpdatedAt         time.Time      `json:"updatedAt"`
	ResolvedAt        *time.Time     `json:"resolvedAt,omitempty"`
	Events            []RiskEvent    `json:"events"`
}

type RiskSignalFilter struct {
	Query        string
	Status       string
	Severity     string
	ResourceType string
	ResourceID   *uuid.UUID
	Cursor       string
	Limit        int
}

type RiskSignalPage struct {
	Items      []RiskSignal `json:"items"`
	NextCursor *string      `json:"nextCursor,omitempty"`
}

type riskSignalCursor struct {
	Priority   int       `json:"priority"`
	Score      int       `json:"score"`
	DetectedAt time.Time `json:"detectedAt"`
	ID         uuid.UUID `json:"id"`
}

type RiskReview struct {
	Decision        string `json:"decision"`
	ExpectedVersion int    `json:"expectedVersion"`
}

type RankingRevision struct {
	ID                    uuid.UUID  `json:"id"`
	Version               int        `json:"version"`
	ParentRevisionID      *uuid.UUID `json:"parentRevisionId,omitempty"`
	Name                  string     `json:"name"`
	TitleExactWeight      int        `json:"titleExactWeight"`
	TitlePrefixWeight     int        `json:"titlePrefixWeight"`
	TitleContainsWeight   int        `json:"titleContainsWeight"`
	CreatorExactWeight    int        `json:"creatorExactWeight"`
	CreatorMatchWeight    int        `json:"creatorMatchWeight"`
	BodyMatchWeight       int        `json:"bodyMatchWeight"`
	SecondaryMatchWeight  int        `json:"secondaryMatchWeight"`
	RecencyWeight         int        `json:"recencyWeight"`
	CreatorActivityWeight int        `json:"creatorActivityWeight"`
	WorkTypeBoost         int        `json:"workTypeBoost"`
	CreatorTypeBoost      int        `json:"creatorTypeBoost"`
	ProductTypeBoost      int        `json:"productTypeBoost"`
	DemandTypeBoost       int        `json:"demandTypeBoost"`
	CreatedBy             *uuid.UUID `json:"createdBy,omitempty"`
	CreatedByHandle       *string    `json:"createdByHandle,omitempty"`
	CreatedAt             time.Time  `json:"createdAt"`
}

type RankingPolicy struct {
	Current    RankingRevision   `json:"current"`
	Candidate  *RankingRevision  `json:"candidate,omitempty"`
	Rollout    RankingRollout    `json:"rollout"`
	History    []RankingRevision `json:"history"`
	NextCursor *string           `json:"nextCursor,omitempty"`
}

type RankingRollout struct {
	Percent   int        `json:"percent"`
	Version   int        `json:"version"`
	StartedAt *time.Time `json:"startedAt,omitempty"`
}

type RankingUpdate struct {
	Name                  string `json:"name"`
	TitleExactWeight      int    `json:"titleExactWeight"`
	TitlePrefixWeight     int    `json:"titlePrefixWeight"`
	TitleContainsWeight   int    `json:"titleContainsWeight"`
	CreatorExactWeight    int    `json:"creatorExactWeight"`
	CreatorMatchWeight    int    `json:"creatorMatchWeight"`
	BodyMatchWeight       int    `json:"bodyMatchWeight"`
	SecondaryMatchWeight  int    `json:"secondaryMatchWeight"`
	RecencyWeight         int    `json:"recencyWeight"`
	CreatorActivityWeight int    `json:"creatorActivityWeight"`
	WorkTypeBoost         int    `json:"workTypeBoost"`
	CreatorTypeBoost      int    `json:"creatorTypeBoost"`
	ProductTypeBoost      int    `json:"productTypeBoost"`
	DemandTypeBoost       int    `json:"demandTypeBoost"`
	ExpectedVersion       int    `json:"expectedVersion"`
}

type Service struct {
	pool              *pgxpool.Pool
	runtimes          creation.RuntimeAvailability
	providerSecretKey []byte
}

func NewService(pool *pgxpool.Pool, localProviderRuntime bool) *Service {
	return NewServiceWithRuntimes(pool, creation.NewLocalRuntimeCatalog("", localProviderRuntime))
}

func NewServiceWithRuntimes(pool *pgxpool.Pool, runtimes creation.RuntimeAvailability) *Service {
	return NewServiceWithRuntimesAndProviderKey(pool, runtimes, nil)
}

func NewServiceWithRuntimesAndProviderKey(pool *pgxpool.Pool, runtimes creation.RuntimeAvailability, providerSecretKey []byte) *Service {
	if runtimes == nil {
		runtimes = creation.NewRuntimeCatalog()
	}
	return &Service{pool: pool, runtimes: runtimes, providerSecretKey: append([]byte(nil), providerSecretKey...)}
}

func (s *Service) Overview(ctx context.Context) (Overview, error) {
	result := Overview{AsOf: time.Now().UTC()}
	queries := []struct {
		table string
		into  *CountBreakdown
	}{
		{"users", &result.Users}, {"works", &result.Works}, {"generations", &result.Generations},
		{"orders", &result.Orders}, {"demands", &result.Tasks}, {"risk_signals", &result.Risks}, {"provider_profiles", &result.Providers},
	}
	for _, query := range queries {
		statusColumn := "status"
		if query.table == "provider_profiles" {
			statusColumn = "CASE WHEN admin_enabled THEN 'enabled' ELSE 'disabled' END"
		}
		rows, err := s.pool.Query(ctx, "SELECT "+statusColumn+",COUNT(*) FROM "+query.table+" GROUP BY 1")
		if err != nil {
			return Overview{}, fmt.Errorf("summarize %s: %w", query.table, err)
		}
		query.into.ByStatus = map[string]int64{}
		for rows.Next() {
			var status string
			var count int64
			if err := rows.Scan(&status, &count); err != nil {
				rows.Close()
				return Overview{}, fmt.Errorf("scan %s summary: %w", query.table, err)
			}
			query.into.ByStatus[status] = count
			query.into.Total += count
		}
		rows.Close()
	}
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(SUM(amount_cents),0) FROM orders WHERE status IN ('fulfilled','test_refunded')`).Scan(&result.Orders.AmountCents); err != nil {
		return Overview{}, fmt.Errorf("summarize orders: %w", err)
	}
	return result, nil
}

func (s *Service) ListUsers(ctx context.Context, input UserListInput) (UserPage, error) {
	input.Query = strings.ToLower(strings.TrimSpace(input.Query))
	input.Role = strings.ToLower(strings.TrimSpace(input.Role))
	input.Status = strings.ToLower(strings.TrimSpace(input.Status))
	if len(input.Query) > 120 || (input.Role != "" && !oneOf(input.Role, "member", "creator", "publisher", "moderator", "admin")) ||
		(input.Status != "" && !oneOf(input.Status, "active", "suspended", "deleted")) {
		return UserPage{}, ErrInvalidUserFilter
	}
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Limit < 1 || input.Limit > 50 {
		return UserPage{}, ErrInvalidUserFilter
	}
	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if input.Cursor != "" {
		cursor, err := decodeUserCursor(input.Cursor)
		if err != nil {
			return UserPage{}, ErrInvalidUserFilter
		}
		cursorTime, cursorID = &cursor.CreatedAt, &cursor.ID
	}
	rows, err := s.pool.Query(ctx, `
		SELECT u.id,u.email,u.handle,u.display_name,u.role,u.status,u.locale,u.timezone,
		       (SELECT max(last_seen_at) FROM sessions s WHERE s.user_id=u.id),u.created_at,u.updated_at
		FROM users u
		WHERE ($1='' OR strpos(lower(u.email),$1)>0 OR strpos(lower(u.handle),$1)>0 OR strpos(lower(u.display_name),$1)>0)
		  AND ($2='' OR u.role=$2)
		  AND ($3='' OR u.status=$3)
		  AND ($4::timestamptz IS NULL OR (u.created_at,u.id) < ($4,$5::uuid))
		ORDER BY u.created_at DESC,u.id DESC LIMIT $6`, input.Query, input.Role, input.Status, cursorTime, cursorID, input.Limit+1)
	if err != nil {
		return UserPage{}, fmt.Errorf("list admin users: %w", err)
	}
	defer rows.Close()
	items := make([]User, 0)
	for rows.Next() {
		var item User
		if err := rows.Scan(&item.ID, &item.Email, &item.Handle, &item.DisplayName, &item.Role, &item.Status,
			&item.Locale, &item.Timezone, &item.LastSeenAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return UserPage{}, fmt.Errorf("scan admin user: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return UserPage{}, err
	}
	page := UserPage{Items: items}
	if len(page.Items) > input.Limit {
		page.Items = page.Items[:input.Limit]
		cursor := encodeUserCursor(page.Items[len(page.Items)-1])
		page.NextCursor = &cursor
	}
	return page, nil
}

func encodeUserCursor(item User) string {
	body, _ := json.Marshal(userCursor{CreatedAt: item.CreatedAt, ID: item.ID})
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodeUserCursor(value string) (userCursor, error) {
	var cursor userCursor
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &cursor) != nil || cursor.ID == uuid.Nil || cursor.CreatedAt.IsZero() {
		return userCursor{}, ErrInvalidUserFilter
	}
	return cursor, nil
}

func (s *Service) user(ctx context.Context, userID uuid.UUID) (User, error) {
	var item User
	err := s.pool.QueryRow(ctx, `
		SELECT u.id,u.email,u.handle,u.display_name,u.role,u.status,u.locale,u.timezone,
		       (SELECT max(last_seen_at) FROM sessions s WHERE s.user_id=u.id),u.created_at,u.updated_at
		FROM users u WHERE u.id=$1`, userID).Scan(&item.ID, &item.Email, &item.Handle, &item.DisplayName, &item.Role, &item.Status,
		&item.Locale, &item.Timezone, &item.LastSeenAt, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("get admin user: %w", err)
	}
	return item, nil
}

func (s *Service) UpdateUser(ctx context.Context, actorID, userID uuid.UUID, input UserUpdate, _ string) (User, error) {
	input.Role = strings.TrimSpace(strings.ToLower(input.Role))
	input.Status = strings.TrimSpace(strings.ToLower(input.Status))
	if !oneOf(input.Role, "member", "creator", "publisher", "moderator", "admin") || !oneOf(input.Status, "active", "suspended", "deleted") {
		return User{}, ErrInvalid
	}
	if actorID == userID {
		return User{}, ErrSelfMutation
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var lockedID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&lockedID); errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	} else if err != nil {
		return User{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET role=$2,status=$3,updated_at=now() WHERE id=$1`, userID, input.Role, input.Status); err != nil {
		return User{}, fmt.Errorf("update admin user: %w", err)
	}
	if input.Status != "active" {
		if _, err := tx.Exec(ctx, `UPDATE sessions SET revoked_at=COALESCE(revoked_at,now()) WHERE user_id=$1`, userID); err != nil {
			return User{}, fmt.Errorf("revoke disabled user sessions: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, err
	}
	return s.user(ctx, userID)
}

func (s *Service) ListContent(ctx context.Context, input ContentListInput) (ContentPage, error) {
	input.Query = strings.ToLower(strings.TrimSpace(input.Query))
	input.ResourceType = strings.ToLower(strings.TrimSpace(input.ResourceType))
	input.Status = strings.ToLower(strings.TrimSpace(input.Status))
	if len(input.Query) > 120 || (input.ResourceType != "" && input.ResourceType != "work") ||
		(input.Status != "" && !oneOf(input.Status, "draft", "published", "hidden", "removed")) {
		return ContentPage{}, ErrInvalidContentFilter
	}
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Limit < 1 || input.Limit > 50 {
		return ContentPage{}, ErrInvalidContentFilter
	}
	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if input.Cursor != "" {
		cursor, err := decodeContentCursor(input.Cursor)
		if err != nil {
			return ContentPage{}, ErrInvalidContentFilter
		}
		cursorTime, cursorID = &cursor.UpdatedAt, &cursor.ID
	}
	rows, err := s.pool.Query(ctx, `
		SELECT w.id,'work',w.title,w.author_id,u.handle,w.status,w.ai_disclosure,w.published_at,w.updated_at
		FROM works w JOIN users u ON u.id=w.author_id
		WHERE ($1='' OR strpos(lower(w.title),$1)>0 OR strpos(lower(u.handle),$1)>0 OR strpos(lower(w.ai_disclosure),$1)>0)
		  AND ($2='' OR $2='work')
		  AND ($3='' OR w.status=$3)
		  AND ($4::timestamptz IS NULL OR (w.updated_at,w.id) < ($4,$5::uuid))
		ORDER BY w.updated_at DESC,w.id DESC LIMIT $6`, input.Query, input.ResourceType, input.Status, cursorTime, cursorID, input.Limit+1)
	if err != nil {
		return ContentPage{}, fmt.Errorf("list moderation content: %w", err)
	}
	defer rows.Close()
	items := make([]ContentItem, 0)
	for rows.Next() {
		var item ContentItem
		if err := rows.Scan(&item.ID, &item.ResourceType, &item.Title, &item.AuthorID, &item.AuthorHandle, &item.Status,
			&item.AIDisclosure, &item.PublishedAt, &item.UpdatedAt); err != nil {
			return ContentPage{}, fmt.Errorf("scan moderation content: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return ContentPage{}, err
	}
	page := ContentPage{Items: items}
	if len(page.Items) > input.Limit {
		page.Items = page.Items[:input.Limit]
		cursor := encodeContentCursor(page.Items[len(page.Items)-1])
		page.NextCursor = &cursor
	}
	return page, nil
}

func encodeContentCursor(item ContentItem) string {
	body, _ := json.Marshal(contentCursor{UpdatedAt: item.UpdatedAt, ID: item.ID})
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodeContentCursor(value string) (contentCursor, error) {
	var cursor contentCursor
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &cursor) != nil || cursor.ID == uuid.Nil || cursor.UpdatedAt.IsZero() {
		return contentCursor{}, ErrInvalidContentFilter
	}
	return cursor, nil
}

func (s *Service) content(ctx context.Context, workID uuid.UUID) (ContentItem, error) {
	var item ContentItem
	err := s.pool.QueryRow(ctx, `
		SELECT w.id,'work',w.title,w.author_id,u.handle,w.status,w.ai_disclosure,w.published_at,w.updated_at
		FROM works w JOIN users u ON u.id=w.author_id WHERE w.id=$1`, workID).Scan(
		&item.ID, &item.ResourceType, &item.Title, &item.AuthorID, &item.AuthorHandle, &item.Status,
		&item.AIDisclosure, &item.PublishedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ContentItem{}, ErrNotFound
	}
	if err != nil {
		return ContentItem{}, fmt.Errorf("get moderation content: %w", err)
	}
	return item, nil
}

func (s *Service) UpdateContent(ctx context.Context, _ uuid.UUID, workID uuid.UUID, input ContentUpdate, _ string) (ContentItem, error) {
	input.Status = strings.TrimSpace(strings.ToLower(input.Status))
	if !oneOf(input.Status, "published", "hidden", "removed") {
		return ContentItem{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ContentItem{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var lockedID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM works WHERE id=$1 FOR UPDATE`, workID).Scan(&lockedID); errors.Is(err, pgx.ErrNoRows) {
		return ContentItem{}, ErrNotFound
	} else if err != nil {
		return ContentItem{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE works SET status=$2,updated_at=now() WHERE id=$1`, workID, input.Status); err != nil {
		return ContentItem{}, fmt.Errorf("moderate work: %w", err)
	}
	postStatus := input.Status
	if postStatus == "published" {
		postStatus = "published"
	}
	if _, err := tx.Exec(ctx, `UPDATE posts SET status=$2,updated_at=now() WHERE work_id=$1`, workID, postStatus); err != nil {
		return ContentItem{}, fmt.Errorf("moderate linked post: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ContentItem{}, err
	}
	return s.content(ctx, workID)
}

func (s *Service) CancelGeneration(ctx context.Context, _ uuid.UUID, generationID uuid.UUID, _ string) (GenerationItem, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return GenerationItem{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM generations WHERE id=$1 FOR UPDATE`, generationID).Scan(&status); errors.Is(err, pgx.ErrNoRows) {
		return GenerationItem{}, ErrNotFound
	} else if err != nil {
		return GenerationItem{}, err
	}
	if status != "queued" && status != "running" && status != "cancelled" {
		return GenerationItem{}, ErrConflict
	}
	if status != "cancelled" {
		if _, err := tx.Exec(ctx, `UPDATE generations SET status='cancelled',progress=0,cancelled_at=now(),cancel_reason=$2,updated_at=now() WHERE id=$1`, generationID, "Cancelled by an administrator"); err != nil {
			return GenerationItem{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE jobs SET status='cancelled',lease_owner=NULL,lease_expires_at=NULL,updated_at=now() WHERE kind=$2 AND payload->>'generationId'=$1 AND status IN ('queued','running')`, generationID.String(), creation.JobKind); err != nil {
			return GenerationItem{}, err
		}
		if err := billing.ReleaseGenerationPointsTx(ctx, tx, generationID, "administrator cancellation"); err != nil {
			return GenerationItem{}, err
		}
		if err := billing.ReleaseGenerationTx(ctx, tx, generationID, "administrator cancellation"); err != nil {
			return GenerationItem{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return GenerationItem{}, err
	}
	return s.generation(ctx, generationID)
}

func (s *Service) generation(ctx context.Context, id uuid.UUID) (GenerationItem, error) {
	item, err := scanGeneration(s.pool.QueryRow(ctx, creationAdminSelect+` WHERE g.id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return GenerationItem{}, ErrNotFound
	}
	return item, err
}

func (s *Service) ListProviders(ctx context.Context) ([]Provider, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id,mode,provider,model_name,display_name,description,estimated_cost_cents,currency,local_test,admin_enabled,updated_at
		FROM provider_profiles ORDER BY mode,id`)
	if err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}
	defer rows.Close()
	items := make([]Provider, 0)
	for rows.Next() {
		var item Provider
		if err := rows.Scan(&item.ID, &item.Mode, &item.Provider, &item.ModelName, &item.DisplayName, &item.Description,
			&item.EstimatedCostCents, &item.Currency, &item.LocalTest, &item.AdminEnabled, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan provider: %w", err)
		}
		item.RuntimeAvailable = s.runtimes.Available(item.Provider, item.Mode, item.ModelName)
		item.EffectiveEnabled = item.AdminEnabled && item.RuntimeAvailable
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) UpdateProvider(ctx context.Context, actorID uuid.UUID, providerID string, input ProviderUpdate, _ string) (Provider, error) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return Provider{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Provider{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var mode, provider, modelName, displayName, description string
	var estimatedCostCents int
	if err := tx.QueryRow(ctx, `SELECT mode,provider,model_name,display_name,description,estimated_cost_cents FROM provider_profiles WHERE id=$1 FOR UPDATE`, providerID).Scan(&mode, &provider, &modelName, &displayName, &description, &estimatedCostCents); errors.Is(err, pgx.ErrNoRows) {
		return Provider{}, ErrNotFound
	} else if err != nil {
		return Provider{}, err
	}
	newModelName, newDisplayName, newDescription, newCost := modelName, displayName, description, estimatedCostCents
	if input.ModelName != nil {
		newModelName = strings.TrimSpace(*input.ModelName)
		if len(newModelName) < 1 || len(newModelName) > 160 {
			return Provider{}, ErrInvalid
		}
	}
	if input.DisplayName != nil {
		newDisplayName = strings.TrimSpace(*input.DisplayName)
		if len(newDisplayName) < 2 || len(newDisplayName) > 120 {
			return Provider{}, ErrInvalid
		}
	}
	if input.Description != nil {
		newDescription = strings.TrimSpace(*input.Description)
		if len(newDescription) < 10 || len(newDescription) > 1000 {
			return Provider{}, ErrInvalid
		}
	}
	if input.EstimatedCostCents != nil {
		newCost = *input.EstimatedCostCents
		if newCost < 0 || newCost > 1000000 {
			return Provider{}, ErrInvalid
		}
	}
	if input.Enabled && !s.runtimes.Available(provider, mode, newModelName) {
		return Provider{}, ErrProviderConfig
	}
	if _, err := tx.Exec(ctx, `UPDATE provider_profiles SET model_name=$2,display_name=$3,description=$4,estimated_cost_cents=$5,admin_enabled=$6,updated_by=$7,updated_at=now() WHERE id=$1`, providerID, newModelName, newDisplayName, newDescription, newCost, input.Enabled, actorID); err != nil {
		return Provider{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Provider{}, err
	}
	items, err := s.ListProviders(ctx)
	if err != nil {
		return Provider{}, err
	}
	for _, item := range items {
		if item.ID == providerID {
			return item, nil
		}
	}
	return Provider{}, ErrNotFound
}

func (s *Service) AdjustFinance(ctx context.Context, actorID, userID uuid.UUID, input FinanceAdjustment, _ string) (FinanceAccount, error) {
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	if input.DeltaCents == 0 || input.Currency != "USD" || input.DeltaCents > 1000000 || input.DeltaCents < -1000000 {
		return FinanceAccount{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return FinanceAccount{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	operationID := uuid.New()
	_, err = billing.AdjustTx(ctx, tx, userID, operationID, input.DeltaCents, input.Currency, "Administrative balance adjustment", map[string]any{"actorId": actorID.String(), "paymentMode": "local_test"})
	if err != nil {
		return FinanceAccount{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return FinanceAccount{}, err
	}
	return s.financeAccount(ctx, userID, input.Currency)
}

const riskSignalSelect = `
	SELECT s.id,s.resource_type,s.resource_id,
		       CASE s.resource_type
		            WHEN 'task' THEN COALESCE((SELECT title FROM demands WHERE id=s.resource_id),'Unavailable task')
		            WHEN 'order' THEN COALESCE((SELECT product_title_snapshot FROM orders WHERE id=s.resource_id),'Unavailable order')
		            WHEN 'post' THEN COALESCE((SELECT w.title FROM posts p JOIN works w ON w.id=p.work_id WHERE p.id=s.resource_id),'Unavailable post')
		            WHEN 'asset' THEN COALESCE((SELECT title FROM assets WHERE id=s.resource_id),'Unavailable Asset')
		            WHEN 'user' THEN 'Account @'||subject.handle
		       END,
		       CASE s.resource_type
		            WHEN 'task' THEN '/market/demands?task='||s.resource_id::text
		            WHEN 'order' THEN '/workspace/orders'
		            WHEN 'post' THEN COALESCE((SELECT '/works/'||p.work_id::text FROM posts p WHERE p.id=s.resource_id),'/community')
		            WHEN 'asset' THEN '/workspace/assets/'||s.resource_id::text
		            WHEN 'user' THEN '/admin?tab=users&q='||subject.handle
		       END,
		       s.subject_user_id,subject.handle,s.signal_type,s.severity,s.score,s.status,s.summary,s.evidence,s.version,
		       s.reviewer_id,reviewer.handle,s.resolution_outcome,s.resolution_reason,s.detected_at,s.updated_at,s.resolved_at
		FROM risk_signals s
		JOIN users subject ON subject.id=s.subject_user_id
		LEFT JOIN users reviewer ON reviewer.id=s.reviewer_id`

func (s *Service) ListRiskSignals(ctx context.Context, filter RiskSignalFilter) (RiskSignalPage, error) {
	filter.Query = strings.ToLower(strings.TrimSpace(filter.Query))
	filter.Status = strings.ToLower(strings.TrimSpace(filter.Status))
	filter.Severity = strings.ToLower(strings.TrimSpace(filter.Severity))
	filter.ResourceType = strings.ToLower(strings.TrimSpace(filter.ResourceType))
	if (filter.ResourceType == "") != (filter.ResourceID == nil) ||
		(filter.ResourceType != "" && !oneOf(filter.ResourceType, "task", "order", "post", "asset", "user")) ||
		(filter.ResourceID != nil && *filter.ResourceID == uuid.Nil) || len(filter.Query) > 120 ||
		(filter.Status != "" && !oneOf(filter.Status, "open", "reviewing", "resolved", "dismissed")) ||
		(filter.Severity != "" && !oneOf(filter.Severity, "low", "medium", "high", "critical")) {
		return RiskSignalPage{}, ErrInvalidRiskFilter
	}
	if filter.Limit == 0 {
		filter.Limit = 20
	}
	if filter.Limit < 1 || filter.Limit > 50 {
		return RiskSignalPage{}, ErrInvalidRiskFilter
	}
	var priority *int
	var score *int
	var detectedAt *time.Time
	var cursorID *uuid.UUID
	if filter.Cursor != "" {
		cursor, err := decodeRiskSignalCursor(filter.Cursor)
		if err != nil {
			return RiskSignalPage{}, err
		}
		priority, score, detectedAt, cursorID = &cursor.Priority, &cursor.Score, &cursor.DetectedAt, &cursor.ID
	}
	const prioritySQL = `CASE s.status WHEN 'open' THEN 0 WHEN 'reviewing' THEN 1 ELSE 2 END`
	rows, err := s.pool.Query(ctx, riskSignalSelect+`
		WHERE ($1='' OR strpos(lower(s.summary),$1)>0 OR strpos(lower(subject.handle),$1)>0 OR strpos(lower(s.signal_type),$1)>0 OR strpos(lower(s.resource_type),$1)>0 OR strpos(lower(s.resource_id::text),$1)>0)
		  AND ($2='' OR s.status=$2) AND ($3='' OR s.severity=$3)
		  AND ($4='' OR (s.resource_type=$4 AND s.resource_id=$5::uuid))
		  AND ($6::int IS NULL OR `+prioritySQL+`>$6 OR (`+prioritySQL+`=$6 AND (s.score<$7 OR (s.score=$7 AND (s.detected_at,s.id)>($8,$9::uuid)))))
		ORDER BY `+prioritySQL+`,s.score DESC,s.detected_at,s.id LIMIT $10`, filter.Query, filter.Status, filter.Severity,
		filter.ResourceType, filter.ResourceID, priority, score, detectedAt, cursorID, filter.Limit+1)
	if err != nil {
		return RiskSignalPage{}, fmt.Errorf("list risk signals: %w", err)
	}
	defer rows.Close()
	items := make([]RiskSignal, 0)
	for rows.Next() {
		item, err := scanRiskSignal(rows)
		if err != nil {
			return RiskSignalPage{}, fmt.Errorf("scan risk signal: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return RiskSignalPage{}, err
	}
	for index := range items {
		events, err := s.riskEvents(ctx, items[index].ID)
		if err != nil {
			return RiskSignalPage{}, err
		}
		items[index].Events = events
	}
	page := RiskSignalPage{Items: items}
	if len(page.Items) > filter.Limit {
		page.Items = page.Items[:filter.Limit]
		cursor := encodeRiskSignalCursor(page.Items[len(page.Items)-1])
		page.NextCursor = &cursor
	}
	return page, nil
}

func riskSignalPriority(status string) int {
	if status == "open" {
		return 0
	}
	if status == "reviewing" {
		return 1
	}
	return 2
}

func encodeRiskSignalCursor(item RiskSignal) string {
	body, _ := json.Marshal(riskSignalCursor{Priority: riskSignalPriority(item.Status), Score: item.Score, DetectedAt: item.DetectedAt, ID: item.ID})
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodeRiskSignalCursor(value string) (riskSignalCursor, error) {
	var cursor riskSignalCursor
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &cursor) != nil || cursor.Priority < 0 || cursor.Priority > 2 || cursor.Score < 0 || cursor.Score > 100 || cursor.DetectedAt.IsZero() || cursor.ID == uuid.Nil {
		return riskSignalCursor{}, ErrInvalidRiskFilter
	}
	return cursor, nil
}

func (s *Service) riskSignal(ctx context.Context, id uuid.UUID) (RiskSignal, error) {
	item, err := scanRiskSignal(s.pool.QueryRow(ctx, riskSignalSelect+` WHERE s.id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return RiskSignal{}, ErrNotFound
	}
	if err != nil {
		return RiskSignal{}, fmt.Errorf("get risk signal: %w", err)
	}
	item.Events, err = s.riskEvents(ctx, id)
	return item, err
}

func (s *Service) GetRankingPolicy(ctx context.Context, inputs ...RevisionHistoryInput) (RankingPolicy, error) {
	input := RevisionHistoryInput{}
	if len(inputs) > 0 {
		input = inputs[0]
	}
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Limit < 1 || input.Limit > 50 {
		return RankingPolicy{}, ErrInvalidRankingHistory
	}
	var cursorVersion *int
	if input.Cursor != "" {
		cursor, err := decodeRevisionHistoryCursor(input.Cursor, ErrInvalidRankingHistory)
		if err != nil {
			return RankingPolicy{}, err
		}
		cursorVersion = &cursor.Version
	}
	var activeID uuid.UUID
	var candidateID *uuid.UUID
	var rollout RankingRollout
	if err := s.pool.QueryRow(ctx, `SELECT active_revision_id,candidate_revision_id,rollout_percent,rollout_version,rollout_started_at FROM discovery_ranking_state WHERE singleton=true`).Scan(&activeID, &candidateID, &rollout.Percent, &rollout.Version, &rollout.StartedAt); errors.Is(err, pgx.ErrNoRows) {
		return RankingPolicy{}, ErrNotFound
	} else if err != nil {
		return RankingPolicy{}, fmt.Errorf("get active ranking revision: %w", err)
	}
	current, err := s.rankingRevision(ctx, activeID)
	if err != nil {
		return RankingPolicy{}, err
	}
	policy := RankingPolicy{Current: current, History: make([]RankingRevision, 0), Rollout: rollout}
	if candidateID != nil {
		candidate, err := s.rankingRevision(ctx, *candidateID)
		if err != nil {
			return RankingPolicy{}, err
		}
		policy.Candidate = &candidate
	}
	rows, err := s.pool.Query(ctx, `
		SELECT r.id,r.version,r.parent_revision_id,r.name,
		       r.title_exact_weight,r.title_prefix_weight,r.title_contains_weight,
		       r.creator_exact_weight,r.creator_match_weight,r.body_match_weight,r.secondary_match_weight,
		       r.recency_weight,r.creator_activity_weight,r.work_type_boost,r.creator_type_boost,
		       r.product_type_boost,r.demand_type_boost,r.created_by,u.handle,r.created_at
		FROM discovery_ranking_revisions r LEFT JOIN users u ON u.id=r.created_by
		WHERE ($1::int IS NULL OR r.version < $1)
		ORDER BY r.version DESC LIMIT $2`, cursorVersion, input.Limit+1)
	if err != nil {
		return RankingPolicy{}, fmt.Errorf("list ranking revisions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item RankingRevision
		if err := rows.Scan(&item.ID, &item.Version, &item.ParentRevisionID, &item.Name,
			&item.TitleExactWeight, &item.TitlePrefixWeight, &item.TitleContainsWeight,
			&item.CreatorExactWeight, &item.CreatorMatchWeight, &item.BodyMatchWeight, &item.SecondaryMatchWeight,
			&item.RecencyWeight, &item.CreatorActivityWeight, &item.WorkTypeBoost, &item.CreatorTypeBoost,
			&item.ProductTypeBoost, &item.DemandTypeBoost, &item.CreatedBy, &item.CreatedByHandle,
			&item.CreatedAt); err != nil {
			return RankingPolicy{}, fmt.Errorf("scan ranking revision: %w", err)
		}
		policy.History = append(policy.History, item)
	}
	if err := rows.Err(); err != nil {
		return RankingPolicy{}, err
	}
	if len(policy.History) > input.Limit {
		policy.History = policy.History[:input.Limit]
		cursor := encodeRevisionHistoryCursor(policy.History[len(policy.History)-1].Version)
		policy.NextCursor = &cursor
	}
	return policy, nil
}

func (s *Service) rankingRevision(ctx context.Context, id uuid.UUID) (RankingRevision, error) {
	var item RankingRevision
	err := s.pool.QueryRow(ctx, `
		SELECT r.id,r.version,r.parent_revision_id,r.name,
		       r.title_exact_weight,r.title_prefix_weight,r.title_contains_weight,
		       r.creator_exact_weight,r.creator_match_weight,r.body_match_weight,r.secondary_match_weight,
		       r.recency_weight,r.creator_activity_weight,r.work_type_boost,r.creator_type_boost,
		       r.product_type_boost,r.demand_type_boost,r.created_by,u.handle,r.created_at
		FROM discovery_ranking_revisions r LEFT JOIN users u ON u.id=r.created_by WHERE r.id=$1`, id).Scan(
		&item.ID, &item.Version, &item.ParentRevisionID, &item.Name,
		&item.TitleExactWeight, &item.TitlePrefixWeight, &item.TitleContainsWeight,
		&item.CreatorExactWeight, &item.CreatorMatchWeight, &item.BodyMatchWeight, &item.SecondaryMatchWeight,
		&item.RecencyWeight, &item.CreatorActivityWeight, &item.WorkTypeBoost, &item.CreatorTypeBoost,
		&item.ProductTypeBoost, &item.DemandTypeBoost, &item.CreatedBy, &item.CreatedByHandle, &item.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return RankingRevision{}, ErrNotFound
	}
	return item, err
}

func (s *Service) UpdateRankingPolicy(ctx context.Context, actorID uuid.UUID, input RankingUpdate, _ string) (RankingPolicy, error) {
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
	var candidateID *uuid.UUID
	var version int
	if err := tx.QueryRow(ctx, `SELECT active_revision_id,candidate_revision_id,version FROM discovery_ranking_state WHERE singleton=true FOR UPDATE`).Scan(&activeID, &candidateID, &version); errors.Is(err, pgx.ErrNoRows) {
		return RankingPolicy{}, ErrNotFound
	} else if err != nil {
		return RankingPolicy{}, err
	}
	if version != input.ExpectedVersion || candidateID != nil {
		return RankingPolicy{}, ErrConflict
	}
	newID := uuid.New()
	newVersion := version + 1
	if _, err := tx.Exec(ctx, `
		INSERT INTO discovery_ranking_revisions(
			id,version,parent_revision_id,name,title_exact_weight,title_prefix_weight,title_contains_weight,
			creator_exact_weight,creator_match_weight,body_match_weight,secondary_match_weight,
			recency_weight,creator_activity_weight,work_type_boost,creator_type_boost,product_type_boost,demand_type_boost,
			reason,created_by
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
		newID, newVersion, activeID, input.Name, input.TitleExactWeight, input.TitlePrefixWeight,
		input.TitleContainsWeight, input.CreatorExactWeight, input.CreatorMatchWeight, input.BodyMatchWeight,
		input.SecondaryMatchWeight, input.RecencyWeight, input.CreatorActivityWeight, input.WorkTypeBoost,
		input.CreatorTypeBoost, input.ProductTypeBoost, input.DemandTypeBoost, "Administrative configuration update", actorID); err != nil {
		return RankingPolicy{}, fmt.Errorf("create ranking revision: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE discovery_ranking_state SET active_revision_id=$1,version=$2,updated_at=now() WHERE singleton=true`, newID, newVersion); err != nil {
		return RankingPolicy{}, fmt.Errorf("activate ranking revision: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return RankingPolicy{}, err
	}
	return s.GetRankingPolicy(ctx)
}

func validRankingUpdate(input RankingUpdate) bool {
	weights := []int{
		input.TitleExactWeight, input.TitlePrefixWeight, input.TitleContainsWeight, input.CreatorExactWeight,
		input.CreatorMatchWeight, input.BodyMatchWeight, input.SecondaryMatchWeight,
	}
	for _, weight := range weights {
		if weight < 0 || weight > 200 {
			return false
		}
	}
	boosts := []int{input.WorkTypeBoost, input.CreatorTypeBoost, input.ProductTypeBoost, input.DemandTypeBoost}
	for _, boost := range boosts {
		if boost < -50 || boost > 50 {
			return false
		}
	}
	return input.ExpectedVersion > 0 && len(input.Name) >= 3 && len(input.Name) <= 80 &&
		input.RecencyWeight >= 0 && input.RecencyWeight <= 50 &&
		input.CreatorActivityWeight >= 0 && input.CreatorActivityWeight <= 50 &&
		input.TitleExactWeight >= input.TitlePrefixWeight && input.TitlePrefixWeight >= input.TitleContainsWeight &&
		input.CreatorExactWeight >= input.CreatorMatchWeight
}

func (s *Service) ReviewRiskSignal(ctx context.Context, actorID, signalID uuid.UUID, input RiskReview, _ string) (RiskSignal, error) {
	input.Decision = strings.TrimSpace(strings.ToLower(input.Decision))
	if input.ExpectedVersion < 1 || !oneOf(input.Decision, "monitor", "no_action", "escalated") {
		return RiskSignal{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RiskSignal{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var oldStatus string
	var version int
	if err := tx.QueryRow(ctx, `SELECT status,version FROM risk_signals WHERE id=$1 FOR UPDATE`, signalID).Scan(&oldStatus, &version); errors.Is(err, pgx.ErrNoRows) {
		return RiskSignal{}, ErrNotFound
	} else if err != nil {
		return RiskSignal{}, err
	}
	if version != input.ExpectedVersion || oldStatus == "resolved" || oldStatus == "dismissed" {
		return RiskSignal{}, ErrConflict
	}
	newStatus := "reviewing"
	if input.Decision == "no_action" {
		newStatus = "dismissed"
	} else if input.Decision == "escalated" {
		newStatus = "resolved"
	}
	if _, err := tx.Exec(ctx, `
		UPDATE risk_signals SET status=$2,reviewer_id=$3,resolution_outcome=$4,resolution_reason=$5,
		       resolved_at=CASE WHEN $2 IN ('resolved','dismissed') THEN now() ELSE NULL END,
		       version=version+1,updated_at=now()
		WHERE id=$1`, signalID, newStatus, actorID, input.Decision, "Administrative review: "+input.Decision); err != nil {
		return RiskSignal{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO risk_events(signal_id,actor_id,kind,from_status,to_status,reason,metadata)
		VALUES($1,$2,'reviewed',$3,$4,$5,jsonb_build_object('decision',$6::text,'expectedVersion',$7::integer))`,
		signalID, actorID, oldStatus, newStatus, "Administrative review: "+input.Decision, input.Decision, input.ExpectedVersion); err != nil {
		return RiskSignal{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RiskSignal{}, err
	}
	return s.riskSignal(ctx, signalID)
}

func (s *Service) riskEvents(ctx context.Context, signalID uuid.UUID) ([]RiskEvent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT e.id,e.actor_id,u.handle,e.kind,e.from_status,e.to_status,e.reason,e.metadata,e.created_at
		FROM risk_events e LEFT JOIN users u ON u.id=e.actor_id WHERE e.signal_id=$1 ORDER BY e.created_at,e.id`, signalID)
	if err != nil {
		return nil, fmt.Errorf("list risk events: %w", err)
	}
	defer rows.Close()
	items := make([]RiskEvent, 0)
	for rows.Next() {
		var item RiskEvent
		if err := rows.Scan(&item.ID, &item.ActorID, &item.ActorHandle, &item.Kind, &item.FromStatus, &item.ToStatus,
			&item.Reason, &item.Metadata, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan risk event: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

const creationAdminSelect = `
	SELECT g.id,g.mode,g.provider,g.model_name,g.prompt,g.status,g.progress,g.estimated_cost_cents,g.charged_cost_cents,
	       g.output_asset_id,a.media_url,g.output_text,g.source_work_id,g.source_asset_id,g.source_task_id,g.retry_of_generation_id,
	       g.error_code,g.error_message,g.cancelled_at,g.cancel_reason,g.created_at,g.updated_at,u.email,u.handle
	FROM generations g JOIN users u ON u.id=g.owner_id LEFT JOIN assets a ON a.id=g.output_asset_id`

type scanner interface{ Scan(...any) error }

func scanRiskSignal(row scanner) (RiskSignal, error) {
	var item RiskSignal
	err := row.Scan(&item.ID, &item.ResourceType, &item.ResourceID, &item.ResourceTitle, &item.TargetPath,
		&item.SubjectUserID, &item.SubjectHandle, &item.SignalType, &item.Severity, &item.Score, &item.Status,
		&item.Summary, &item.Evidence, &item.Version, &item.ReviewerID, &item.ReviewerHandle,
		&item.ResolutionOutcome, &item.ResolutionReason, &item.DetectedAt, &item.UpdatedAt, &item.ResolvedAt)
	return item, err
}

func scanGeneration(row scanner) (GenerationItem, error) {
	var item GenerationItem
	err := row.Scan(&item.ID, &item.Mode, &item.Provider, &item.ModelName, &item.Prompt, &item.Status, &item.Progress,
		&item.EstimatedCostCents, &item.ChargedCostCents, &item.OutputAssetID, &item.OutputMediaURL, &item.OutputText,
		&item.SourceWorkID, &item.SourceAssetID, &item.SourceTaskID, &item.RetryOfGenerationID,
		&item.ErrorCode, &item.ErrorMessage, &item.CancelledAt, &item.CancelReason, &item.CreatedAt, &item.UpdatedAt,
		&item.OwnerEmail, &item.OwnerHandle)
	return item, err
}

func oneOf(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}
