package payments

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/creation"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

type purchasedReferenceFixture struct {
	buyer, source, purchased, order uuid.UUID
	root                            string
	content                         []byte
}

func newPurchasedReferenceFixture(t *testing.T, pool *pgxpool.Pool, mode string) purchasedReferenceFixture {
	t.Helper()
	return newPurchasedReferenceWithBytes(t, pool, mode, nil)
}

func newPurchasedReferenceWithBytes(t *testing.T, pool *pgxpool.Pool, mode string, body io.Reader) purchasedReferenceFixture {
	t.Helper()
	ctx := context.Background()
	buyer, _, source, product := newProductCheckoutFixture(t, pool)
	if body != nil {
		file, err := os.OpenFile(filepath.Join(paymentTestRoot(t, pool), source.String()+".jpg"), os.O_WRONLY|os.O_TRUNC, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, copyErr := io.Copy(file, body)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			t.Fatal(copyErr, closeErr)
		}
	}
	if mode == "chat" {
		if _, err := pool.Exec(ctx, `UPDATE assets SET kind='document',mime_type='text/plain' WHERE id=$1`, source); err != nil {
			t.Fatal(err)
		}
	}
	service := newPaymentTestService(t, pool, ServiceConfig{Enabled: true, APIVersion: testStripeAPIVersion, WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute}, NewRuntimeCatalog(&productCheckoutRuntime{}))
	checkout, _, err := service.BeginProductCheckout(ctx, buyer, product, "worker-contract-purchase", "test", "https://example.test/success", "https://example.test/cancel", true, productOfferVersion(t, pool, product))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	service.verifier.now = func() time.Time { return now }
	receipt := receivePaymentWorkflowEvent(t, service, productPaidEvent(checkout.PaymentID, product, now.Unix(), checkout.AmountCents), now)
	if err := service.HandlePaymentEventJob(ctx, jobs.Job{Kind: PaymentEventJobKind, Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, receipt.EventID.String()))}); err != nil {
		t.Fatal(err)
	}
	result := purchasedReferenceFixture{buyer: buyer, source: source, order: checkout.OrderID, root: paymentTestRoot(t, pool), content: []byte("The accepted purchased reference")}
	if err := pool.QueryRow(ctx, `SELECT asset_id FROM entitlements WHERE order_id=$1`, checkout.OrderID).Scan(&result.purchased); err != nil {
		t.Fatal(err)
	}

	return result
}

type referenceWorkerRuntime struct {
	calls   int
	request creation.ProviderRequest
	fail    bool
}

func (r *referenceWorkerRuntime) Provider() string { return "local_test" }
func (r *referenceWorkerRuntime) Supports(mode, model string) bool {
	return creation.NewLocalRuntime("").Supports(mode, model)
}
func (r *referenceWorkerRuntime) Generate(_ context.Context, request creation.ProviderRequest) (creation.ProviderOutput, error) {
	r.calls++
	r.request = request
	if r.fail {
		return creation.ProviderOutput{}, creation.NewProviderFailure("provider_rate_limited", 0)
	}
	if request.Mode == "chat" {
		text := "Contract reference processed"
		return creation.ProviderOutput{Kind: "document", MIMEType: "text/plain; charset=utf-8", Extension: ".txt", Text: &text, Content: []byte(text)}, nil
	}
	width, height := 960, 540
	return creation.ProviderOutput{Kind: "video", MIMEType: "video/mp4", Extension: ".mp4", Width: &width, Height: &height, Content: []byte("video fixture")}, nil
}

func generationWorkerJob(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) jobs.Job {
	return testutil.GenerationJob(t, pool, id)
}

