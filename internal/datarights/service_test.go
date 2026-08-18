package datarights_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/emailactions"
	"github.com/hcai-chat/hcai-chat/internal/identity"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/hcai-chat/hcai-chat/internal/platform/database"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/support"
	"github.com/hcai-chat/hcai-chat/internal/webhooks"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestExportAndDeletionLifecycle(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := context.Background()
	identityRepository := identity.NewRepository(pool)
	handle := "rights_" + uuid.NewString()[:8]
	user, token, err := identityRepository.Register(ctx, identity.RegisterInput{
		Email: handle + "@test.local", Password: "local-test-password", Handle: handle,
		DisplayName: "Rights Owner", Locale: "en-US", Timezone: "UTC",
	}, identity.ClientInfo{Label: "Data rights test", NetworkHash: identity.HashNetwork("127.0.0.1"), RequestID: "rights-register"})
	if err != nil {
		t.Fatal(err)
	}
	mediaRoot := t.TempDir()
	service := datarights.NewService(pool, mediaRoot)
	assetService := assets.NewService(pool, mediaRoot)
	jobRepository := jobs.NewRepository(pool)
	rootAsset, err := assetService.Upload(ctx, user.ID, assets.UploadInput{
		Title: "Data rights source", Filename: "rights-source.txt", Reader: strings.NewReader("Data rights Asset v1."), RequestID: "rights-asset-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	job := claimDataRightsJobKind(t, ctx, pool, "data-rights-test", assets.ScanJobKind)
	if err := assetService.HandleScanJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := jobRepository.Complete(ctx, job, "data-rights-test"); err != nil {
		t.Fatal(err)
	}
	versionAsset, err := assetService.UploadVersion(ctx, user.ID, rootAsset.ID, assets.VersionInput{
		UploadInput: assets.UploadInput{Title: "Data rights source revised", Filename: "rights-source-v2.txt", Reader: strings.NewReader("Data rights Asset v2."), RequestID: "rights-asset-v2"},
		Note:        "Replace private working copy with the reviewed source.",
	})
	if err != nil {
		t.Fatal(err)
	}
	job = claimDataRightsJobKind(t, ctx, pool, "data-rights-test", assets.ScanJobKind)
	if err := assetService.HandleScanJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := jobRepository.Complete(ctx, job, "data-rights-test"); err != nil {
		t.Fatal(err)
	}
	supportService := support.NewService(pool)
	supportCase, err := supportService.Create(ctx, user.ID, support.CreateInput{
		Category: "general_support", Subject: "Export and remove my private support data",
		Details: "This private support message must appear in my export and be redacted after deletion.", Locale: "en-US",
	}, "support-create")
	if err != nil {
		t.Fatal(err)
	}
	adminID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role,status) VALUES($1,$2,$3,'Data Rights Admin','admin','active')`, adminID, adminID.String()+"@test.local", "admin_"+adminID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	supportCase, err = supportService.AdminReply(ctx, adminID, supportCase.ID, support.AdminReplyInput{
		Body:   "We received the request and will preserve only bounded operational evidence.",
		Reason: "Confirm the private support thread is represented in the owner export.", ExpectedVersion: supportCase.Version, Confirmed: true,
	}, "support-reply")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE developer_access_control SET enabled=true,version=version+1`); err != nil {
		t.Fatal(err)
	}
	webhookKey := sha256.Sum256([]byte("data-rights-webhook-encryption-key"))
	webhookService := webhooks.NewService(pool, webhookKey[:], true)
	webhookURL := "http://127.0.0.1:43210/hcai-events"
	webhookCredential, err := webhookService.Create(ctx, user.ID, webhooks.CreateInput{
		Name: "Data rights receiver", URL: webhookURL, EventTypes: []string{"developer.webhook.test"},
	}, "rights-webhook-create")
	if err != nil {
		t.Fatal(err)
	}
	webhookDelivery, err := webhookService.QueueTest(ctx, user.ID, webhookCredential.ID, "rights-webhook-test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='cancelled',updated_at=now() WHERE kind=$1`, webhooks.JobKind); err != nil {
		t.Fatal(err)
	}
	emailKey := sha256.Sum256([]byte("data-rights-email-action-key"))
	emailService := emailactions.NewService(pool, emailKey[:], "local_file", mediaRoot, "http://127.0.0.1:5173")
	emailAction, err := emailService.RequestVerification(ctx, user.ID, "rights-email-verification")
	if err != nil {
		t.Fatal(err)
	}
	emailPayload, _ := json.Marshal(map[string]any{"actionId": emailAction.ID})
	if err := emailService.HandleDeliveryJob(ctx, jobs.Job{Kind: emailactions.DeliveryJobKind, Payload: emailPayload, Attempts: 1, MaxAttempts: 5}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='cancelled',updated_at=now() WHERE kind IN ($1,$2) AND payload->>'actionId'=$3`, emailactions.DeliveryJobKind, emailactions.ExpiryJobKind, emailAction.ID.String()); err != nil {
		t.Fatal(err)
	}
	savedAuthorID, savedAssetID, savedWorkID, savedPostID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role,status) VALUES($1,$2,$3,'Saved Rights Author','creator','active')`, savedAuthorID, savedAuthorID.String()+"@test.local", "saved_"+savedAuthorID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code)
		VALUES($1,$2,'image','Saved rights source','/media/saved-rights.jpg','image/jpeg','clean','demo','personal')`, savedAssetID, savedAuthorID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO works(id,author_id,asset_id,title,summary,model_name,status,ai_disclosure,published_at)
		VALUES($1,$2,$3,'Saved rights work','Reference for export.','Local Test','published','Local Test disclosure.',now())`, savedWorkID, savedAuthorID, savedAssetID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO posts(id,author_id,work_id,body,status,published_at) VALUES($1,$2,$3,'Saved rights post','published',now())`, savedPostID, savedAuthorID, savedWorkID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO post_reactions(post_id,user_id,kind) VALUES($1,$2,'bookmark')`, savedPostID, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_follows(follower_id,following_id) VALUES($1,$2)`, user.ID, savedAuthorID); err != nil {
		t.Fatal(err)
	}
	exportRequest, err := service.Create(ctx, user.ID, token, datarights.CreateInput{RequestType: "data_export", IdentityConfirmation: handle}, "export-request")
	if err != nil || exportRequest.Status != "queued" {
		t.Fatalf("create export: %#v %v", exportRequest, err)
	}
	job = claimDataRightsJobKind(t, ctx, pool, "data-rights-test", datarights.ExportJobKind)
	if err := service.HandleExportJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := jobRepository.Complete(ctx, job, "data-rights-test"); err != nil {
		t.Fatal(err)
	}
	body, checksum, err := service.Download(ctx, user.ID, exportRequest.ID)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	if checksum != hex.EncodeToString(sum[:]) {
		t.Fatalf("export checksum mismatch: %s", checksum)
	}
	text := strings.ToLower(string(body))
	for _, forbidden := range []string{"token_hash", "network_hash", "password_hash", "ciphertext", "nonce", token, webhookCredential.SigningSecret} {
		if strings.Contains(text, strings.ToLower(forbidden)) {
			t.Fatalf("export leaked forbidden value %q", forbidden)
		}
	}
	var exported map[string]any
	if err := json.Unmarshal(body, &exported); err != nil || exported["subjectRef"] == "" {
		t.Fatalf("invalid export package: %#v %v", exported, err)
	}
	var exportPackage struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &exportPackage); err != nil {
		t.Fatal(err)
	}
	var supportCases []struct {
		ID       uuid.UUID `json:"id"`
		Subject  string    `json:"subject"`
		Messages []struct {
			Body string `json:"body"`
		} `json:"messages"`
		Events []struct {
			Reason string `json:"reason"`
		} `json:"events"`
	}
	if err := json.Unmarshal(exportPackage.Data["supportCases"], &supportCases); err != nil || len(supportCases) != 1 || supportCases[0].ID != supportCase.ID || len(supportCases[0].Messages) != 2 || len(supportCases[0].Events) != 2 {
		t.Fatalf("support data missing from export: %#v %v", supportCases, err)
	}
	if !strings.Contains(supportCases[0].Messages[0].Body, "must appear in my export") || supportCases[0].Events[0].Reason == "" {
		t.Fatalf("support export projection lost owner-visible content: %#v", supportCases[0])
	}
	var versionEvents []struct {
		AssetID uuid.UUID `json:"assetId"`
		Reason  string    `json:"reason"`
	}
	if err := json.Unmarshal(exportPackage.Data["assetVersionEvents"], &versionEvents); err != nil || len(versionEvents) != 1 || versionEvents[0].AssetID != versionAsset.ID || versionEvents[0].Reason != "Replace private working copy with the reviewed source." {
		t.Fatalf("Asset version evidence missing from export: %#v %v", versionEvents, err)
	}
	var webhookEndpoints []struct {
		ID  uuid.UUID `json:"id"`
		URL string    `json:"url"`
	}
	if err := json.Unmarshal(exportPackage.Data["developerWebhookEndpoints"], &webhookEndpoints); err != nil || len(webhookEndpoints) != 1 || webhookEndpoints[0].ID != webhookCredential.ID || webhookEndpoints[0].URL != webhookURL {
		t.Fatalf("Webhook endpoint missing from export: %#v %v", webhookEndpoints, err)
	}
	var webhookDeliveries []struct {
		ID      uuid.UUID `json:"id"`
		EventID uuid.UUID `json:"eventId"`
	}
	if err := json.Unmarshal(exportPackage.Data["developerWebhookDeliveries"], &webhookDeliveries); err != nil || len(webhookDeliveries) != 1 || webhookDeliveries[0].ID != webhookDelivery.ID || webhookDeliveries[0].EventID == uuid.Nil {
		t.Fatalf("Webhook delivery evidence missing from export: %#v %v", webhookDeliveries, err)
	}
	var webhookEvents []struct {
		ID        uuid.UUID       `json:"id"`
		EventType string          `json:"eventType"`
		Payload   json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(exportPackage.Data["developerWebhookEvents"], &webhookEvents); err != nil || len(webhookEvents) != 1 || webhookEvents[0].ID != webhookDeliveries[0].EventID || webhookEvents[0].EventType != "developer.webhook.test" || len(webhookEvents[0].Payload) == 0 {
		t.Fatalf("Webhook event evidence missing from export: %#v %v", webhookEvents, err)
	}
	var emailActions []struct {
		ID           uuid.UUID `json:"id"`
		Kind         string    `json:"kind"`
		AttemptCount int       `json:"attemptCount"`
	}
	if err := json.Unmarshal(exportPackage.Data["identityEmailActions"], &emailActions); err != nil || len(emailActions) != 1 || emailActions[0].ID != emailAction.ID || emailActions[0].Kind != emailactions.VerifyEmail || emailActions[0].AttemptCount != 1 {
		t.Fatalf("identity email metadata missing from export: %#v %v", emailActions, err)
	}
	var exportedNotifications []struct {
		ID             uuid.UUID  `json:"id"`
		DeliveryStatus string     `json:"deliveryStatus"`
		DeliveredAt    *time.Time `json:"deliveredAt"`
	}
	notificationExport := exportPackage.Data["notifications"]
	if err := json.Unmarshal(notificationExport, &exportedNotifications); err != nil || len(exportedNotifications) == 0 || exportedNotifications[0].ID == uuid.Nil || exportedNotifications[0].DeliveryStatus != "delivered" || exportedNotifications[0].DeliveredAt == nil {
		t.Fatalf("safe notification delivery evidence missing from export: %#v %v", exportedNotifications, err)
	}
	if strings.Contains(string(notificationExport), "sourceKey") || strings.Contains(string(notificationExport), "userId") {
		t.Fatalf("notification export exposed internal producer identifiers: %s", notificationExport)
	}
	if _, included := exportPackage.Data["jobs"]; included {
		t.Fatal("data export exposed durable job payload or lease state")
	}
	var savedWorks []struct {
		PostID  uuid.UUID `json:"postId"`
		WorkID  uuid.UUID `json:"workId"`
		SavedAt time.Time `json:"savedAt"`
	}
	if err := json.Unmarshal(exportPackage.Data["savedWorks"], &savedWorks); err != nil || len(savedWorks) != 1 || savedWorks[0].PostID != savedPostID || savedWorks[0].WorkID != savedWorkID || savedWorks[0].SavedAt.IsZero() {
		t.Fatalf("saved work missing from safe export: %#v %v", savedWorks, err)
	}
	var follows []struct {
		FollowingID uuid.UUID `json:"followingId"`
		CreatedAt   time.Time `json:"createdAt"`
	}
	if err := json.Unmarshal(exportPackage.Data["communityFollows"], &follows); err != nil || len(follows) != 1 || follows[0].FollowingID != savedAuthorID || follows[0].CreatedAt.IsZero() {
		t.Fatalf("follow relationship missing from safe export: %#v %v", follows, err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.data_rights_maintenance','on',true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE data_rights_export_artifacts SET expires_at=now()-interval '1 minute' WHERE request_id=$1`, exportRequest.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	expiryPayload, _ := json.Marshal(map[string]any{"requestId": exportRequest.ID})
	if err := service.HandleExportExpiryJob(ctx, jobs.Job{Kind: datarights.ExportExpiryJobKind, Payload: expiryPayload, Attempts: 1, MaxAttempts: 5}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Download(ctx, user.ID, exportRequest.ID); !errors.Is(err, datarights.ErrExpired) {
		t.Fatalf("expired export remained available: %v", err)
	}
	var bodyPurged bool
	if err := pool.QueryRow(ctx, `SELECT body IS NULL AND purged_at IS NOT NULL FROM data_rights_export_artifacts WHERE request_id=$1`, exportRequest.ID).Scan(&bodyPurged); err != nil || !bodyPurged {
		t.Fatalf("export body retention did not execute: purged=%v err=%v", bodyPurged, err)
	}

	deletionRequest, err := service.Create(ctx, user.ID, token, datarights.CreateInput{RequestType: "account_deletion", IdentityConfirmation: handle}, "deletion-request")
	if err != nil || deletionRequest.Status != "scheduled" || deletionRequest.CancelUntil == nil {
		t.Fatalf("create deletion: %#v %v", deletionRequest, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE data_rights_requests SET execute_after=now()-interval '1 minute',cancel_until=now()-interval '1 minute' WHERE id=$1`, deletionRequest.ID); err != nil {
		t.Fatal(err)
	}
	hold, err := service.CreateHold(ctx, adminID, datarights.HoldInput{UserID: user.ID, Reason: "A signed legal request requires temporary preservation.", AuthorityReference: "LEGAL-TEST-REFERENCE", Confirmed: true}, "hold-create")
	if err != nil || hold.Status != "active" || len(hold.AuthorityReferenceHash) != 64 {
		t.Fatalf("create legal hold: %#v %v", hold, err)
	}
	payload, _ := json.Marshal(map[string]any{"requestId": deletionRequest.ID})
	if err := service.HandleDeletionJob(ctx, jobs.Job{Kind: datarights.DeletionJobKind, Payload: payload, Attempts: 1, MaxAttempts: 5}); err != nil {
		t.Fatal(err)
	}
	if _, err := identityRepository.Authenticate(ctx, token); err != nil {
		t.Fatalf("held account lost access before release: %v", err)
	}
	hold, err = service.ReleaseHold(ctx, adminID, hold.ID, "Signed authority released the temporary preservation requirement.", "hold-release", true)
	if err != nil || hold.Status != "released" {
		t.Fatalf("release legal hold: %#v %v", hold, err)
	}
	runningNotificationJob := claimDataRightsJobKind(t, ctx, pool, "data-rights-notification-running", notifications.JobKind)
	queuedSourceKey := "data-deletion-queued:" + user.ID.String()
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := notifications.CreateTx(ctx, tx, notifications.CreateInput{
		UserID: user.ID, Kind: "account.data_rights", Title: "Deletion work queued", Body: "This delivery must be cancelled before account notification content is removed.",
		TargetPath: "/settings", ResourceType: "data_rights_request", ResourceID: &deletionRequest.ID, SourceKey: queuedSourceKey,
	}); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var queuedNotificationJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT j.id FROM jobs j JOIN notifications n ON j.payload->>'notificationId'=n.id::text
		WHERE j.kind=$1 AND n.user_id=$2 AND n.source_key=$3`, notifications.JobKind, user.ID, queuedSourceKey).Scan(&queuedNotificationJobID); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleDeletionJob(ctx, jobs.Job{Kind: datarights.DeletionJobKind, Payload: payload, Attempts: 1, MaxAttempts: 5}); err != nil {
		t.Fatal(err)
	}
	var runningJobStatus, queuedJobStatus, runningAttemptStatus, runningAttemptError string
	if err := pool.QueryRow(ctx, `SELECT status FROM jobs WHERE id=$1`, runningNotificationJob.ID).Scan(&runningJobStatus); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM jobs WHERE id=$1`, queuedNotificationJobID).Scan(&queuedJobStatus); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT status,error_code FROM job_attempts WHERE lease_token=$1`, runningNotificationJob.LeaseToken).Scan(&runningAttemptStatus, &runningAttemptError); err != nil {
		t.Fatal(err)
	}
	var remainingNotifications int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE user_id=$1`, user.ID).Scan(&remainingNotifications); err != nil {
		t.Fatal(err)
	}
	if runningJobStatus != "cancelled" || queuedJobStatus != "cancelled" || runningAttemptStatus != "cancelled" || runningAttemptError != "job_cancelled" || remainingNotifications != 0 {
		t.Fatalf("notification deletion cleanup mismatch: running=%s queued=%s attempt=%s/%s notifications=%d", runningJobStatus, queuedJobStatus, runningAttemptStatus, runningAttemptError, remainingNotifications)
	}
	if _, err := identityRepository.Authenticate(ctx, token); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("deleted account remained authenticated: %v", err)
	}
	var remainingSaved, remainingFollows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM post_reactions WHERE user_id=$1`, user.ID).Scan(&remainingSaved); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_follows WHERE follower_id=$1 OR following_id=$1`, user.ID).Scan(&remainingFollows); err != nil {
		t.Fatal(err)
	}
	if remainingSaved != 0 || remainingFollows != 0 {
		t.Fatalf("community relationships survived deletion: saved=%d follows=%d", remainingSaved, remainingFollows)
	}
	var status, email string
	if err := pool.QueryRow(ctx, `SELECT status,email FROM users WHERE id=$1`, user.ID).Scan(&status, &email); err != nil || status != "deleted" || !strings.HasSuffix(email, "@hcai.invalid") {
		t.Fatalf("identity not anonymized: status=%s email=%s err=%v", status, email, err)
	}
	var redactedAt *time.Time
	var caseSubject, caseDetails string
	if err := pool.QueryRow(ctx, `SELECT subject,details,personal_data_redacted_at FROM support_cases WHERE id=$1`, supportCase.ID).Scan(&caseSubject, &caseDetails, &redactedAt); err != nil || caseSubject != "Account support record" || caseDetails != "[Redacted following account deletion]" || redactedAt == nil {
		t.Fatalf("support case was not redacted: subject=%q details=%q redactedAt=%v err=%v", caseSubject, caseDetails, redactedAt, err)
	}
	var remainingMessages, remainingEvents int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM support_messages WHERE case_id=$1 AND body<>'[Redacted following account deletion]'`, supportCase.ID).Scan(&remainingMessages); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM support_events WHERE case_id=$1 AND (reason<>'Redacted following account deletion.' OR metadata<>'{}'::jsonb)`, supportCase.ID).Scan(&remainingEvents); err != nil {
		t.Fatal(err)
	}
	if remainingMessages != 0 || remainingEvents != 0 {
		t.Fatalf("support free text survived deletion: messages=%d events=%d", remainingMessages, remainingEvents)
	}
	var deletedVersionNote *string
	var deletedVersionReason string
	if err := pool.QueryRow(ctx, `SELECT version_note FROM assets WHERE id=$1`, versionAsset.ID).Scan(&deletedVersionNote); err != nil || deletedVersionNote != nil {
		t.Fatalf("Asset version note survived deletion: note=%v err=%v", deletedVersionNote, err)
	}
	if err := pool.QueryRow(ctx, `SELECT reason FROM asset_version_events WHERE asset_id=$1`, versionAsset.ID).Scan(&deletedVersionReason); err != nil || deletedVersionReason != "Redacted following account deletion." {
		t.Fatalf("Asset version event was not redacted: reason=%q err=%v", deletedVersionReason, err)
	}
	var deletedWebhookName, deletedWebhookURL, deletedWebhookStatus string
	var webhookRedactedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT name,url,status,personal_data_redacted_at FROM developer_webhook_endpoints WHERE id=$1`, webhookCredential.ID).Scan(&deletedWebhookName, &deletedWebhookURL, &deletedWebhookStatus, &webhookRedactedAt); err != nil || !strings.HasPrefix(deletedWebhookName, "Deleted endpoint ") || deletedWebhookURL != "https://deleted.invalid/webhook" || deletedWebhookStatus != "revoked" || webhookRedactedAt == nil {
		t.Fatalf("Webhook endpoint was not minimized: name=%q url=%q status=%q redactedAt=%v err=%v", deletedWebhookName, deletedWebhookURL, deletedWebhookStatus, webhookRedactedAt, err)
	}
	var remainingWebhookSecrets int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM developer_webhook_secret_revisions WHERE endpoint_id=$1`, webhookCredential.ID).Scan(&remainingWebhookSecrets); err != nil || remainingWebhookSecrets != 0 {
		t.Fatalf("Webhook signing ciphertext survived deletion: count=%d err=%v", remainingWebhookSecrets, err)
	}
	var minimizedEmailAction bool
	if err := pool.QueryRow(ctx, `SELECT status='cancelled' AND email_snapshot LIKE 'deleted+%@invalid.local' AND token_hash IS NULL AND token_nonce IS NULL AND token_ciphertext IS NULL FROM identity_email_actions WHERE id=$1`, emailAction.ID).Scan(&minimizedEmailAction); err != nil || !minimizedEmailAction {
		t.Fatalf("identity email action was not minimized: minimized=%v err=%v", minimizedEmailAction, err)
	}
	if _, err := os.Stat(filepath.Join(mediaRoot, "mailbox", user.ID.String())); !os.IsNotExist(err) {
		t.Fatalf("local identity mailbox survived deletion: %v", err)
	}
	for _, assetID := range []uuid.UUID{rootAsset.ID, versionAsset.ID} {
		if _, err := os.Stat(filepath.Join(mediaRoot, assetID.String()+".txt")); !os.IsNotExist(err) {
			t.Fatalf("owned media object %s survived deletion: %v", assetID, err)
		}
	}
	var minimizedDelivery bool
	if err := pool.QueryRow(ctx, `SELECT status='cancelled' AND secret_revision_id IS NULL FROM developer_webhook_deliveries WHERE id=$1`, webhookDelivery.ID).Scan(&minimizedDelivery); err != nil || !minimizedDelivery {
		t.Fatalf("Webhook delivery was not safely cancelled and detached: minimized=%v err=%v", minimizedDelivery, err)
	}
	var minimizedEvent bool
	if err := pool.QueryRow(ctx, `SELECT resource_type='redacted' AND resource_id IS NULL AND source_key='deleted:'||id::text AND NOT payload::text LIKE $2 FROM developer_webhook_events WHERE id=$1`, webhookDeliveries[0].EventID, "%"+webhookCredential.ID.String()+"%").Scan(&minimizedEvent); err != nil || !minimizedEvent {
		t.Fatalf("Webhook event payload was not minimized: minimized=%v err=%v", minimizedEvent, err)
	}
	var receiptID uuid.UUID
	var receiptBody []byte
	if err := pool.QueryRow(ctx, `SELECT id,receipt FROM data_rights_deletion_receipts WHERE request_id=$1`, deletionRequest.ID).Scan(&receiptID, &receiptBody); err != nil {
		t.Fatal(err)
	}
	if (!strings.Contains(string(receiptBody), `"domain": "support"`) && !strings.Contains(string(receiptBody), `"domain":"support"`)) || !strings.Contains(string(receiptBody), "developer_webhooks") || !strings.Contains(string(receiptBody), "developer_webhook_credentials") || !strings.Contains(string(receiptBody), "identity_email_actions") || !strings.Contains(string(receiptBody), "identity_email_tokens") || !strings.Contains(string(receiptBody), "redacted_minimal") || !strings.Contains(string(receiptBody), "erased") {
		t.Fatalf("deletion receipt omitted support disposition: %s", receiptBody)
	}
	if _, err := pool.Exec(ctx, `UPDATE data_rights_deletion_receipts SET completed_at=now() WHERE id=$1`, receiptID); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("deletion receipt was mutable: %v", err)
	}
	assetMaintenanceTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := assetMaintenanceTx.Exec(ctx, `SELECT set_config('app.asset_data_rights_maintenance','on',true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := assetMaintenanceTx.Exec(ctx, `UPDATE asset_version_events SET reason='arbitrary mutation' WHERE asset_id=$1`, versionAsset.ID); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("Asset maintenance boundary accepted arbitrary mutation: %v", err)
	}
	_ = assetMaintenanceTx.Rollback(ctx)
	maintenanceTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer maintenanceTx.Rollback(ctx)
	if _, err := maintenanceTx.Exec(ctx, `SELECT set_config('app.support_data_rights_maintenance','on',true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := maintenanceTx.Exec(ctx, `UPDATE support_messages SET body='arbitrary mutation' WHERE case_id=$1`, supportCase.ID); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("support maintenance boundary accepted arbitrary mutation: %v", err)
	}
}

func TestDeletionCanBeCancelledDuringGraceWindow(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := context.Background()
	repository := identity.NewRepository(pool)
	handle := "cancel_" + uuid.NewString()[:8]
	user, token, err := repository.Register(ctx, identity.RegisterInput{Email: handle + "@test.local", Password: "local-test-password", Handle: handle, DisplayName: "Cancel Owner", Locale: "en-US", Timezone: "UTC"}, identity.ClientInfo{Label: "test", RequestID: "register"})
	if err != nil {
		t.Fatal(err)
	}
	service := datarights.NewService(pool, t.TempDir())
	item, err := service.Create(ctx, user.ID, token, datarights.CreateInput{RequestType: "account_deletion", IdentityConfirmation: handle}, "create")
	if err != nil {
		t.Fatal(err)
	}
	item, err = service.Cancel(ctx, user.ID, item.ID, "cancel")
	if err != nil || item.Status != "cancelled" {
		t.Fatalf("cancel deletion: %#v %v", item, err)
	}
	if _, err := repository.Authenticate(ctx, token); err != nil {
		t.Fatalf("cancelled account lost access: %v", err)
	}
}

func TestAccountLinkRiskExportAndDeletionRemainPrivacyMinimized(t *testing.T) {
	pool, cleanup := dataRightsTestPool(t)
	defer cleanup()
	ctx := context.Background()
	repository := identity.NewRepository(pool)
	sharedNetwork := identity.HashNetwork("198.51.100.91")
	var owner identity.User
	var token string
	for index := 1; index <= 3; index++ {
		handle := "rights_link_" + uuid.NewString()[:8]
		user, sessionToken, err := repository.Register(ctx, identity.RegisterInput{
			Email: handle + "@test.local", Password: "local-test-password", Handle: handle,
			DisplayName: "Rights Link Account", Locale: "en-US", Timezone: "UTC",
		}, identity.ClientInfo{Label: "Data rights link test", NetworkHash: sharedNetwork, RequestID: "rights-link-register"})
		if err != nil {
			t.Fatal(err)
		}
		if index == 3 {
			owner, token = user, sessionToken
		}
	}

	service := datarights.NewService(pool, t.TempDir())
	exportRequest, err := service.Create(ctx, owner.ID, token, datarights.CreateInput{
		RequestType: "data_export", IdentityConfirmation: owner.Handle,
	}, "rights-link-export")
	if err != nil {
		t.Fatal(err)
	}
	job := claimDataRightsJobKind(t, ctx, pool, "rights-link-test", datarights.ExportJobKind)
	if err := service.HandleExportJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := jobs.NewRepository(pool).Complete(ctx, job, "rights-link-test"); err != nil {
		t.Fatal(err)
	}
	body, _, err := service.Download(ctx, owner.ID, exportRequest.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{sharedNetwork, "198.51.100.91", "network_hash", "networkHash", "ipAddress", "deviceFingerprint", "linkedAccounts"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("data export exposed forbidden account-link detail %q", forbidden)
		}
	}
	var exportPackage struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &exportPackage); err != nil {
		t.Fatal(err)
	}
	var signals []struct {
		ResourceType string         `json:"resourceType"`
		ResourceID   uuid.UUID      `json:"resourceId"`
		SignalType   string         `json:"signalType"`
		Score        int            `json:"score"`
		Evidence     map[string]any `json:"evidence"`
	}
	if err := json.Unmarshal(exportPackage.Data["riskSignals"], &signals); err != nil || len(signals) != 1 {
		t.Fatalf("owner-visible risk export mismatch: signals=%#v err=%v", signals, err)
	}
	if signals[0].ResourceType != "user" || signals[0].ResourceID != owner.ID || signals[0].SignalType != "account_link" || signals[0].Score != 65 ||
		signals[0].Evidence["linkedAccountCount"] != float64(3) || signals[0].Evidence["networkDataStored"] != false {
		t.Fatalf("safe account-link evidence missing from export: %#v", signals[0])
	}

	deletionRequest, err := service.Create(ctx, owner.ID, token, datarights.CreateInput{
		RequestType: "account_deletion", IdentityConfirmation: owner.Handle,
	}, "rights-link-delete")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE data_rights_requests SET execute_after=now()-interval '1 minute',cancel_until=now()-interval '1 minute' WHERE id=$1`, deletionRequest.ID); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"requestId": deletionRequest.ID})
	if err := service.HandleDeletionJob(ctx, jobs.Job{Kind: datarights.DeletionJobKind, Payload: payload, Attempts: 1, MaxAttempts: 5}); err != nil {
		t.Fatal(err)
	}
	var retainedNetworkHashes int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE user_id=$1 AND network_hash IS NOT NULL`, owner.ID).Scan(&retainedNetworkHashes); err != nil || retainedNetworkHashes != 0 {
		t.Fatalf("account deletion retained session network evidence: count=%d err=%v", retainedNetworkHashes, err)
	}
}

func claimDataRightsJobKind(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owner, kind string) jobs.Job {
	t.Helper()
	jobRepository := jobs.NewRepository(pool)
	notificationRepository := notifications.NewRepository(pool)
	for attempt := 0; attempt < 20; attempt++ {
		job, err := jobRepository.Claim(ctx, owner, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if job.Kind == kind {
			return job
		}
		if job.Kind != notifications.JobKind {
			t.Fatalf("unexpected job %q while waiting for %q", job.Kind, kind)
		}
		if err := notificationRepository.HandleDeliveryJob(ctx, job); err != nil {
			t.Fatal(err)
		}
		if err := jobRepository.Complete(ctx, job, owner); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatalf("job %q unavailable after draining notifications", kind)
	return jobs.Job{}
}

func dataRightsTestPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	ctx := context.Background()
	baseURL := os.Getenv("TEST_DATABASE_URL")
	if baseURL == "" {
		baseURL = "postgres://hcai:hcai@localhost:5432/hcai?sslmode=disable"
	}
	root, err := pgxpool.New(ctx, baseURL)
	if err != nil {
		t.Skipf("PostgreSQL integration database unavailable: %v", err)
	}
	if err := root.Ping(ctx); err != nil {
		root.Close()
		t.Skipf("PostgreSQL integration database unavailable: %v", err)
	}
	schema := "test_data_rights_" + uuid.NewString()[:8]
	if _, err := root.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
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
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = root.Exec(cleanupCtx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		root.Close()
	}
}
