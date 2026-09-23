package creation_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/creation"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
)

type ownerReferenceRuntime struct {
	calls int
	retry bool
}

func (r *ownerReferenceRuntime) Provider() string { return "local_test" }
func (r *ownerReferenceRuntime) Supports(mode, model string) bool {
	return creation.NewLocalRuntime("").Supports(mode, model)
}
func (r *ownerReferenceRuntime) Generate(_ context.Context, _ creation.ProviderRequest) (creation.ProviderOutput, error) {
	r.calls++
	if r.retry {
		return creation.ProviderOutput{}, creation.NewProviderFailure("provider_rate_limited", 0)
	}
	return creation.ProviderOutput{}, errors.New("unauthorized reference reached provider")
}

type ownerChangingStore struct {
	media.Store
	change func() error
}

func (s *ownerChangingStore) Open(ctx context.Context, key string, requested *media.ByteRange) (media.Object, error) {
	object, err := s.Store.Open(ctx, key, requested)
	if err != nil {
		return object, err
	}
	if err = s.change(); err != nil {
		object.Body.Close()
		return media.Object{}, err
	}
	return object, nil
}

func TestOwnedReferencesRequireActiveOwnerAtSubmissionRetryAndDispatch(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	for _, status := range []string{"suspended", "deleted"} {
		for _, scenario := range []string{"image", "video", "chat", "mask", "video_during_read", "chat_during_read", "video_auto_retry", "chat_auto_retry"} {
			t.Run(status+"/"+scenario, func(t *testing.T) {
				owner, source := uuid.New(), uuid.New()
				if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name) VALUES($1,$2,$3,'Reference creator')`, owner, owner.String()+"@test.local", "refs_"+owner.String()[:8]); err != nil {
					t.Fatal(err)
				}
				mode, kind, mime := "image", "image", "image/jpeg"
				switch scenario {
				case "video", "video_during_read", "video_auto_retry":
					mode = "video"
				case "chat", "chat_during_read", "chat_auto_retry":
					mode, kind, mime = "chat", "document", "text/plain"
				}
				if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key)
     VALUES($1,$2,$3,'Owned reference',$4,$5,'clean','upload','creator-owned','local_file',$1::uuid::text||'.bin')`, source, owner, kind, "/api/v1/assets/"+source.String()+"/content", mime); err != nil {
					t.Fatal(err)
				}
				local := media.NewLocalStore(t.TempDir())
				if err := local.Put(ctx, source.String()+".bin", []byte("Private owned reference"), mime); err != nil {
					t.Fatal(err)
				}
				revoke := func() error {
					_, err := pool.Exec(ctx, `UPDATE users SET status=$2 WHERE id=$1`, owner, status)
					return err
				}
				store := &ownerChangingStore{Store: local, change: func() error { return nil }}
				runtime := &ownerReferenceRuntime{}
				svc := creation.NewServiceWithMedia(pool, media.NewCatalog(store), creation.NewRuntimeCatalog(runtime))
				input := creation.SubmitInput{Mode: mode, Prompt: "Use my private reference", SourceAssetIDs: []uuid.UUID{source}}
				if scenario == "mask" {
					input.MaskAssetID = &source
				}
				g, err := svc.SubmitCommand(ctx, owner, input, "owned-reference-before-change", "test")
				if err != nil {
					t.Fatal(err)
				}
				job := lifecycleJob(t, pool, g.ID)
				expectedCalls := 0
				switch scenario {
				case "video_during_read", "chat_during_read":
					store.change = revoke
				case "video_auto_retry", "chat_auto_retry":
					runtime.retry = true
					if err = svc.HandleJob(ctx, job); err == nil || !jobs.ShouldRetry(err) || runtime.calls != 1 {
						t.Fatal("expected first retryable provider attempt", err)
					}
					expectedCalls = 1
					if err := jobs.NewRepository(pool).Fail(ctx, job, "generation-test", creation.NewProviderFailure("provider_rate_limited", 0)); err != nil {
						t.Fatal(err)
					}
					job = lifecycleJob(t, pool, g.ID)
					if err = revoke(); err != nil {
						t.Fatal(err)
					}
				default:
					if err = revoke(); err != nil {
						t.Fatal(err)
					}
				}
				if err = svc.HandleJob(ctx, job); !errors.Is(err, creation.ErrAccountUnavailable) || jobs.ShouldRetry(err) {
					t.Fatalf("did not terminate revoked reference: %v", err)
				}
				if runtime.calls != expectedCalls {
					t.Fatalf("invalid owner dispatched: calls=%d want=%d", runtime.calls, expectedCalls)
				}
				after, err := svc.Get(ctx, owner, g.ID)
				if err != nil || after.Status != "failed" || after.ErrorCode == nil || *after.ErrorCode != "generation_account_unavailable" || after.ChargedPoints != 0 {
					t.Fatal("incorrect termination", after, err)
				}
				var balance, reserved int64
				if err = pool.QueryRow(ctx, `SELECT balance_points,reserved_points FROM point_accounts WHERE user_id=$1`, owner).Scan(&balance, &reserved); err != nil || balance != 10000 || reserved != 0 {
					t.Fatal("credits not released", balance, reserved, err)
				}
				if _, err = svc.SubmitCommand(ctx, owner, input, "owned-reference-after-change", "test"); !errors.Is(err, creation.ErrForbidden) {
					t.Fatal("inactive reference submission", err)
				}
				if _, err = svc.Retry(ctx, owner, g.ID, "owned-reference-manual-retry", "test"); !errors.Is(err, creation.ErrForbidden) {
					t.Fatal("inactive reference manual retry", err)
				}
				if err = svc.HandleJob(ctx, job); err != nil || runtime.calls != expectedCalls {
					t.Fatal("terminal replay dispatched", err)
				}
				var failures int
				if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE resource_id=$1 AND action='generation.failed'`, g.ID).Scan(&failures); err != nil || failures != 1 {
					t.Fatal("failure evidence repeated", failures, err)
				}
			})
		}
	}
}
