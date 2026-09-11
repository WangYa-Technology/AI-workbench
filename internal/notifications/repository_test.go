package notifications_test

import (
	"context"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/hcai-chat/hcai-chat/internal/platform/database"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestNotificationOwnershipDeepLinksReadStateAndPreferences(t *testing.T) {
	pool, cleanup := notificationTestPool(t)
	defer cleanup()
	ctx := context.Background()
	ownerID, outsiderID, taskID := uuid.New(), uuid.New(), uuid.New()
	for index, userID := range []uuid.UUID{ownerID, outsiderID} {
		if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role,status) VALUES($1,$2,$3,$4,'member','active')`, userID, userID.String()+"@test.local", "notify_user_"+userID.String()[:8], []string{"Owner", "Outsider"}[index]); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	create := notifications.CreateInput{UserID: ownerID, Kind: "task.proposal_accepted", Title: "Proposal accepted", Body: "Your proposal is ready for production.", TargetPath: "/market/demands/" + taskID.String(), ResourceType: "task", ResourceID: &taskID, SourceKey: "task:" + taskID.String() + ":accepted"}
	if err := notifications.CreateTx(ctx, tx, create); err != nil {
		t.Fatal(err)
	}
	if err := notifications.CreateTx(ctx, tx, create); err != nil {
		t.Fatalf("idempotent notification replay failed: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var notificationCount, deliveryJobCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE user_id=$1 AND source_key=$2`, ownerID, create.SourceKey).Scan(&notificationCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind=$1`, notifications.JobKind).Scan(&deliveryJobCount); err != nil {
		t.Fatal(err)
	}
	if notificationCount != 1 || deliveryJobCount != 1 {
		t.Fatalf("notification replay duplicated durable work: notifications=%d jobs=%d", notificationCount, deliveryJobCount)
	}
	repository := notifications.NewRepository(pool)
	jobRepository := jobs.NewRepository(pool)
	deliverNext(t, ctx, repository, jobRepository)
	page, err := repository.List(ctx, ownerID, notifications.ListInput{ReadState: "unread", Limit: 20})
	if err != nil || len(page.Items) != 1 || page.UnreadCount != 1 || page.Items[0].TargetPath != create.TargetPath {
		t.Fatalf("unexpected inbox: %#v err=%v", page, err)
	}
	if _, err := repository.MarkRead(ctx, outsiderID, page.Items[0].ID); !errors.Is(err, notifications.ErrNotFound) {
		t.Fatalf("outsider could infer or mutate notification: %v", err)
	}
	read, err := repository.MarkRead(ctx, ownerID, page.Items[0].ID)
	if err != nil || read.ReadAt == nil {
		t.Fatalf("mark read failed: %#v err=%v", read, err)
	}
	preferences, err := repository.ListPreferences(ctx, ownerID)
	if err != nil || len(preferences) != len(notifications.KnownKinds) {
		t.Fatalf("default preferences missing: %#v err=%v", preferences, err)
	}
	if _, err := repository.UpdatePreference(ctx, ownerID, "generation.completed", false, 2); !errors.Is(err, notifications.ErrConflict) {
		t.Fatalf("missing preference accepted a non-initial version: %v", err)
	}
	updated, err := repository.UpdatePreference(ctx, ownerID, "task.proposal_accepted", false, 1)
	if err != nil || updated.InAppEnabled || updated.Version != 2 {
		t.Fatalf("preference update failed: %#v err=%v", updated, err)
	}
	if _, err := repository.UpdatePreference(ctx, ownerID, "task.proposal_accepted", true, 1); !errors.Is(err, notifications.ErrConflict) {
		t.Fatalf("stale preference version accepted: %v", err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	create.SourceKey += ":disabled"
	if err := notifications.CreateTx(ctx, tx, create); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	deliverNext(t, ctx, repository, jobRepository)
	all, err := repository.List(ctx, ownerID, notifications.ListInput{ReadState: "all", Limit: 20})
	if err != nil || len(all.Items) != 1 {
		t.Fatalf("disabled preference created new notification: %#v err=%v", all, err)
	}
	evidence, err := repository.ListDeliveryEvidence(ctx, ownerID)
	if err != nil || len(evidence) != 2 || evidence[0].Status != "suppressed" || evidence[0].ErrorCode == nil || *evidence[0].ErrorCode != "preference_disabled" || evidence[0].Attempts != 1 || evidence[1].Status != "delivered" {
		t.Fatalf("notification delivery evidence mismatch: %#v err=%v", evidence, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE notifications SET delivery_status='queued',delivered_at=NULL WHERE id=$1`, evidence[1].ID); err == nil {
		t.Fatal("terminal notification delivery evidence was mutable")
	}
	invalidPayloadError := repository.HandleDeliveryJob(ctx, jobs.Job{Kind: notifications.JobKind, Payload: []byte(`{"notificationId":`)})
	coded, ok := invalidPayloadError.(interface{ ErrorCode() string })
	if !ok || coded.ErrorCode() != "notification_payload_invalid" {
		t.Fatalf("invalid payload did not return a safe error code: %v", invalidPayloadError)
	}
}

