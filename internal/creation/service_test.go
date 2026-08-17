package creation_test

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/creation"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/hcai-chat/hcai-chat/internal/platform/database"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAllLocalCreationModesProduceTypedAssetsAndCaptureOnce(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	ownerID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role,status)
		VALUES($1,$2,$3,'Multimode Creator','creator','active')`, ownerID, ownerID.String()+"@test.local", "modes_"+ownerID.String()[:8]); err != nil {
		t.Fatal(err)
	}

	providerDir := t.TempDir()
	imageFixture := []byte("deterministic-jpeg-fixture")
	videoFixture := []byte("deterministic-mp4-fixture")
	imageSource := filepath.Join(providerDir, "source.jpg")
	if err := os.WriteFile(imageSource, imageFixture, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(providerDir, "local-video-test.mp4"), videoFixture, 0o600); err != nil {
		t.Fatal(err)
	}
	mediaRoot := t.TempDir()
	service := creation.NewService(pool, mediaRoot, imageSource, true)
	assetService := assets.NewService(pool, mediaRoot)
	jobRepository := jobs.NewRepository(pool)

	cases := []struct {
		mode        string
		cost        int
		kind        string
		mimeType    string
		extension   string
		validate    func(*testing.T, []byte)
		needsOutput bool
	}{
		{mode: "chat", cost: 2, kind: "document", mimeType: "text/plain; charset=utf-8", extension: ".txt", needsOutput: true, validate: func(t *testing.T, content []byte) {
			if !bytes.Contains(content, []byte("durable chat submission")) {
				t.Fatalf("chat result does not contain Local Test workflow evidence: %q", content)
			}
		}},
		{mode: "image", cost: 5, kind: "image", mimeType: "image/jpeg", extension: ".jpg", validate: func(t *testing.T, content []byte) {
			if !bytes.Equal(content, imageFixture) {
				t.Fatal("image output differs from deterministic fixture")
			}
		}},
		{mode: "video", cost: 20, kind: "video", mimeType: "video/mp4", extension: ".mp4", validate: func(t *testing.T, content []byte) {
			if !bytes.Equal(content, videoFixture) {
				t.Fatal("video output differs from deterministic fixture")
			}
		}},
		{mode: "music", cost: 8, kind: "audio", mimeType: "audio/wav", extension: ".wav", validate: func(t *testing.T, content []byte) {
			if len(content) < 44 || string(content[:4]) != "RIFF" || string(content[8:12]) != "WAVE" {
				t.Fatal("music output is not a valid RIFF/WAVE envelope")
			}
		}},
	}

	for _, testCase := range cases {
		t.Run(testCase.mode, func(t *testing.T) {
			prompt := "Verify the complete " + testCase.mode + " creation workflow"
			generation, err := service.SubmitCommand(ctx, ownerID, creation.SubmitInput{Mode: testCase.mode, Prompt: prompt}, "submit-"+testCase.mode+"-command", "request-"+testCase.mode)
			if err != nil {
				t.Fatal(err)
			}
			job := claimCreationJobKind(t, ctx, pool, "multimode-worker", creation.JobKind)
			if err := service.HandleJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			if err := jobRepository.Complete(ctx, job, "multimode-worker"); err != nil {
				t.Fatal(err)
			}

			completed, err := service.Get(ctx, ownerID, generation.ID)
			if err != nil {
				t.Fatal(err)
			}
			if completed.Status != "succeeded" || completed.OutputAssetID == nil || completed.ChargedCostCents != testCase.cost {
				t.Fatalf("unexpected completed %s generation: %#v", testCase.mode, completed)
			}
			if testCase.needsOutput && (completed.OutputText == nil || !strings.Contains(*completed.OutputText, prompt)) {
				t.Fatalf("chat output text is missing its request evidence: %#v", completed.OutputText)
			}
			var kind, mimeType string
			if err := pool.QueryRow(ctx, `SELECT kind,mime_type FROM assets WHERE id=$1`, completed.OutputAssetID).Scan(&kind, &mimeType); err != nil {
				t.Fatal(err)
			}
			if kind != testCase.kind || mimeType != testCase.mimeType {
				t.Fatalf("unexpected %s asset contract kind=%q mime=%q", testCase.mode, kind, mimeType)
			}
			path, servedMIME, err := assetService.Content(ctx, ownerID, *completed.OutputAssetID)
			if err != nil {
				t.Fatal(err)
			}
			if filepath.Ext(path) != testCase.extension || servedMIME != testCase.mimeType {
				t.Fatalf("unexpected %s content path=%q mime=%q", testCase.mode, path, servedMIME)
			}
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			testCase.validate(t, content)

			var chargeCount int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM billing_entries WHERE operation_id=$1 AND entry_type='generation_charge'`, generation.ID).Scan(&chargeCount); err != nil {
				t.Fatal(err)
			}
			if chargeCount != 1 {
				t.Fatalf("expected exactly one %s charge, got %d", testCase.mode, chargeCount)
			}
		})
	}

	var balance, reserved int64
	if err := pool.QueryRow(ctx, `SELECT balance_cents,reserved_cents FROM billing_accounts WHERE user_id=$1`, ownerID).Scan(&balance, &reserved); err != nil {
		t.Fatal(err)
	}
	if balance != 249965 || reserved != 0 {
		t.Fatalf("unexpected multimode billing result balance=%d reserved=%d", balance, reserved)
	}
}

