package webhooks

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const JobKind = "webhook.deliver"

var (
	ErrDisabled                = errors.New("webhooks are disabled")
	ErrInvalid                 = errors.New("invalid webhook input")
	ErrNotFound                = errors.New("webhook resource not found")
	ErrConflict                = errors.New("webhook state conflict")
	ErrInvalidDeadLetterFilter = errors.New("invalid webhook dead-letter filter")
	ErrInvalidOwnerFilter      = errors.New("invalid owner webhook delivery filter")
	eventCatalog               = []string{
		"developer.webhook.test",
		"generation.completed",
		"marketplace.order.fulfilled",
		"marketplace.order.refunded",
		"work.published",
	}
	namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{1,78}[A-Za-z0-9]$`)
)

type Endpoint struct {
	ID                   uuid.UUID  `json:"id"`
	Name                 string     `json:"name"`
	URL                  string     `json:"url"`
	EventTypes           []string   `json:"eventTypes"`
	Status               string     `json:"status"`
	CurrentSecretVersion int        `json:"currentSecretVersion"`
	SecretHint           string     `json:"secretHint"`
	Version              int        `json:"version"`
	CreatedAt            time.Time  `json:"createdAt"`
	UpdatedAt            time.Time  `json:"updatedAt"`
	RevokedAt            *time.Time `json:"revokedAt,omitempty"`
	Deliveries           []Delivery `json:"deliveries"`
	DeliveryNextCursor   *string    `json:"deliveryNextCursor,omitempty"`
}

type Credential struct {
	Endpoint
	SigningSecret string `json:"signingSecret"`
}

type Delivery struct {
	ID                 uuid.UUID  `json:"id"`
	EndpointID         uuid.UUID  `json:"endpointId"`
	EndpointName       string     `json:"endpointName"`
	EndpointHost       string     `json:"endpointHost"`
	OwnerHandle        string     `json:"ownerHandle,omitempty"`
	EventID            uuid.UUID  `json:"eventId"`
	EventType          string     `json:"eventType"`
	Status             string     `json:"status"`
	Version            int        `json:"version"`
	AttemptCount       int        `json:"attemptCount"`
	NextAttemptAt      *time.Time `json:"nextAttemptAt,omitempty"`
	LastStatusCode     *int       `json:"lastStatusCode,omitempty"`
	LastErrorCode      *string    `json:"lastErrorCode,omitempty"`
	OriginalDeliveryID *uuid.UUID `json:"originalDeliveryId,omitempty"`
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
	SucceededAt        *time.Time `json:"succeededAt,omitempty"`
	DeadLetteredAt     *time.Time `json:"deadLetteredAt,omitempty"`
	Attempts           []Attempt  `json:"attempts"`
}

type Attempt struct {
	ID             uuid.UUID `json:"id"`
	AttemptNumber  int       `json:"attemptNumber"`
	StatusCode     *int      `json:"statusCode,omitempty"`
	ErrorCode      *string   `json:"errorCode,omitempty"`
	ResponseSHA256 *string   `json:"responseSha256,omitempty"`
	DurationMS     int       `json:"durationMs"`
	AttemptedAt    time.Time `json:"attemptedAt"`
}

type Access struct {
	EventTypes []string   `json:"eventTypes"`
	Endpoints  []Endpoint `json:"endpoints"`
}

type CreateInput struct {
	Name       string   `json:"name"`
	URL        string   `json:"url"`
	EventTypes []string `json:"eventTypes"`
}

type Transition struct {
	ExpectedVersion int    `json:"expectedVersion"`
	Reason          string `json:"reason"`
	Confirmed       bool   `json:"confirmed"`
}

type AdminTransition struct {
	ExpectedVersion int `json:"expectedVersion"`
}

type DeadLetterListInput struct {
	Query     string
	EventType string
	Cursor    string
	Limit     int
}

type OwnerDeliveryListInput struct {
	Cursor string
	Limit  int
}

type DeliveryPage struct {
	Items      []Delivery `json:"items"`
	NextCursor *string    `json:"nextCursor,omitempty"`
}

type deliveryCursor struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        uuid.UUID `json:"id"`
}

type EventInput struct {
	OwnerID      uuid.UUID
	EventType    string
	ResourceType string
	ResourceID   *uuid.UUID
	SourceKey    string
}

type Service struct {
	pool       *pgxpool.Pool
	key        []byte
	allowLocal bool
}

func NewService(pool *pgxpool.Pool, encryptionKey []byte, allowLocal bool) *Service {
	key := append([]byte(nil), encryptionKey...)
	if len(key) != 32 {
		fallback := sha256.Sum256([]byte("hcai-chat-deterministic-local-test-webhook-key"))
		key = fallback[:]
	}
	return &Service{pool: pool, key: key, allowLocal: allowLocal}
}

func (s *Service) GetAccess(ctx context.Context, ownerID uuid.UUID) (Access, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT e.id,e.name,e.url,e.event_types,e.status,e.current_secret_version,r.display_hint,e.version,e.created_at,e.updated_at,e.revoked_at
		FROM developer_webhook_endpoints e
		JOIN developer_webhook_secret_revisions r ON r.endpoint_id=e.id AND r.version=e.current_secret_version
		WHERE e.owner_id=$1 ORDER BY e.created_at DESC,e.id DESC`, ownerID)
	if err != nil {
		return Access{}, err
	}
	defer rows.Close()
	items := []Endpoint{}
	for rows.Next() {
		var item Endpoint
		if err := rows.Scan(&item.ID, &item.Name, &item.URL, &item.EventTypes, &item.Status, &item.CurrentSecretVersion, &item.SecretHint, &item.Version, &item.CreatedAt, &item.UpdatedAt, &item.RevokedAt); err != nil {
			return Access{}, err
		}
		page, err := s.ListEndpointDeliveries(ctx, ownerID, item.ID, OwnerDeliveryListInput{Limit: 5})
		if err != nil {
			return Access{}, err
		}
		item.Deliveries = page.Items
		item.DeliveryNextCursor = page.NextCursor
		items = append(items, item)
	}
	return Access{EventTypes: append([]string(nil), eventCatalog...), Endpoints: items}, rows.Err()
}