func deliverNext(t *testing.T, ctx context.Context, repository *notifications.Repository, jobRepository *jobs.Repository) {
	t.Helper()
	job, err := jobRepository.Claim(ctx, "notification-test-worker", time.Minute)
	if err != nil || job.Kind != notifications.JobKind {
		t.Fatalf("claim notification delivery: job=%#v err=%v", job, err)
	}
	if err := repository.HandleDeliveryJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := jobRepository.Complete(ctx, job, "notification-test-worker"); err != nil {
		t.Fatal(err)
	}
}

func TestNotificationTargetAllowlist(t *testing.T) {
	valid := []string{"/notifications", "/workspace/orders", "/market/demands/" + uuid.NewString(), "/workspace/assets/" + uuid.NewString(), "/community/posts/" + uuid.NewString()}
	for _, target := range valid {
		if !notifications.ValidTargetPath(target) {
			t.Errorf("valid target rejected: %s", target)
		}
	}
	invalid := []string{"https://example.com", "//example.com", "/market/demands/not-an-id", "/settings?redirect=https://example.com", "/unknown"}
	for _, target := range invalid {
		if notifications.ValidTargetPath(target) {
			t.Errorf("unsafe target accepted: %s", target)
		}
	}
}

func TestDeliveryEvidenceStablePaginationAndOwnership(t *testing.T) {
	pool, cleanup := notificationTestPool(t)
	defer cleanup()
	ctx := context.Background()
	ownerID, outsiderID := uuid.New(), uuid.New()
	for index, userID := range []uuid.UUID{ownerID, outsiderID} {
		if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role,status) VALUES($1,$2,$3,$4,'member','active')`, userID, userID.String()+"@test.local", "delivery_"+userID.String()[:8], []string{"Delivery Owner", "Delivery Outsider"}[index]); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	batch := &pgx.Batch{}
	for index := 0; index < 106; index++ {
		batch.Queue(`INSERT INTO notifications(id,user_id,kind,title,body,target_path,delivery_status,delivered_at,created_at) VALUES($1,$2,'generation.completed','Generation ready','Generation completed.','/notifications','delivered',$3,$3)`, uuid.New(), ownerID, base)
	}
	batch.Queue(`INSERT INTO notifications(id,user_id,kind,title,body,target_path,delivery_status,delivered_at,created_at) VALUES($1,$2,'generation.completed','Outsider delivery','Private delivery evidence.','/notifications','delivered',$3,$3)`, uuid.New(), outsiderID, base.Add(time.Hour))
	results := pool.SendBatch(ctx, batch)
	if err := results.Close(); err != nil {
		t.Fatal(err)
	}

	repository := notifications.NewRepository(pool)
	seen := make(map[uuid.UUID]struct{})
	cursor := ""
	for {
		page, err := repository.ListDeliveryEvidencePage(ctx, ownerID, notifications.DeliveryEvidenceInput{Cursor: cursor, Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if !item.CreatedAt.Equal(base) || item.Status != "delivered" {
				t.Fatalf("foreign delivery evidence leaked: %#v", item)
			}
			if _, duplicate := seen[item.ID]; duplicate {
				t.Fatalf("duplicate delivery evidence %s", item.ID)
			}
			seen[item.ID] = struct{}{}
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(seen) != 106 {
		t.Fatalf("expected 106 owned delivery records, got %d", len(seen))
	}
	if _, err := repository.ListDeliveryEvidencePage(ctx, ownerID, notifications.DeliveryEvidenceInput{Cursor: cursor + "modified", Limit: 50}); !errors.Is(err, notifications.ErrInvalidDeliveryFilter) {
		t.Fatalf("modified delivery cursor was accepted: %v", err)
	}
	if _, err := repository.ListDeliveryEvidencePage(ctx, ownerID, notifications.DeliveryEvidenceInput{Limit: 51}); !errors.Is(err, notifications.ErrInvalidDeliveryFilter) {
		t.Fatalf("oversized delivery page was accepted: %v", err)
	}
}

func notificationTestPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	ctx := context.Background()
	baseURL := os.Getenv("TEST_DATABASE_URL")
	if baseURL == "" {
		baseURL = "postgres://hcai:hcai@localhost:5432/hcai?sslmode=disable"
	}
	admin, err := pgxpool.New(ctx, baseURL)
	if err != nil {
		t.Skipf("PostgreSQL integration database unavailable: %v", err)
	}
	if err := admin.Ping(ctx); err != nil {
		admin.Close()
		t.Skipf("PostgreSQL integration database unavailable: %v", err)
	}
	schema := "test_notifications_" + uuid.NewString()[:8]
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(baseURL)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	pool, err := database.Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	}
}
