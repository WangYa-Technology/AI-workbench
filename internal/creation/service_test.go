package creation_test

import (
	"bytes"
	"context"
	"errors"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"io"
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
		mode          string
		kind          string
		mimeType      string
		extension     string
		validate      func(*testing.T, []byte)
		needsOutput   bool
		producesAsset bool
	}{
		{mode: "chat", needsOutput: true, producesAsset: false, validate: func(t *testing.T, content []byte) {
			if !bytes.Contains(content, []byte("durable chat submission")) {
				t.Fatalf("chat result does not contain Local Test workflow evidence: %q", content)
			}
		}},
		{mode: "image", kind: "image", mimeType: "image/jpeg", extension: ".jpg", producesAsset: true, validate: func(t *testing.T, content []byte) {
			if !bytes.Equal(content, imageFixture) {
				t.Fatal("image output differs from deterministic fixture")
			}
		}},
		{mode: "video", kind: "video", mimeType: "video/mp4", extension: ".mp4", producesAsset: true, validate: func(t *testing.T, content []byte) {
			if !bytes.Equal(content, videoFixture) {
				t.Fatal("video output differs from deterministic fixture")
			}
		}},
		{mode: "music", kind: "audio", mimeType: "audio/wav", extension: ".wav", producesAsset: true, validate: func(t *testing.T, content []byte) {
			if len(content) < 44 || string(content[:4]) != "RIFF" || string(content[8:12]) != "WAVE" {
				t.Fatal("music output is not a valid RIFF/WAVE envelope")
			}
		}},
	}

	var totalChargedPoints int64
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
			if completed.Status != "succeeded" || completed.EstimatedCostCents != 0 || completed.ChargedCostCents != 0 || completed.EstimatedPoints < 1 || completed.ChargedPoints < 1 || (testCase.producesAsset && completed.OutputAssetID == nil) || (!testCase.producesAsset && completed.OutputAssetID != nil) {
				t.Fatalf("unexpected completed %s generation: %#v", testCase.mode, completed)
			}
			totalChargedPoints += completed.ChargedPoints
			if completed.ProviderUsage == nil || completed.ProviderUsage.Status != "not_reported" || completed.ProviderUsage.TotalTokens != nil {
				t.Fatalf("Local Test %s generation must retain explicit not-reported usage evidence: %#v", testCase.mode, completed.ProviderUsage)
			}
			if testCase.needsOutput && (completed.OutputText == nil || !strings.Contains(*completed.OutputText, prompt)) {
				t.Fatalf("chat output text is missing its request evidence: %#v", completed.OutputText)
			}
			if testCase.producesAsset {
				var kind, mimeType, storageBackend, storageKey string
				if err := pool.QueryRow(ctx, `SELECT kind,mime_type,storage_backend,storage_key FROM assets WHERE id=$1`, completed.OutputAssetID).Scan(&kind, &mimeType, &storageBackend, &storageKey); err != nil {
					t.Fatal(err)
				}
				if kind != testCase.kind || mimeType != testCase.mimeType || storageBackend != "local_file" || filepath.Ext(storageKey) != testCase.extension {
					t.Fatalf("unexpected %s asset contract kind=%q mime=%q backend=%q key=%q", testCase.mode, kind, mimeType, storageBackend, storageKey)
				}
				contentHandle, err := assetService.Content(ctx, ownerID, *completed.OutputAssetID)
				if err != nil {
					t.Fatal(err)
				}
				if contentHandle.MimeType != testCase.mimeType {
					t.Fatalf("unexpected %s served mime=%q", testCase.mode, contentHandle.MimeType)
				}
				object, err := contentHandle.Open(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				content, readErr := io.ReadAll(object.Body)
				closeErr := object.Body.Close()
				if readErr != nil || closeErr != nil {
					t.Fatalf("read %s stored output: read=%v close=%v", testCase.mode, readErr, closeErr)
				}
				testCase.validate(t, content)
			} else if completed.OutputText == nil || !strings.Contains(*completed.OutputText, prompt) {
				t.Fatalf("chat response was not retained as generation text: %#v", completed.OutputText)
			}

			var chargeCount int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM point_entries WHERE operation_id=$1 AND entry_type='generation_charge'`, generation.ID).Scan(&chargeCount); err != nil {
				t.Fatal(err)
			}
			if chargeCount != 1 {
				t.Fatalf("expected exactly one %s charge, got %d", testCase.mode, chargeCount)
			}
		})
	}

	var pointBalance, pointReserved int64
	if err := pool.QueryRow(ctx, `SELECT balance_points,reserved_points FROM point_accounts WHERE user_id=$1`, ownerID).Scan(&pointBalance, &pointReserved); err != nil {
		t.Fatal(err)
	}
	if pointBalance != 10000-totalChargedPoints || pointReserved != 0 {
		t.Fatalf("unexpected multimode point result balance=%d reserved=%d charged=%d", pointBalance, pointReserved, totalChargedPoints)
	}
	var walletBalance, walletReserved int64
	if err := pool.QueryRow(ctx, `SELECT balance_cents,reserved_cents FROM billing_accounts WHERE user_id=$1`, ownerID).Scan(&walletBalance, &walletReserved); err != nil {
		t.Fatal(err)
	}
	if walletBalance != 250000 || walletReserved != 0 {
		t.Fatalf("model calls changed the subscription wallet: balance=%d reserved=%d", walletBalance, walletReserved)
	}
}

func TestChatGenerationContinuesOwnedConversationAndPreservesBranchOnRetry(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	ownerID, otherOwnerID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role,status) VALUES
		($1,$2,$3,'Conversation Creator','creator','active'),
		($4,$5,$6,'Other Creator','creator','active')`,
		ownerID, ownerID.String()+"@test.local", "conversation_"+ownerID.String()[:8],
		otherOwnerID, otherOwnerID.String()+"@test.local", "other_"+otherOwnerID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	service := creation.NewService(pool, t.TempDir(), filepath.Join(t.TempDir(), "unused.jpg"), true)
	jobRepository := jobs.NewRepository(pool)

	root, err := service.SubmitCommand(ctx, ownerID, creation.SubmitInput{Mode: "chat", Prompt: "Draft a concise launch position"}, "chat-root-command", "chat-root-request")
	if err != nil {
		t.Fatal(err)
	}
	job := claimCreationJobKind(t, ctx, pool, "conversation-worker", creation.JobKind)
	if err := service.HandleJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := jobRepository.Complete(ctx, job, "conversation-worker"); err != nil {
		t.Fatal(err)
	}

	followUp, err := service.SubmitCommand(ctx, ownerID, creation.SubmitInput{
		Mode: "chat", Prompt: "Now adapt it for product teams", ParentGenerationID: &root.ID,
	}, "chat-followup-command", "chat-followup-request")
	if err != nil {
		t.Fatal(err)
	}
	job = claimCreationJobKind(t, ctx, pool, "conversation-worker", creation.JobKind)
	if err := service.HandleJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := jobRepository.Complete(ctx, job, "conversation-worker"); err != nil {
		t.Fatal(err)
	}
	completed, err := service.Get(ctx, ownerID, followUp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.ParentGenerationID == nil || *completed.ParentGenerationID != root.ID || completed.OutputText == nil || !strings.Contains(*completed.OutputText, "1 prior turn(s)") {
		t.Fatalf("follow-up did not retain its conversation context: %#v", completed)
	}

	if _, err := service.SubmitCommand(ctx, otherOwnerID, creation.SubmitInput{Mode: "chat", Prompt: "Cross-account follow-up", ParentGenerationID: &root.ID}, "chat-cross-owner", "chat-cross-owner-request"); !errors.Is(err, creation.ErrInvalid) {
		t.Fatalf("expected foreign conversation parent to fail closed, got %v", err)
	}
	if _, err := service.SubmitCommand(ctx, ownerID, creation.SubmitInput{Mode: "video", Prompt: "Wrongly linked video", ParentGenerationID: &root.ID}, "video-chat-parent", "video-chat-parent-request"); !errors.Is(err, creation.ErrInvalid) {
		t.Fatalf("expected non-chat conversation parent to be rejected, got %v", err)
	}

	branch, err := service.SubmitCommand(ctx, ownerID, creation.SubmitInput{Mode: "chat", Prompt: "Try an alternate ending", ParentGenerationID: &root.ID}, "chat-branch-command", "chat-branch-request")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Cancel(ctx, ownerID, branch.ID, "cancel-chat-branch", "cancel-chat-branch-request", "Testing retry branch preservation"); err != nil {
		t.Fatal(err)
	}
	retried, err := service.Retry(ctx, ownerID, branch.ID, "retry-chat-branch", "retry-chat-branch-request")
	if err != nil {
		t.Fatal(err)
	}
	if retried.ParentGenerationID == nil || *retried.ParentGenerationID != root.ID {
		t.Fatalf("retry lost the original conversation branch: %#v", retried.ParentGenerationID)
	}
	favorited, err := service.SetFavorite(ctx, ownerID, root.ID, true, "favorite-chat-root")
	if err != nil || !favorited.IsFavorite {
		t.Fatalf("favorite state was not persisted: %#v err=%v", favorited, err)
	}
	unfavorited, err := service.SetFavorite(ctx, ownerID, root.ID, false, "unfavorite-chat-root")
	if err != nil || unfavorited.IsFavorite {
		t.Fatalf("favorite state was not removed: %#v err=%v", unfavorited, err)
	}
}

func TestGenerationPersistsOrderedCompatibleReferenceAssetsAcrossRetry(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	ownerID := uuid.New()
	firstAssetID, secondAssetID, documentAssetID := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role,status)
		VALUES($1,$2,$3,'Reference Creator','creator','active')`,
		ownerID, ownerID.String()+"@test.local", "references_"+ownerID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key) VALUES
		($2::uuid,$1,'image','First reference','/media/first.jpg','image/jpeg','clean','upload','creator-owned','local_file',($2::uuid)::text||'.jpg'),
		($3::uuid,$1,'image','Second reference','/media/second.jpg','image/jpeg','clean','upload','creator-owned','local_file',($3::uuid)::text||'.jpg'),
		($4::uuid,$1,'document','Wrong mode reference','/media/brief.txt','text/plain','clean','upload','creator-owned','local_file',($4::uuid)::text||'.txt')`,
		ownerID, firstAssetID, secondAssetID, documentAssetID); err != nil {
		t.Fatal(err)
	}
	service := creation.NewService(pool, t.TempDir(), filepath.Join(t.TempDir(), "unused.jpg"), true)
	created, err := service.SubmitCommand(ctx, ownerID, creation.SubmitInput{
		Mode: "image", Prompt: "Combine both visual references", SourceAssetIDs: []uuid.UUID{secondAssetID, firstAssetID, secondAssetID}, MaskAssetID: &firstAssetID,
	}, "multi-reference-submit", "multi-reference-request")
	if err != nil {
		t.Fatal(err)
	}
	if created.SourceAssetID == nil || *created.SourceAssetID != secondAssetID || created.MaskAssetID == nil || *created.MaskAssetID != firstAssetID || len(created.SourceAssetIDs) != 2 || created.SourceAssetIDs[0] != secondAssetID || created.SourceAssetIDs[1] != firstAssetID {
		t.Fatalf("reference order was not preserved: %#v", created)
	}
	loaded, err := service.Get(ctx, ownerID, created.ID)
	if err != nil || loaded.MaskAssetID == nil || *loaded.MaskAssetID != firstAssetID || len(loaded.SourceAssetIDs) != 2 || loaded.SourceAssetIDs[0] != secondAssetID || loaded.SourceAssetIDs[1] != firstAssetID {
		t.Fatalf("persisted references were not returned: %#v err=%v", loaded.SourceAssetIDs, err)
	}
	if _, err := service.Cancel(ctx, ownerID, created.ID, "cancel-multi-reference", "cancel-multi-reference-request", "Verify reference preservation on retry"); err != nil {
		t.Fatal(err)
	}
	retried, err := service.Retry(ctx, ownerID, created.ID, "retry-multi-reference", "retry-multi-reference-request")
	if err != nil || retried.MaskAssetID == nil || *retried.MaskAssetID != firstAssetID || len(retried.SourceAssetIDs) != 2 || retried.SourceAssetIDs[0] != secondAssetID || retried.SourceAssetIDs[1] != firstAssetID {
		t.Fatalf("retry lost ordered references: %#v err=%v", retried.SourceAssetIDs, err)
	}
	batch, err := service.Batch(ctx, ownerID, creation.GenerationBatchInput{
		GenerationIDs: []uuid.UUID{created.ID, retried.ID}, Action: "favorite",
	}, "unused-for-favorite", "batch-favorite-request")
	if err != nil || len(batch.Items) != 2 || len(batch.Failures) != 0 || !batch.Items[0].IsFavorite || !batch.Items[1].IsFavorite {
		t.Fatalf("batch favorite did not update every item: %#v err=%v", batch, err)
	}
	missingGenerationID := uuid.New()
	batch, err = service.Batch(ctx, ownerID, creation.GenerationBatchInput{
		GenerationIDs: []uuid.UUID{missingGenerationID, retried.ID}, Action: "cancel", Reason: "Verify partial batch cancellation",
	}, "batch-cancel-command", "batch-cancel-request")
	if err != nil || len(batch.Items) != 1 || batch.Items[0].ID != retried.ID || len(batch.Failures) != 1 || batch.Failures[0].GenerationID != missingGenerationID || batch.Failures[0].Code != "not_found" {
		t.Fatalf("batch cancellation did not preserve per-item outcomes: %#v err=%v", batch, err)
	}
	if _, err := service.SubmitCommand(ctx, ownerID, creation.SubmitInput{
		Mode: "image", Prompt: "Use an incompatible document", SourceAssetIDs: []uuid.UUID{documentAssetID},
	}, "wrong-reference-kind", "wrong-reference-kind-request"); !errors.Is(err, creation.ErrInvalid) {
		t.Fatalf("expected incompatible reference kind to fail, got %v", err)
	}
	if _, err := service.SubmitCommand(ctx, ownerID, creation.SubmitInput{
		Mode: "image", Prompt: "Use a mask without a base image", MaskAssetID: &firstAssetID,
	}, "mask-without-base", "mask-without-base-request"); !errors.Is(err, creation.ErrInvalid) {
		t.Fatalf("expected mask without base reference to fail, got %v", err)
	}
	if _, err := service.SubmitCommand(ctx, ownerID, creation.SubmitInput{
		Mode: "video", Prompt: "Use a mask in video mode", SourceAssetIDs: []uuid.UUID{firstAssetID}, MaskAssetID: &firstAssetID,
	}, "mask-wrong-mode", "mask-wrong-mode-request"); !errors.Is(err, creation.ErrInvalid) {
		t.Fatalf("expected non-image mask to fail, got %v", err)
	}
}