func TestSubmitProcessAndReadGeneratedAsset(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	ownerID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role,status)
		VALUES($1,$2,$3,'Integration Creator','creator','active')`, ownerID, ownerID.String()+"@test.local", "creator_"+ownerID.String()[:8]); err != nil {
		t.Fatal(err)
	}

	mediaRoot := t.TempDir()
	source := filepath.Join(t.TempDir(), "source.jpg")
	expected := []byte("deterministic-jpeg-fixture")
	if err := os.WriteFile(source, expected, 0o600); err != nil {
		t.Fatal(err)
	}
	service := creation.NewService(pool, mediaRoot, source, true)
	primaryPrompt := "A durable integration image"
	generation, err := service.Submit(ctx, ownerID, creation.SubmitInput{Mode: "image", Prompt: primaryPrompt + "\n\nTone: precise\n\nApply a rights review."})
	if err != nil {
		t.Fatal(err)
	}
	if generation.Status != "queued" {
		t.Fatalf("expected queued generation, got %s", generation.Status)
	}

	jobRepository := jobs.NewRepository(pool)
	job := claimCreationJobKind(t, ctx, pool, "integration-worker", creation.JobKind)
	if err := service.HandleJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := jobRepository.Complete(ctx, job, "integration-worker"); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleJob(ctx, job); err != nil {
		t.Fatalf("completed generation replay failed: %v", err)
	}

	completed, err := service.Get(ctx, ownerID, generation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != "succeeded" || completed.Progress != 100 || completed.OutputAssetID == nil {
		t.Fatalf("unexpected completed generation: %#v", completed)
	}
	if completed.EstimatedCostCents != 5 || completed.ChargedCostCents != 5 {
		t.Fatalf("expected a five-cent Local Test capture, got estimate=%d charge=%d", completed.EstimatedCostCents, completed.ChargedCostCents)
	}
	var assetTitle string
	if err := pool.QueryRow(ctx, `SELECT title FROM assets WHERE id=$1`, completed.OutputAssetID).Scan(&assetTitle); err != nil {
		t.Fatal(err)
	}
	if assetTitle != primaryPrompt {
		t.Fatalf("expected the primary prompt as the Asset title, got %q", assetTitle)
	}
	var balance, reserved int64
	if err := pool.QueryRow(ctx, `SELECT balance_cents,reserved_cents FROM billing_accounts WHERE user_id=$1 AND currency='USD'`, ownerID).Scan(&balance, &reserved); err != nil {
		t.Fatal(err)
	}
	if balance != 249995 || reserved != 0 {
		t.Fatalf("unexpected post-generation billing state balance=%d reserved=%d", balance, reserved)
	}
	var chargeCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM billing_entries WHERE user_id=$1 AND operation_id=$2 AND entry_type='generation_charge'`, ownerID, generation.ID).Scan(&chargeCount); err != nil {
		t.Fatal(err)
	}
	if chargeCount != 1 {
		t.Fatalf("expected exactly one generation charge, got %d", chargeCount)
	}
	var notificationCount int
	var notificationTarget string
	if err := pool.QueryRow(ctx, `
		SELECT count(*),COALESCE(max(target_path),'') FROM notifications
		WHERE user_id=$1 AND kind='generation.completed' AND resource_id=$2`, ownerID, generation.ID).Scan(&notificationCount, &notificationTarget); err != nil {
		t.Fatal(err)
	}
	if notificationCount != 1 || notificationTarget != "/workspace/assets/"+completed.OutputAssetID.String() {
		t.Fatalf("generation notification is not idempotent or deep-linked: count=%d target=%q", notificationCount, notificationTarget)
	}
	assetService := assets.NewService(pool, mediaRoot)
	path, mimeType, err := assetService.Content(ctx, ownerID, *completed.OutputAssetID)
	if err != nil {
		t.Fatal(err)
	}
	if mimeType != "image/jpeg" {
		t.Fatalf("unexpected MIME type %q", mimeType)
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(actual) != string(expected) {
		t.Fatalf("generated asset content mismatch")
	}
}