func (s *Service) ListEndpointDeliveries(ctx context.Context, ownerID, endpointID uuid.UUID, input OwnerDeliveryListInput) (DeliveryPage, error) {
	if input.Limit == 0 {
		input.Limit = 5
	}
	if ownerID == uuid.Nil || endpointID == uuid.Nil || input.Limit < 1 || input.Limit > 50 {
		return DeliveryPage{}, ErrInvalidOwnerFilter
	}
	var exists int
	if err := s.pool.QueryRow(ctx, `SELECT 1 FROM developer_webhook_endpoints WHERE id=$1 AND owner_id=$2`, endpointID, ownerID).Scan(&exists); errors.Is(err, pgx.ErrNoRows) {
		return DeliveryPage{}, ErrNotFound
	} else if err != nil {
		return DeliveryPage{}, err
	}
	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if input.Cursor != "" {
		cursor, err := decodeOwnerDeliveryCursor(input.Cursor)
		if err != nil {
			return DeliveryPage{}, err
		}
		cursorTime, cursorID = &cursor.CreatedAt, &cursor.ID
	}
	rows, err := s.pool.Query(ctx, deliverySelect+`
		WHERE e.owner_id=$1 AND e.id=$2
		  AND ($3::timestamptz IS NULL OR (d.created_at,d.id)<($3,$4::uuid))
		ORDER BY d.created_at DESC,d.id DESC LIMIT $5`, ownerID, endpointID, cursorTime, cursorID, input.Limit+1)
	if err != nil {
		return DeliveryPage{}, fmt.Errorf("list owner webhook deliveries: %w", err)
	}
	defer rows.Close()
	items := make([]Delivery, 0)
	for rows.Next() {
		item, err := scanDelivery(rows)
		if err != nil {
			return DeliveryPage{}, err
		}
		item.Attempts, err = s.listAttempts(ctx, item.ID)
		if err != nil {
			return DeliveryPage{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return DeliveryPage{}, err
	}
	page := DeliveryPage{Items: items}
	if len(page.Items) > input.Limit {
		page.Items = page.Items[:input.Limit]
		cursor := encodeDeliveryCursor(page.Items[len(page.Items)-1])
		page.NextCursor = &cursor
	}
	return page, nil
}

func (s *Service) Create(ctx context.Context, ownerID uuid.UUID, input CreateInput, requestID string) (Credential, error) {
	name := strings.TrimSpace(input.Name)
	target, err := validateURL(input.URL, s.allowLocal)
	if !namePattern.MatchString(name) || err != nil {
		return Credential{}, ErrInvalid
	}
	events, ok := normalizeEvents(input.EventTypes)
	if !ok {
		return Credential{}, ErrInvalid
	}
	secret, err := newSigningSecret()
	if err != nil {
		return Credential{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Credential{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := requireEnabled(ctx, tx); err != nil {
		return Credential{}, err
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM developer_webhook_endpoints WHERE owner_id=$1 AND status='active'`, ownerID).Scan(&count); err != nil {
		return Credential{}, err
	}
	if count >= 5 {
		return Credential{}, ErrConflict
	}
	item := Endpoint{ID: uuid.New(), Name: name, URL: target, EventTypes: events, Status: "active", CurrentSecretVersion: 1, Version: 1, Deliveries: []Delivery{}}
	if err := tx.QueryRow(ctx, `
		INSERT INTO developer_webhook_endpoints(id,owner_id,name,url,event_types) VALUES($1,$2,$3,$4,$5)
		RETURNING created_at,updated_at`, item.ID, ownerID, name, target, events).Scan(&item.CreatedAt, &item.UpdatedAt); uniqueViolation(err) {
		return Credential{}, ErrConflict
	} else if err != nil {
		return Credential{}, err
	}
	nonce, ciphertext, err := s.encryptSecret(item.ID, 1, secret)
	if err != nil {
		return Credential{}, err
	}
	item.SecretHint = secret[len(secret)-6:]
	if _, err := tx.Exec(ctx, `INSERT INTO developer_webhook_secret_revisions(endpoint_id,version,nonce,ciphertext,display_hint) VALUES($1,1,$2,$3,$4)`, item.ID, nonce, ciphertext, item.SecretHint); err != nil {
		return Credential{}, err
	}
	if err := audit(ctx, tx, ownerID, "developer.webhook_endpoint_created", "developer_webhook_endpoint", item.ID, "", requestID, map[string]any{"eventTypes": events, "host": endpointHost(target)}); err != nil {
		return Credential{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Credential{}, err
	}
	return Credential{Endpoint: item, SigningSecret: secret}, nil
}

func (s *Service) Rotate(ctx context.Context, ownerID, endpointID uuid.UUID, input Transition, requestID string) (Credential, error) {
	if !validTransition(input) {
		return Credential{}, ErrInvalid
	}
	secret, err := newSigningSecret()
	if err != nil {
		return Credential{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Credential{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := requireEnabled(ctx, tx); err != nil {
		return Credential{}, err
	}
	var item Endpoint
	err = tx.QueryRow(ctx, `
		SELECT id,name,url,event_types,status,current_secret_version,version,created_at,updated_at,revoked_at
		FROM developer_webhook_endpoints WHERE id=$1 AND owner_id=$2 AND status='active' AND version=$3 FOR UPDATE`, endpointID, ownerID, input.ExpectedVersion).Scan(
		&item.ID, &item.Name, &item.URL, &item.EventTypes, &item.Status, &item.CurrentSecretVersion, &item.Version, &item.CreatedAt, &item.UpdatedAt, &item.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Credential{}, ErrConflict
	}
	if err != nil {
		return Credential{}, err
	}
	nextSecretVersion := item.CurrentSecretVersion + 1
	nonce, ciphertext, err := s.encryptSecret(item.ID, nextSecretVersion, secret)
	if err != nil {
		return Credential{}, err
	}
	item.SecretHint = secret[len(secret)-6:]
	if _, err := tx.Exec(ctx, `INSERT INTO developer_webhook_secret_revisions(endpoint_id,version,nonce,ciphertext,display_hint) VALUES($1,$2,$3,$4,$5)`, item.ID, nextSecretVersion, nonce, ciphertext, item.SecretHint); err != nil {
		return Credential{}, err
	}
	if err := tx.QueryRow(ctx, `UPDATE developer_webhook_endpoints SET current_secret_version=$2,version=version+1,updated_at=now() WHERE id=$1 RETURNING current_secret_version,version,updated_at`, item.ID, nextSecretVersion).Scan(&item.CurrentSecretVersion, &item.Version, &item.UpdatedAt); err != nil {
		return Credential{}, err
	}
	if err := audit(ctx, tx, ownerID, "developer.webhook_secret_rotated", "developer_webhook_endpoint", item.ID, input.Reason, requestID, map[string]any{"secretVersion": nextSecretVersion, "host": endpointHost(item.URL)}); err != nil {
		return Credential{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Credential{}, err
	}
	item.Deliveries = []Delivery{}
	return Credential{Endpoint: item, SigningSecret: secret}, nil
}

func (s *Service) Revoke(ctx context.Context, ownerID, endpointID uuid.UUID, input Transition, requestID string) (Endpoint, error) {
	if !validTransition(input) {
		return Endpoint{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Endpoint{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var item Endpoint
	err = tx.QueryRow(ctx, `
		UPDATE developer_webhook_endpoints SET status='revoked',revoked_at=now(),version=version+1,updated_at=now()
		WHERE id=$1 AND owner_id=$2 AND status='active' AND version=$3
		RETURNING id,name,url,event_types,status,current_secret_version,version,created_at,updated_at,revoked_at`, endpointID, ownerID, input.ExpectedVersion).Scan(
		&item.ID, &item.Name, &item.URL, &item.EventTypes, &item.Status, &item.CurrentSecretVersion, &item.Version, &item.CreatedAt, &item.UpdatedAt, &item.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Endpoint{}, ErrConflict
	}
	if err != nil {
		return Endpoint{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE developer_webhook_deliveries SET status='cancelled',version=version+1,updated_at=now(),next_attempt_at=NULL WHERE endpoint_id=$1 AND status IN ('queued','delivering','retry_scheduled')`, endpointID); err != nil {
		return Endpoint{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE jobs SET status='cancelled',updated_at=now() WHERE kind=$1 AND status IN ('queued','running') AND payload->>'deliveryId' IN (SELECT id::text FROM developer_webhook_deliveries WHERE endpoint_id=$2)`, JobKind, endpointID); err != nil {
		return Endpoint{}, err
	}
	if err := audit(ctx, tx, ownerID, "developer.webhook_endpoint_revoked", "developer_webhook_endpoint", item.ID, input.Reason, requestID, map[string]any{"host": endpointHost(item.URL)}); err != nil {
		return Endpoint{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Endpoint{}, err
	}
	item.Deliveries = []Delivery{}
	return item, nil
}

func (s *Service) QueueTest(ctx context.Context, ownerID, endpointID uuid.UUID, requestID string) (Delivery, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Delivery{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := requireEnabled(ctx, tx); err != nil {
		return Delivery{}, err
	}
	var endpointName string
	var secretVersion int
	if err := tx.QueryRow(ctx, `SELECT name,current_secret_version FROM developer_webhook_endpoints WHERE id=$1 AND owner_id=$2 AND status='active' FOR UPDATE`, endpointID, ownerID).Scan(&endpointName, &secretVersion); errors.Is(err, pgx.ErrNoRows) {
		return Delivery{}, ErrNotFound
	} else if err != nil {
		return Delivery{}, err
	}
	eventID := uuid.New()
	createdAt := time.Now().UTC()
	payload := eventPayload(eventID, "developer.webhook.test", createdAt, "developer_webhook_endpoint", &endpointID)
	if _, err := tx.Exec(ctx, `INSERT INTO developer_webhook_events(id,owner_id,event_type,resource_type,resource_id,payload,source_key,created_at) VALUES($1,$2,'developer.webhook.test','developer_webhook_endpoint',$3,$4,$5,$6)`, eventID, ownerID, endpointID, payload, "webhook-test:"+eventID.String(), createdAt); err != nil {
		return Delivery{}, err
	}
	delivery, err := enqueueDeliveryTx(ctx, tx, endpointID, endpointName, eventID, "developer.webhook.test", secretVersion, nil)
	if err != nil {
		return Delivery{}, err
	}
	if err := audit(ctx, tx, ownerID, "developer.webhook_test_queued", "developer_webhook_delivery", delivery.ID, "", requestID, map[string]any{"endpointId": endpointID}); err != nil {
		return Delivery{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Delivery{}, err
	}
	return delivery, nil
}

func EnqueueTx(ctx context.Context, tx pgx.Tx, input EventInput) error {
	if input.OwnerID == uuid.Nil || !validEvent(input.EventType) || strings.TrimSpace(input.ResourceType) == "" || len(input.SourceKey) < 3 || len(input.SourceKey) > 240 {
		return ErrInvalid
	}
	rows, err := tx.Query(ctx, `SELECT id,name,current_secret_version FROM developer_webhook_endpoints WHERE owner_id=$1 AND status='active' AND $2=ANY(event_types) ORDER BY id`, input.OwnerID, input.EventType)
	if err != nil {
		return err
	}
	type target struct {
		id      uuid.UUID
		name    string
		version int
	}
	targets := []target{}
	for rows.Next() {
		var item target
		if err := rows.Scan(&item.id, &item.name, &item.version); err != nil {
			rows.Close()
			return err
		}
		targets = append(targets, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(targets) == 0 {
		return nil
	}
	eventID := uuid.New()
	createdAt := time.Now().UTC()
	payload := eventPayload(eventID, input.EventType, createdAt, input.ResourceType, input.ResourceID)
	result, err := tx.Exec(ctx, `INSERT INTO developer_webhook_events(id,owner_id,event_type,resource_type,resource_id,payload,source_key,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(owner_id,source_key) DO NOTHING`, eventID, input.OwnerID, input.EventType, strings.TrimSpace(input.ResourceType), input.ResourceID, payload, input.SourceKey, createdAt)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return nil
	}
	for _, endpoint := range targets {
		if _, err := enqueueDeliveryTx(ctx, tx, endpoint.id, endpoint.name, eventID, input.EventType, endpoint.version, nil); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ListDeadLetters(ctx context.Context, input DeadLetterListInput) (DeliveryPage, error) {
	input.Query = strings.ToLower(strings.TrimSpace(input.Query))
	input.EventType = strings.ToLower(strings.TrimSpace(input.EventType))
	if len(input.Query) > 120 || (input.EventType != "" && !supportedEvent(input.EventType)) {
		return DeliveryPage{}, ErrInvalidDeadLetterFilter
	}
	if input.Limit == 0 {
		input.Limit = 20
	}
	if input.Limit < 1 || input.Limit > 50 {
		return DeliveryPage{}, ErrInvalidDeadLetterFilter
	}
	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if input.Cursor != "" {
		cursor, err := decodeDeliveryCursor(input.Cursor)
		if err != nil {
			return DeliveryPage{}, err
		}
		cursorTime, cursorID = &cursor.CreatedAt, &cursor.ID
	}
	rows, err := s.pool.Query(ctx, deliverySelect+`
		WHERE d.status='dead_letter'
		  AND ($1='' OR strpos(lower(e.name),$1)>0 OR strpos(lower(u.handle),$1)>0 OR strpos(lower(v.event_type),$1)>0 OR strpos(lower(COALESCE(d.last_error_code,'')),$1)>0)
		  AND ($2='' OR v.event_type=$2)
		  AND ($3::timestamptz IS NULL OR (d.created_at,d.id)<($3,$4::uuid))
		ORDER BY d.created_at DESC,d.id DESC LIMIT $5`, input.Query, input.EventType, cursorTime, cursorID, input.Limit+1)
	if err != nil {
		return DeliveryPage{}, fmt.Errorf("list webhook dead letters: %w", err)
	}
	defer rows.Close()
	items := make([]Delivery, 0)
	for rows.Next() {
		item, err := scanDelivery(rows)
		if err != nil {
			return DeliveryPage{}, err
		}
		item.Attempts, err = s.listAttempts(ctx, item.ID)
		if err != nil {
			return DeliveryPage{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return DeliveryPage{}, err
	}
	page := DeliveryPage{Items: items}
	if len(page.Items) > input.Limit {
		page.Items = page.Items[:input.Limit]
		cursor := encodeDeliveryCursor(page.Items[len(page.Items)-1])
		page.NextCursor = &cursor
	}
	return page, nil
}

func (s *Service) Replay(ctx context.Context, _ uuid.UUID, deliveryID uuid.UUID, input AdminTransition, _ string) (Delivery, error) {
	if input.ExpectedVersion < 1 {
		return Delivery{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Delivery{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var endpointID, eventID, ownerID uuid.UUID
	var endpointName string
	var secretVersion int
	err = tx.QueryRow(ctx, `
		SELECT d.endpoint_id,d.event_id,e.owner_id,e.name,e.current_secret_version
		FROM developer_webhook_deliveries d JOIN developer_webhook_endpoints e ON e.id=d.endpoint_id
		WHERE d.id=$1 AND d.status='dead_letter' AND d.version=$2 AND e.status='active' FOR UPDATE`, deliveryID, input.ExpectedVersion).Scan(&endpointID, &eventID, &ownerID, &endpointName, &secretVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Delivery{}, ErrConflict
	}
	if err != nil {
		return Delivery{}, err
	}
	var eventType string
	if err := tx.QueryRow(ctx, `SELECT event_type FROM developer_webhook_events WHERE id=$1`, eventID).Scan(&eventType); err != nil {
		return Delivery{}, err
	}
	delivery, err := enqueueDeliveryTx(ctx, tx, endpointID, endpointName, eventID, eventType, secretVersion, &deliveryID)
	if err != nil {
		return Delivery{}, err
	}
	if err := notifications.CreateTx(ctx, tx, notifications.CreateInput{UserID: ownerID, Kind: "security.webhook_replayed", Title: "Webhook delivery replayed", Body: "Operations replayed a dead-letter webhook delivery.", TargetPath: "/settings", ResourceType: "developer_webhook_delivery", ResourceID: &delivery.ID, SourceKey: "webhook-replay:" + delivery.ID.String()}); err != nil {
		return Delivery{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Delivery{}, err
	}
	return delivery, nil
}

const deliverySelect = `
	SELECT d.id,d.endpoint_id,e.name,e.url,u.handle,d.event_id,v.event_type,d.status,d.version,d.attempt_count,d.next_attempt_at,d.last_status_code,d.last_error_code,d.original_delivery_id,d.created_at,d.updated_at,d.succeeded_at,d.dead_lettered_at
	FROM developer_webhook_deliveries d
	JOIN developer_webhook_endpoints e ON e.id=d.endpoint_id
	JOIN developer_webhook_events v ON v.id=d.event_id
	JOIN users u ON u.id=e.owner_id`

func scanDelivery(row pgx.Row) (Delivery, error) {
	var item Delivery
	var endpointURL string
	if err := row.Scan(&item.ID, &item.EndpointID, &item.EndpointName, &endpointURL, &item.OwnerHandle, &item.EventID, &item.EventType, &item.Status, &item.Version, &item.AttemptCount, &item.NextAttemptAt, &item.LastStatusCode, &item.LastErrorCode, &item.OriginalDeliveryID, &item.CreatedAt, &item.UpdatedAt, &item.SucceededAt, &item.DeadLetteredAt); err != nil {
		return Delivery{}, err
	}
	item.EndpointHost = endpointHost(endpointURL)
	return item, nil
}

func encodeDeliveryCursor(item Delivery) string {
	body, _ := json.Marshal(deliveryCursor{CreatedAt: item.CreatedAt, ID: item.ID})
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodeDeliveryCursor(value string) (deliveryCursor, error) {
	var cursor deliveryCursor
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &cursor) != nil || cursor.CreatedAt.IsZero() || cursor.ID == uuid.Nil {
		return deliveryCursor{}, ErrInvalidDeadLetterFilter
	}
	return cursor, nil
}

func decodeOwnerDeliveryCursor(value string) (deliveryCursor, error) {
	var cursor deliveryCursor
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &cursor) != nil || cursor.CreatedAt.IsZero() || cursor.ID == uuid.Nil {
		return deliveryCursor{}, ErrInvalidOwnerFilter
	}
	return cursor, nil
}

func supportedEvent(value string) bool {
	for _, eventType := range eventCatalog {
		if value == eventType {
			return true
		}
	}
	return false
}

func (s *Service) listAttempts(ctx context.Context, deliveryID uuid.UUID) ([]Attempt, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,attempt_number,status_code,error_code,response_sha256,duration_ms,attempted_at FROM developer_webhook_delivery_attempts WHERE delivery_id=$1 ORDER BY attempt_number`, deliveryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Attempt{}
	for rows.Next() {
		var item Attempt
		if err := rows.Scan(&item.ID, &item.AttemptNumber, &item.StatusCode, &item.ErrorCode, &item.ResponseSHA256, &item.DurationMS, &item.AttemptedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func enqueueDeliveryTx(ctx context.Context, tx pgx.Tx, endpointID uuid.UUID, endpointName string, eventID uuid.UUID, eventType string, secretVersion int, originalID *uuid.UUID) (Delivery, error) {
	var secretRevisionID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM developer_webhook_secret_revisions WHERE endpoint_id=$1 AND version=$2`, endpointID, secretVersion).Scan(&secretRevisionID); err != nil {
		return Delivery{}, err
	}
	item := Delivery{EndpointID: endpointID, EndpointName: endpointName, EventID: eventID, EventType: eventType, Status: "queued", Version: 1, Attempts: []Attempt{}, OriginalDeliveryID: originalID}
	if err := tx.QueryRow(ctx, `INSERT INTO developer_webhook_deliveries(endpoint_id,event_id,secret_revision_id,original_delivery_id) VALUES($1,$2,$3,$4) RETURNING id,created_at,updated_at`, endpointID, eventID, secretRevisionID, originalID).Scan(&item.ID, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return Delivery{}, err
	}
	payload, _ := json.Marshal(map[string]any{"deliveryId": item.ID})
	if _, err := tx.Exec(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,$2,5)`, JobKind, payload); err != nil {
		return Delivery{}, err
	}
	return item, nil
}

func eventPayload(id uuid.UUID, eventType string, createdAt time.Time, resourceType string, resourceID *uuid.UUID) json.RawMessage {
	body, _ := json.Marshal(map[string]any{"id": id, "type": eventType, "createdAt": createdAt, "data": map[string]any{"resourceType": resourceType, "resourceId": resourceID}})
	return body
}

func requireEnabled(ctx context.Context, tx pgx.Tx) error {
	var enabled bool
	if err := tx.QueryRow(ctx, `SELECT enabled FROM developer_access_control WHERE singleton=true`).Scan(&enabled); err != nil {
		return err
	}
	if !enabled {
		return ErrDisabled
	}
	return nil
}

func (s *Service) encryptSecret(endpointID uuid.UUID, version int, secret string) ([]byte, []byte, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	ciphertext := gcm.Seal(nil, nonce, []byte(secret), []byte(fmt.Sprintf("%s:%d", endpointID, version)))
	return nonce, ciphertext, nil
}

func (s *Service) decryptSecret(endpointID uuid.UUID, version int, nonce, ciphertext []byte) (string, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, []byte(fmt.Sprintf("%s:%d", endpointID, version)))
	if err != nil {
		return "", errors.New("webhook secret decryption failed")
	}
	return string(plaintext), nil
}

func newSigningSecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "whsec_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

func normalizeEvents(values []string) ([]string, bool) {
	seen := map[string]bool{}
	items := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if !validEvent(value) || seen[value] {
			return nil, false
		}
		seen[value] = true
		items = append(items, value)
	}
	sort.Strings(items)
	return items, len(items) > 0 && len(items) <= len(eventCatalog)
}

func validEvent(value string) bool {
	for _, candidate := range eventCatalog {
		if value == candidate {
			return true
		}
	}
	return false
}

func validTransition(input Transition) bool {
	reason := strings.TrimSpace(input.Reason)
	return input.Confirmed && input.ExpectedVersion > 0 && len(reason) >= 10 && len(reason) <= 500
}

func validateURL(raw string, allowLocal bool) (string, error) {
	value := strings.TrimSpace(raw)
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" || parsed.RawQuery != "" || len(value) > 2048 {
		return "", ErrInvalid
	}
	if parsed.Scheme != "https" && !(allowLocal && parsed.Scheme == "http") {
		return "", ErrInvalid
	}
	host := strings.ToLower(parsed.Hostname())
	if ip := net.ParseIP(host); ip != nil && !allowedIP(ip, allowLocal) {
		return "", ErrInvalid
	}
	if host == "localhost" && !allowLocal {
		return "", ErrInvalid
	}
	parsed.Host = strings.ToLower(parsed.Host)
	return parsed.String(), nil
}

func allowedIP(ip net.IP, allowLocal bool) bool {
	if allowLocal && ip.IsLoopback() {
		return true
	}
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast()
}

func endpointHost(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "invalid"
	}
	return parsed.Host
}

func audit(ctx context.Context, tx pgx.Tx, actorID uuid.UUID, action, resourceType string, resourceID uuid.UUID, reason, requestID string, metadata any) error {
	if strings.TrimSpace(requestID) == "" {
		requestID = "webhook-worker"
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id,metadata) VALUES($1,$2,$3,$4,NULLIF($5,''),$6,$7)`, actorID, action, resourceType, resourceID, strings.TrimSpace(reason), requestID, raw)
	return err
}

func uniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "SQLSTATE 23505")
}

func sha256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
