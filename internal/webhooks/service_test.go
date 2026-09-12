package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/database"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestWebhookEncryptedSecretSignedDeliveryDeadLetterAndReplay(t *testing.T) {
	pool, cleanup := webhookTestPool(t)
	defer cleanup()
	ctx := context.Background()
	ownerID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Webhook Owner','admin')`, ownerID, "webhook_"+ownerID.String()+"@example.test", "webhook_"+ownerID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE developer_access_control SET enabled=true,version=version+1`); err != nil {
		t.Fatal(err)
	}

	type receivedRequest struct {
		body      []byte
		eventID   string
		timestamp string
		signature string
	}
	received := make(chan receivedRequest, 4)
	var receiverStatus struct {
		sync.Mutex
		code int
	}
	receiverStatus.code = http.StatusNoContent
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- receivedRequest{body: body, eventID: r.Header.Get("HCAI-Webhook-Id"), timestamp: r.Header.Get("HCAI-Webhook-Timestamp"), signature: r.Header.Get("HCAI-Webhook-Signature")}
		receiverStatus.Lock()
		code := receiverStatus.code
		receiverStatus.Unlock()
		w.WriteHeader(code)
		_, _ = w.Write([]byte("receiver evidence"))
	}))
	defer receiver.Close()

	encryptionKey := bytes.Repeat([]byte{0x42}, 32)
	service := NewService(pool, encryptionKey, true)
	credential, err := service.Create(ctx, ownerID, CreateInput{Name: "Primary event receiver", URL: receiver.URL + "/hcai", EventTypes: []string{"developer.webhook.test", "generation.completed"}}, "webhook-create-request")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(credential.SigningSecret, "whsec_") || credential.SecretHint == "" || credential.Version != 1 {
		t.Fatalf("one-time credential mismatch: %#v", credential)
	}
	var nonce, ciphertext []byte
	if err := pool.QueryRow(ctx, `SELECT nonce,ciphertext FROM developer_webhook_secret_revisions WHERE endpoint_id=$1 AND version=1`, credential.ID).Scan(&nonce, &ciphertext); err != nil {
		t.Fatal(err)
	}
	if len(nonce) != 12 || bytes.Contains(ciphertext, []byte(credential.SigningSecret)) {
		t.Fatalf("signing secret was not encrypted safely: nonce=%d ciphertext=%x", len(nonce), ciphertext)
	}
	access, err := service.GetAccess(ctx, ownerID)
	if err != nil {
		t.Fatal(err)
	}
	encodedAccess, _ := json.Marshal(access)
	if bytes.Contains(encodedAccess, []byte(credential.SigningSecret)) || bytes.Contains(encodedAccess, ciphertext) || strings.Contains(string(encodedAccess), "ciphertext") {
		t.Fatalf("safe projection exposed secret material: %s", encodedAccess)
	}

	delivery, err := service.QueueTest(ctx, ownerID, credential.ID, "webhook-test-request")
	if err != nil {
		t.Fatal(err)
	}
	repository := jobs.NewRepository(pool)
	job, err := repository.Claim(ctx, "webhook-worker", 30*time.Second)
	if err != nil || job.Kind != JobKind {
		t.Fatalf("claim Webhook job: kind=%q err=%v", job.Kind, err)
	}
	if err := service.HandleJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := repository.Complete(ctx, job, "webhook-worker"); err != nil {
		t.Fatal(err)
	}
	request := <-received
	if request.eventID == "" || request.timestamp == "" || !json.Valid(request.body) {
		t.Fatalf("Webhook request envelope mismatch: %#v", request)
	}
	mac := hmac.New(sha256.New, []byte(credential.SigningSecret))
	_, _ = mac.Write([]byte(request.timestamp + "."))
	_, _ = mac.Write(request.body)
	if expected := "v1=" + hex.EncodeToString(mac.Sum(nil)); !hmac.Equal([]byte(expected), []byte(request.signature)) {
		t.Fatalf("Webhook signature mismatch: got=%q expected=%q", request.signature, expected)
	}
	access, err = service.GetAccess(ctx, ownerID)
	if err != nil {
		t.Fatal(err)
	}
	completed := findDelivery(access, delivery.ID)
	if completed.Status != "succeeded" || completed.AttemptCount != 1 || len(completed.Attempts) != 1 || completed.Attempts[0].ResponseSHA256 == nil {
		t.Fatalf("successful delivery evidence mismatch: %#v", completed)
	}

	rotated, err := service.Rotate(ctx, ownerID, credential.ID, Transition{ExpectedVersion: credential.Version, Reason: "Rotate after receiver verification evidence", Confirmed: true}, "webhook-rotate-request")
	if err != nil {
		t.Fatal(err)
	}
	if rotated.SigningSecret == credential.SigningSecret || rotated.CurrentSecretVersion != 2 || rotated.Version != 2 {
		t.Fatalf("signing-secret rotation mismatch: %#v", rotated)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	generationID := uuid.New()
	eventInput := EventInput{OwnerID: ownerID, EventType: "generation.completed", ResourceType: "generation", ResourceID: &generationID, SourceKey: "generation:" + generationID.String() + ":completed"}
	if err := EnqueueTx(ctx, tx, eventInput); err != nil {
		t.Fatal(err)
	}
	if err := EnqueueTx(ctx, tx, eventInput); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var eventCount, deliveryCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM developer_webhook_events WHERE owner_id=$1 AND source_key=$2`, ownerID, eventInput.SourceKey).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM developer_webhook_deliveries d JOIN developer_webhook_events e ON e.id=d.event_id WHERE e.owner_id=$1 AND e.source_key=$2`, ownerID, eventInput.SourceKey).Scan(&deliveryCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 || deliveryCount != 1 {
		t.Fatalf("Webhook outbox idempotency mismatch: events=%d deliveries=%d", eventCount, deliveryCount)
	}
	job, err = repository.Claim(ctx, "webhook-worker", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.HandleJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := repository.Complete(ctx, job, "webhook-worker"); err != nil {
		t.Fatal(err)
	}
	rotatedRequest := <-received
	rotatedMAC := hmac.New(sha256.New, []byte(rotated.SigningSecret))
	_, _ = rotatedMAC.Write([]byte(rotatedRequest.timestamp + "."))
	_, _ = rotatedMAC.Write(rotatedRequest.body)
	if expected := "v1=" + hex.EncodeToString(rotatedMAC.Sum(nil)); !hmac.Equal([]byte(expected), []byte(rotatedRequest.signature)) {
		t.Fatal("new event was not signed with the active secret revision")
	}

	receiverStatus.Lock()
	receiverStatus.code = http.StatusBadRequest
	receiverStatus.Unlock()
	failed, err := service.QueueTest(ctx, ownerID, credential.ID, "webhook-failure-request")
	if err != nil {
		t.Fatal(err)
	}
	job, err = repository.Claim(ctx, "webhook-worker", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.HandleJob(ctx, job); err != nil {
		t.Fatalf("terminal receiver rejection should not retry: %v", err)
	}
	if err := repository.Complete(ctx, job, "webhook-worker"); err != nil {
		t.Fatal(err)
	}
	<-received
	deadLetters, err := service.ListDeadLetters(ctx, DeadLetterListInput{})
	if err != nil || len(deadLetters.Items) != 1 || deadLetters.Items[0].ID != failed.ID || deadLetters.Items[0].LastStatusCode == nil || *deadLetters.Items[0].LastStatusCode != http.StatusBadRequest {
		t.Fatalf("dead-letter evidence mismatch: items=%#v err=%v", deadLetters, err)
	}
	replayed, err := service.Replay(ctx, ownerID, failed.ID, AdminTransition{ExpectedVersion: deadLetters.Items[0].Version}, "webhook-replay-request")
	if err != nil || replayed.OriginalDeliveryID == nil || *replayed.OriginalDeliveryID != failed.ID {
		t.Fatalf("Admin replay mismatch: item=%#v err=%v", replayed, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE developer_webhook_delivery_attempts SET duration_ms=0 WHERE delivery_id=$1`, failed.ID); err == nil {
		t.Fatal("Webhook attempt evidence accepted mutation")
	}

	if _, err := service.Create(ctx, ownerID, CreateInput{Name: "Metadata service", URL: "http://169.254.169.254/latest", EventTypes: []string{"developer.webhook.test"}}, "blocked-target"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("metadata target was not rejected: %v", err)
	}
	if _, err := service.Create(ctx, ownerID, CreateInput{Name: "Query secret", URL: receiver.URL + "/hook?token=secret", EventTypes: []string{"developer.webhook.test"}}, "blocked-query"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("query-bearing target was not rejected: %v", err)
	}
	productionBoundary := NewService(pool, encryptionKey, false)
	if _, err := productionBoundary.Create(ctx, ownerID, CreateInput{Name: "Loopback production", URL: receiver.URL, EventTypes: []string{"developer.webhook.test"}}, "blocked-loopback"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("production loopback target was not rejected: %v", err)
	}
}

func TestWebhookDeadLetterDirectoryPaginationAndExactReplay(t *testing.T) {
	pool, cleanup := webhookTestPool(t)
	defer cleanup()
	ctx := context.Background()
	ownerID := uuid.New()
	handle := "webhook_scale_" + ownerID.String()[:8]
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role,status) VALUES($1,$2,$3,'Webhook Scale Admin','admin','active')`, ownerID, ownerID.String()+"@test.local", handle); err != nil {
		t.Fatal(err)
	}
	endpointID, revisionID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO developer_webhook_endpoints(id,owner_id,name,url,event_types) VALUES($1,$2,'Scale receiver','http://127.0.0.1:9999/hook',ARRAY['developer.webhook.test'])`, endpointID, ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO developer_webhook_secret_revisions(id,endpoint_id,version,nonce,ciphertext,display_hint) VALUES($1,$2,1,$3,$4,'...test')`, revisionID, endpointID, bytes.Repeat([]byte{0x01}, 12), bytes.Repeat([]byte{0x02}, 17)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		WITH inserted_events AS (
		  INSERT INTO developer_webhook_events(id,owner_id,event_type,resource_type,payload,source_key,created_at)
		  SELECT gen_random_uuid(),$1,'developer.webhook.test','test','{}'::jsonb,'scale-event:'||value::text,now()-make_interval(secs=>value)
		  FROM generate_series(1,106) value RETURNING id,created_at
		)
		INSERT INTO developer_webhook_deliveries(endpoint_id,event_id,secret_revision_id,status,version,attempt_count,last_error_code,created_at,updated_at,dead_lettered_at)
		SELECT $2,id,$3,'dead_letter',1,1,'receiver_rejected',created_at,created_at,created_at FROM inserted_events`, ownerID, endpointID, revisionID); err != nil {
		t.Fatal(err)
	}
	service := NewService(pool, bytes.Repeat([]byte{0x21}, 32), true)
	access, err := service.GetAccess(ctx, ownerID)
	if err != nil || len(access.Endpoints) != 1 || len(access.Endpoints[0].Deliveries) != 5 || access.Endpoints[0].DeliveryNextCursor == nil {
		t.Fatalf("bounded owner Webhook access mismatch: access=%#v err=%v", access, err)
	}
	ownerSeen := make(map[uuid.UUID]struct{})
	ownerCursor := ""
	for {
		page, err := service.ListEndpointDeliveries(ctx, ownerID, endpointID, OwnerDeliveryListInput{Cursor: ownerCursor, Limit: 25})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if _, duplicate := ownerSeen[item.ID]; duplicate {
				t.Fatalf("duplicate owner Webhook delivery %s", item.ID)
			}
			ownerSeen[item.ID] = struct{}{}
		}
		if page.NextCursor == nil {
			break
		}
		ownerCursor = *page.NextCursor
	}
	if len(ownerSeen) != 106 {
		t.Fatalf("expected 106 owner Webhook deliveries, got %d", len(ownerSeen))
	}
	if _, err := service.ListEndpointDeliveries(ctx, ownerID, endpointID, OwnerDeliveryListInput{Cursor: ownerCursor + "modified", Limit: 25}); !errors.Is(err, ErrInvalidOwnerFilter) {
		t.Fatalf("modified owner Webhook cursor accepted: %v", err)
	}
	if _, err := service.ListEndpointDeliveries(ctx, ownerID, endpointID, OwnerDeliveryListInput{Limit: 51}); !errors.Is(err, ErrInvalidOwnerFilter) {
		t.Fatalf("oversized owner Webhook page accepted: %v", err)
	}
	if _, err := service.ListEndpointDeliveries(ctx, uuid.New(), endpointID, OwnerDeliveryListInput{Cursor: "modified", Limit: 25}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Webhook ownership was not enforced before the cursor: %v", err)
	}
	seen := make(map[uuid.UUID]struct{})
	var oldest Delivery
	cursor := ""
	for {
		page, err := service.ListDeadLetters(ctx, DeadLetterListInput{Query: handle, EventType: "developer.webhook.test", Cursor: cursor, Limit: 25})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if _, duplicate := seen[item.ID]; duplicate {
				t.Fatalf("duplicate Webhook dead letter %s", item.ID)
			}
			seen[item.ID] = struct{}{}
			oldest = item
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(seen) != 106 {
		t.Fatalf("expected 106 Webhook dead letters, got %d", len(seen))
	}
	if _, err := service.ListDeadLetters(ctx, DeadLetterListInput{Cursor: cursor + "modified", Limit: 25}); !errors.Is(err, ErrInvalidDeadLetterFilter) {
		t.Fatalf("modified Webhook cursor accepted: %v", err)
	}
	replayed, err := service.Replay(ctx, ownerID, oldest.ID, AdminTransition{ExpectedVersion: oldest.Version}, "webhook-scale-replay")
	if err != nil || replayed.OriginalDeliveryID == nil || *replayed.OriginalDeliveryID != oldest.ID {
		t.Fatalf("oldest Webhook replay failed: %#v %v", replayed, err)
	}
}

func findDelivery(access Access, id uuid.UUID) Delivery {
	for _, endpoint := range access.Endpoints {
		for _, delivery := range endpoint.Deliveries {
			if delivery.ID == id {
				return delivery
			}
		}
	}
	return Delivery{}
}

func webhookTestPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	ctx := context.Background()
	baseURL := os.Getenv("TEST_DATABASE_URL")
	if baseURL == "" {
		baseURL = "postgres://hcai:hcai@localhost:5432/hcai?sslmode=disable"
	}
	root, err := pgxpool.New(ctx, baseURL)
	if err != nil {
		testutil.DatabaseUnavailable(t, err)
	}
	if err := root.Ping(ctx); err != nil {
		root.Close()
		testutil.DatabaseUnavailable(t, err)
	}
	schema := "test_webhooks_" + uuid.NewString()[:8]
	if _, err := root.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		root.Close()
		t.Fatal(err)
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	pool, err := database.Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		_, _ = root.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		root.Close()
	}
}