func TestExternalRuntimeRouteProducesAssetAndInheritsRetryPolicy(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	ownerID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,handle,display_name,role,status)
		VALUES($1,$2,$3,'Contract Creator','creator','active')`, ownerID, ownerID.String()+"@test.local", "contract_"+ownerID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO provider_profiles(id,mode,provider,model_name,display_name,description,estimated_cost_cents,currency,local_test,admin_enabled)
		VALUES('contract-chat','chat','contract','contract-chat-v1','Contract Chat','Test-only external runtime contract.',7,'USD',false,true)`); err != nil {
		t.Fatal(err)
	}
	var activeID uuid.UUID
	var version int
	if err := pool.QueryRow(ctx, `SELECT active_revision_id,version FROM model_route_state WHERE mode='chat'`).Scan(&activeID, &version); err != nil {
		t.Fatal(err)
	}
	revisionID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO model_route_revisions(id,mode,version,parent_revision_id,provider_profile_id,name,timeout_seconds,max_attempts,reason)
		VALUES($1,'chat',$2,$3,'contract-chat','Contract route',30,2,'Verify the external Provider execution boundary.')`, revisionID, version+1, activeID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE model_route_state SET active_revision_id=$1,version=$2 WHERE mode='chat'`, revisionID, version+1); err != nil {
		t.Fatal(err)
	}

	if _, err := creation.NewServiceWithRuntimes(pool, t.TempDir(), creation.NewRuntimeCatalog()).SubmitCommand(
		ctx, ownerID, creation.SubmitInput{Mode: "chat", Prompt: "Provider boundary prompt"}, "missing-runtime-submit", "missing-runtime-request",
	); !errors.Is(err, creation.ErrProviderOff) {
		t.Fatalf("expected submission without a configured runtime to fail closed, got %v", err)
	}
	var reservationCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM point_reservations WHERE user_id=$1`, ownerID).Scan(&reservationCount); err != nil || reservationCount != 0 {
		t.Fatalf("unavailable runtime reserved points: count=%d err=%v", reservationCount, err)
	}

	response := "External Provider contract output"
	usage := &creation.ProviderUsage{InputTokens: 23, CachedInputTokens: 5, OutputTokens: 17, ReasoningTokens: 4, TotalTokens: 40}
	runtime := &contractRuntime{
		provider: "contract", mode: "chat", model: "contract-chat-v1",
		output: creation.ProviderOutput{
			Kind: "document", MIMEType: "text/plain; charset=utf-8", Extension: ".txt",
			Text: &response, Content: []byte(response), Usage: usage,
		},
	}
	mediaRoot := t.TempDir()
	service := creation.NewServiceWithRuntimes(pool, mediaRoot, creation.NewRuntimeCatalog(runtime))
	generation, err := service.SubmitCommand(ctx, ownerID, creation.SubmitInput{Mode: "chat", Prompt: "Provider boundary prompt"}, "external-runtime-submit", "external-runtime-request")
	if err != nil {
		t.Fatal(err)
	}
	if generation.Provider != "contract" || generation.ModelName != "contract-chat-v1" || generation.EstimatedCostCents != 0 || generation.EstimatedPoints < 1 {
		t.Fatalf("generation did not snapshot its active Provider route: %#v", generation)
	}
	var maxAttempts int
	if err := pool.QueryRow(ctx, `SELECT max_attempts FROM jobs WHERE kind=$1 AND payload->>'generationId'=$2`, creation.JobKind, generation.ID.String()).Scan(&maxAttempts); err != nil {
		t.Fatal(err)
	}
	if maxAttempts != 2 {
		t.Fatalf("generation job did not inherit route max attempts: %d", maxAttempts)
	}
	jobRepository := jobs.NewRepository(pool)
	job := claimCreationJobKind(t, ctx, pool, "contract-worker", creation.JobKind)
	if err := service.HandleJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := jobRepository.Complete(ctx, job, "contract-worker"); err != nil {
		t.Fatal(err)
	}
	completed, err := service.Get(ctx, ownerID, generation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != "succeeded" || completed.OutputAssetID != nil || completed.OutputText == nil || *completed.OutputText != response || completed.ChargedCostCents != 0 || completed.ChargedPoints < 1 {
		t.Fatalf("unexpected external Provider completion: %#v", completed)
	}
	if completed.ProviderUsage == nil || completed.ProviderUsage.Status != "reported" || completed.ProviderUsage.InputTokens == nil || *completed.ProviderUsage.InputTokens != 23 ||
		completed.ProviderUsage.CachedInputTokens == nil || *completed.ProviderUsage.CachedInputTokens != 5 || completed.ProviderUsage.OutputTokens == nil || *completed.ProviderUsage.OutputTokens != 17 ||
		completed.ProviderUsage.ReasoningTokens == nil || *completed.ProviderUsage.ReasoningTokens != 4 || completed.ProviderUsage.TotalTokens == nil || *completed.ProviderUsage.TotalTokens != 40 {
		t.Fatalf("unexpected owner Provider usage evidence: %#v", completed.ProviderUsage)
	}
	if _, err := pool.Exec(ctx, `UPDATE generation_provider_usage SET total_tokens=41 WHERE generation_id=$1`, generation.ID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("Provider usage evidence accepted mutation: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM generation_provider_usage WHERE generation_id=$1`, generation.ID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("Provider usage evidence accepted deletion: %v", err)
	}
	if len(runtime.requests) != 1 || runtime.requests[0].GenerationID != generation.ID || runtime.requests[0].Provider != "contract" {
		t.Fatalf("external runtime did not receive immutable generation evidence: %#v", runtime.requests)
	}
	if _, err := os.Stat(filepath.Join(mediaRoot, generation.ID.String()+".txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("external chat Provider output should not be persisted as an asset: err=%v", err)
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
	if completed.EstimatedCostCents != 0 || completed.ChargedCostCents != 0 || completed.EstimatedPoints < 1 || completed.ChargedPoints < 1 {
		t.Fatalf("expected point-only Local Test capture, got cents estimate=%d charge=%d points estimate=%d charge=%d", completed.EstimatedCostCents, completed.ChargedCostCents, completed.EstimatedPoints, completed.ChargedPoints)
	}
	var assetTitle string
	if err := pool.QueryRow(ctx, `SELECT title FROM assets WHERE id=$1`, completed.OutputAssetID).Scan(&assetTitle); err != nil {
		t.Fatal(err)
	}
	if assetTitle != primaryPrompt {
		t.Fatalf("expected the primary prompt as the Asset title, got %q", assetTitle)
	}
	var balance, reserved int64
	if err := pool.QueryRow(ctx, `SELECT balance_points,reserved_points FROM point_accounts WHERE user_id=$1`, ownerID).Scan(&balance, &reserved); err != nil {
		t.Fatal(err)
	}
	if balance != 10000-completed.ChargedPoints || reserved != 0 {
		t.Fatalf("unexpected post-generation point state balance=%d reserved=%d", balance, reserved)
	}
	var chargeCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM point_entries WHERE user_id=$1 AND operation_id=$2 AND entry_type='generation_charge'`, ownerID, generation.ID).Scan(&chargeCount); err != nil {
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
	contentHandle, err := assetService.Content(ctx, ownerID, *completed.OutputAssetID)
	if err != nil {
		t.Fatal(err)
	}
	if contentHandle.MimeType != "image/jpeg" {
		t.Fatalf("unexpected MIME type %q", contentHandle.MimeType)
	}
	object, err := contentHandle.Open(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	actual, readErr := io.ReadAll(object.Body)
	closeErr := object.Body.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read generated asset: read=%v close=%v", readErr, closeErr)
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
	if err := pool.QueryRow(ctx, `SELECT balance_points,reserved_points FROM point_accounts WHERE user_id=$1`, ownerID).Scan(&balance, &reserved); err != nil {
		t.Fatal(err)
	}
	if balance != 10000 || reserved != 0 {
		t.Fatalf("cancel did not release held points: balance=%d reserved=%d", balance, reserved)
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
	if err := pool.QueryRow(ctx, `SELECT balance_points,reserved_points FROM point_accounts WHERE user_id=$1`, ownerID).Scan(&balance, &reserved); err != nil {
		t.Fatal(err)
	}
	if balance != 10000 || reserved != retried.EstimatedPoints {
		t.Fatalf("retry did not create one new point hold: balance=%d reserved=%d estimate=%d", balance, reserved, retried.EstimatedPoints)
	}
}

func TestRetryRevalidatesTheCurrentProviderCapabilityBeforeBilling(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	ownerID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name,role,status) VALUES($1,$2,$3,'Retry Capability Creator','creator','active')`, ownerID, ownerID.String()+"@test.local", "retry_cap_"+ownerID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	localService := creation.NewService(pool, t.TempDir(), filepath.Join(t.TempDir(), "unused.jpg"), true)
	created, err := localService.SubmitCommand(ctx, ownerID, creation.SubmitInput{
		Mode: "video", Prompt: "A thirty second product story",
		Parameters: creation.GenerationParameters{DurationSeconds: 30},
	}, "retry-cap-submit", "retry-cap-submit-request")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := localService.Cancel(ctx, ownerID, created.ID, "retry-cap-cancel", "retry-cap-cancel-request", "Switch the active video route before retrying."); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO provider_profiles(id,mode,provider,model_name,display_name,description,estimated_cost_cents,currency,local_test,admin_enabled)
		VALUES('retry-byteplus','video','byteplus_video','retry-video','Retry Video','Exact capability retry fixture.',9,'USD',false,true)`); err != nil {
		t.Fatal(err)
	}
	var activeID uuid.UUID
	var version int
	if err := pool.QueryRow(ctx, `SELECT active_revision_id,version FROM model_route_state WHERE mode='video'`).Scan(&activeID, &version); err != nil {
		t.Fatal(err)
	}
	revisionID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO model_route_revisions(id,mode,version,parent_revision_id,provider_profile_id,name,timeout_seconds,max_attempts,reason)
		VALUES($1,'video',$2,$3,'retry-byteplus','Retry Video route',30,2,'Verify retries honor the active Provider capability.')`, revisionID, version+1, activeID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE model_route_state SET active_revision_id=$1,version=$2 WHERE mode='video'`, revisionID, version+1); err != nil {
		t.Fatal(err)
	}
	runtime := creation.NewVideoRuntime(creation.VideoRuntimeConfig{APIKey: "fixture-only", BaseURL: "http://127.0.0.1:9/v1", Model: "retry-video"})
	externalService := creation.NewServiceWithRuntimes(pool, t.TempDir(), creation.NewRuntimeCatalog(runtime))
	if _, err := externalService.Retry(ctx, ownerID, created.ID, "retry-cap-command", "retry-cap-request"); !errors.Is(err, creation.ErrInvalid) {
		t.Fatalf("retry accepted a duration unsupported by the active route: %v", err)
	}
	var reserved int64
	if err := pool.QueryRow(ctx, `SELECT reserved_points FROM point_accounts WHERE user_id=$1`, ownerID).Scan(&reserved); err != nil {
		t.Fatal(err)
	}
	if reserved != 0 {
		t.Fatalf("invalid retry reserved points before failing: %d", reserved)
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
		testutil.DatabaseUnavailable(t, err)
	}
	if err := admin.Ping(ctx); err != nil {
		admin.Close()
		testutil.DatabaseUnavailable(t, err)
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