func assertReferenceWorkerRejected(t *testing.T, pool *pgxpool.Pool, creator *creation.Service, runtime *referenceWorkerRuntime, fixture purchasedReferenceFixture, generation creation.Generation, expectedCalls int, expectedErrors ...error) {
	t.Helper()
	ctx := context.Background()
	job := generationWorkerJob(t, pool, generation.ID)
	expectedErr := error(creation.ErrReferenceUnavailable)
	if len(expectedErrors) > 0 {
		expectedErr = expectedErrors[0]
	}
	errorCode := expectedErr.Error()
	err := creator.HandleJob(ctx, job)
	if !errors.Is(err, expectedErr) || jobs.ShouldRetry(err) {
		t.Fatalf("reference rejection must be terminal: %v", err)
	}
	if runtime.calls != expectedCalls {
		t.Fatalf("unauthorized provider dispatch: calls=%d expected=%d", runtime.calls, expectedCalls)
	}
	got, err := creator.Get(ctx, fixture.buyer, generation.ID)
	if err != nil || got.Status != "failed" || got.ErrorCode == nil || *got.ErrorCode != errorCode || got.OutputAssetID != nil || got.ChargedPoints != 0 {
		t.Fatalf("incorrect failed generation: %+v, %v", got, err)
	}
	var balance, reserved int64
	if err := pool.QueryRow(ctx, `SELECT balance_points,reserved_points FROM point_accounts WHERE user_id=$1`, fixture.buyer).Scan(&balance, &reserved); err != nil {
		t.Fatal(err)
	}
	if balance != 10000 || reserved != 0 {
		t.Fatalf("unauthorized job charged or kept reserved points: balance=%d reserved=%d", balance, reserved)
	}
	if err := creator.HandleJob(ctx, job); err != nil || runtime.calls != expectedCalls {
		t.Fatalf("terminal replay reached provider: calls=%d err=%v", runtime.calls, err)
	}
	var evidence int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='generation.failed' AND resource_id=$1 AND metadata->>'errorCode'=$2`, generation.ID, errorCode).Scan(&evidence); err != nil || evidence != 1 {
		t.Fatalf("missing failure evidence: count=%d err=%v", evidence, err)
	}
}

func TestPurchasedReferenceWorkerRechecksQueuedAndAutomaticRetries(t *testing.T) {
	for _, scenario := range []string{"queued_image", "queued_video", "queued_chat", "mask", "legacy_source", "automatic_retry", "source_rejected"} {
		t.Run(scenario, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx := context.Background()
			mode := "image"
			if scenario == "queued_video" || scenario == "automatic_retry" {
				mode = "video"
			}
			if scenario == "queued_chat" {
				mode = "chat"
			}
			fixture := newPurchasedReferenceFixture(t, pool, mode)
			runtime := &referenceWorkerRuntime{fail: scenario == "automatic_retry"}
			creator := creation.NewServiceWithRuntimes(pool, fixture.root, creation.NewRuntimeCatalog(runtime))
			input := creation.SubmitInput{Mode: mode, Prompt: "Use my purchased reference", SourceAssetIDs: []uuid.UUID{fixture.purchased}}
			if scenario == "mask" {
				base := uuid.New()
				if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key) VALUES($1,$2,'image','Own base','/base','image/jpeg','clean','upload','creator-owned','local_file','base.jpg')`, base, fixture.buyer); err != nil {
					t.Fatal(err)
				}
				input.SourceAssetIDs, input.MaskAssetID = []uuid.UUID{base}, &fixture.purchased
			}
			generation, err := creator.SubmitCommand(ctx, fixture.buyer, input, "queued-purchased-reference", "test")
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "legacy_source" {
				if _, err := pool.Exec(ctx, `DELETE FROM generation_reference_assets WHERE generation_id=$1`, generation.ID); err != nil {
					t.Fatal(err)
				}
			}
			expectedCalls := 0
			if scenario == "automatic_retry" {
				if err := creator.HandleJob(ctx, generationWorkerJob(t, pool, generation.ID)); err == nil || !jobs.ShouldRetry(err) || runtime.calls != 1 {
					t.Fatalf("provider retry not exercised: %v calls=%d", err, runtime.calls)
				}
				expectedCalls = 1
			}
			if scenario == "source_rejected" {
				_, err = pool.Exec(ctx, `UPDATE assets SET scan_status='rejected' WHERE id=$1`, fixture.source)
			} else {
				_, err = pool.Exec(ctx, `UPDATE entitlements SET status='refunded',revoked_at=now() WHERE order_id=$1`, fixture.order)
			}
			if err != nil {
				t.Fatal(err)
			}
			assertReferenceWorkerRejected(t, pool, creator, runtime, fixture, generation, expectedCalls)
		})
	}
}