func claimCreationJobKind(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owner, kind string) jobs.Job {
	t.Helper()
	jobRepository := jobs.NewRepository(pool)
	notificationRepository := notifications.NewRepository(pool)
	for attempt := 0; attempt < 20; attempt++ {
		job, err := jobRepository.Claim(ctx, owner, 30*time.Second)
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

func TestGenerationCancelRetryAndIdempotency(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	ownerID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role,status) VALUES($1,$2,$3,'Retry Creator','creator','active')`, ownerID, ownerID.String()+"@test.local", "retry_"+ownerID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	service := creation.NewService(pool, t.TempDir(), filepath.Join(t.TempDir(), "unused.jpg"), true)
	input := creation.SubmitInput{Mode: "image", Prompt: "A cancellable durable generation"}
	first, err := service.SubmitCommand(ctx, ownerID, input, "submit-command-001", "request-submit")
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := service.SubmitCommand(ctx, ownerID, input, "submit-command-001", "request-submit-replay")
	if err != nil || replayed.ID != first.ID {
		t.Fatalf("submit replay did not return the original generation: %#v %v", replayed, err)
	}
	var generationCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM generations WHERE owner_id=$1`, ownerID).Scan(&generationCount); err != nil {
		t.Fatal(err)
	}
	if generationCount != 1 {
		t.Fatalf("idempotent submit created %d generations", generationCount)
	}
	conflictingInput := input
	conflictingInput.Prompt = "A different payload under the same command key"
	if _, err := service.SubmitCommand(ctx, ownerID, conflictingInput, "submit-command-001", "request-submit-conflict"); !errors.Is(err, creation.ErrIdempotencyConflict) {
		t.Fatalf("expected changed idempotent payload to conflict, got %v", err)
	}

	cancelled, err := service.Cancel(ctx, ownerID, first.ID, "cancel-command-001", "request-cancel", "Changed the intended composition")
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != "cancelled" || cancelled.CancelledAt == nil {
		t.Fatalf("unexpected cancelled generation: %#v", cancelled)
	}
	var balance, reserved int64
	if err := pool.QueryRow(ctx, `SELECT balance_cents,reserved_cents FROM billing_accounts WHERE user_id=$1`, ownerID).Scan(&balance, &reserved); err != nil {
		t.Fatal(err)
	}
	if balance != 250000 || reserved != 0 {
		t.Fatalf("cancel did not release held credits: balance=%d reserved=%d", balance, reserved)
	}

	retried, err := service.Retry(ctx, ownerID, first.ID, "retry-command-001", "request-retry")
	if err != nil {
		t.Fatal(err)
	}
	if retried.ID == first.ID || retried.RetryOfGenerationID == nil || *retried.RetryOfGenerationID != first.ID {
		t.Fatalf("retry lineage is missing: %#v", retried)
	}
	retryReplay, err := service.Retry(ctx, ownerID, first.ID, "retry-command-001", "request-retry-replay")
	if err != nil || retryReplay.ID != retried.ID {
		t.Fatalf("retry replay did not return the created generation: %#v %v", retryReplay, err)
	}
	if err := pool.QueryRow(ctx, `SELECT balance_cents,reserved_cents FROM billing_accounts WHERE user_id=$1`, ownerID).Scan(&balance, &reserved); err != nil {
		t.Fatal(err)
	}
	if balance != 250000 || reserved != 5 {
		t.Fatalf("retry did not create one new hold: balance=%d reserved=%d", balance, reserved)
	}
}

func testPool(t *testing.T) (*pgxpool.Pool, func()) {
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
	schema := "test_creation_" + uuid.NewString()[:8]
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