func TestPurchasedReferenceWorkerReadsAcceptedStorage(t *testing.T) {
	for _, mode := range []string{"video", "chat"} {
		t.Run(mode, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx := context.Background()
			fixture := newPurchasedReferenceFixture(t, pool, mode)
			if err := media.NewLocalStore(fixture.root).Put(ctx, "replacement.bin", []byte("Unexpected replacement"), "application/octet-stream"); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE assets SET storage_key='replacement.bin' WHERE id=$1`, fixture.source); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE licenses SET allows_derivatives=false WHERE code='hcai-commercial-standard-v1'`); err != nil {
				t.Fatal(err)
			}
			content, err := assets.NewService(pool, fixture.root).Content(ctx, fixture.buyer, fixture.purchased)
			if err != nil {
				t.Fatal(err)
			}
			object, err := content.Open(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			actual, readErr := io.ReadAll(object.Body)
			closeErr := object.Body.Close()
			if readErr != nil || closeErr != nil || !bytes.Equal(actual, fixture.content) {
				t.Fatalf("download ignored accepted storage: read=%v close=%v content=%q", readErr, closeErr, actual)
			}
			runtime := &referenceWorkerRuntime{}
			creator := creation.NewServiceWithRuntimes(pool, fixture.root, creation.NewRuntimeCatalog(runtime))
			generation, err := creator.SubmitCommand(ctx, fixture.buyer, creation.SubmitInput{Mode: mode, Prompt: "Use the original licensed content", SourceAssetIDs: []uuid.UUID{fixture.purchased}}, "accepted-storage-generation", "test")
			if err != nil {
				t.Fatal(err)
			}
			if err := creator.HandleJob(ctx, generationWorkerJob(t, pool, generation.ID)); err != nil {
				t.Fatal(err)
			}
			if runtime.calls != 1 || len(runtime.request.ReferenceAssets) != 1 || !bytes.Equal(runtime.request.ReferenceAssets[0].Content, fixture.content) {
				t.Fatalf("provider received current source instead of accepted storage: %+v", runtime.request.ReferenceAssets)
			}
			got, err := creator.Get(ctx, fixture.buyer, generation.ID)
			if err != nil || got.Status != "succeeded" {
				t.Fatalf("valid contract failed: %+v %v", got, err)
			}
		})
	}
}

type revokingReferenceStore struct {
	media.Store
	revoke func() error
}

func (s *revokingReferenceStore) Open(ctx context.Context, key string, requested *media.ByteRange) (media.Object, error) {
	if err := s.revoke(); err != nil {
		return media.Object{}, err
	}
	return s.Store.Open(ctx, key, requested)
}

func TestPurchasedReferenceWorkerRevocationDuringReadStopsDispatch(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	fixture := newPurchasedReferenceFixture(t, pool, "video")
	store := &revokingReferenceStore{Store: media.NewLocalStore(fixture.root), revoke: func() error {
		_, err := pool.Exec(ctx, `UPDATE entitlements SET status='refunded',revoked_at=now() WHERE order_id=$1`, fixture.order)
		return err
	}}
	runtime := &referenceWorkerRuntime{}
	creator := creation.NewServiceWithMedia(pool, media.NewCatalog(store), creation.NewRuntimeCatalog(runtime))
	generation, err := creator.SubmitCommand(ctx, fixture.buyer, creation.SubmitInput{Mode: "video", Prompt: "Use my reference before dispatch", SourceAssetIDs: []uuid.UUID{fixture.purchased}}, "revoked-during-read", "test")
	if err != nil {
		t.Fatal(err)
	}
	assertReferenceWorkerRejected(t, pool, creator, runtime, fixture, generation, 0)
}
