package assets_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/jackc/pgx/v5/pgxpool"
)

type previewAccessFixture struct {
	owner, sample, original, product uuid.UUID
	store                            *media.LocalStore
}

func newPreviewAccessFixture(t *testing.T, pool *pgxpool.Pool) previewAccessFixture {
	t.Helper()
	ctx := context.Background()
	f := previewAccessFixture{owner: uuid.New(), sample: uuid.New(), original: uuid.New(), product: uuid.New(), store: media.NewLocalStore(t.TempDir())}
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,handle,display_name) VALUES($1,$2,$3,'Sample owner')`, f.owner, f.owner.String()+"@test.local", "sample_"+f.owner.String()[:8]); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{f.sample, f.original} {
		if _, err := pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,source_type,scan_status,storage_backend,storage_key,license_code)
   VALUES($1,$2,'document','Licensed content',$3,'text/plain','upload','clean','local_file',$1::uuid::text||'.txt','hcai-commercial-standard-v1')`, id, f.owner, "/api/v1/assets/"+id.String()+"/content"); err != nil {
			t.Fatal(err)
		}
		if err := f.store.Put(ctx, id.String()+".txt", []byte("Public sample content"), "text/plain"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO products(id,seller_id,asset_id,preview_asset_id,title,description,product_type,price_cents,currency,license_code,status)
  VALUES($1,$2,$3,$4,'Sample product','A separate public sample','asset',1900,'USD','hcai-commercial-standard-v1','active')`, f.product, f.owner, f.original, f.sample); err != nil {
		t.Fatal(err)
	}
	return f
}

type watchedContentBody struct {
	io.ReadCloser
	reads, closes int
}

func (b *watchedContentBody) Read(p []byte) (int, error) { b.reads++; return b.ReadCloser.Read(p) }
func (b *watchedContentBody) Close() error               { b.closes++; return b.ReadCloser.Close() }

type changingContentStore struct {
	media.Store
	change func() error
	body   *watchedContentBody
	opens  int
}

func (s *changingContentStore) Open(ctx context.Context, key string, requested *media.ByteRange) (media.Object, error) {
	object, err := s.Store.Open(ctx, key, requested)
	if err != nil {
		return object, err
	}
	s.opens++
	s.body = &watchedContentBody{ReadCloser: object.Body}
	object.Body = s.body
	if err = s.change(); err != nil {
		object.Body.Close()
		return media.Object{}, err
	}
	return object, nil
}

func TestPublicPreviewHandlesRecheckVisibilityAndLocator(t *testing.T) {
	pool, cleanup := assetTestPool(t)
	defer cleanup()
	ctx := context.Background()
	for _, when := range []string{"before_open", "during_open"} {
		for _, change := range []string{"paused", "removed", "seller_suspended", "seller_deleted", "license_retired", "sample_rejected", "original_rejected", "sample_unselected", "sample_becomes_paid_original", "locator_changed", "mime_changed"} {
			t.Run(when+"/"+change, func(t *testing.T) {
				f := newPreviewAccessFixture(t, pool)
				mutate := func() error {
					var err error
					switch change {
					case "paused":
						_, err = pool.Exec(ctx, `UPDATE products SET status='paused' WHERE id=$1`, f.product)
					case "removed":
						_, err = pool.Exec(ctx, `UPDATE products SET status='removed' WHERE id=$1`, f.product)
					case "seller_suspended":
						_, err = pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, f.owner)
					case "seller_deleted":
						_, err = pool.Exec(ctx, `UPDATE users SET status='deleted' WHERE id=$1`, f.owner)
					case "license_retired":
						_, err = pool.Exec(ctx, `UPDATE licenses SET status='retired' WHERE code='hcai-commercial-standard-v1'`)
					case "sample_rejected":
						_, err = pool.Exec(ctx, `UPDATE assets SET scan_status='rejected' WHERE id=$1`, f.sample)
					case "original_rejected":
						_, err = pool.Exec(ctx, `UPDATE assets SET scan_status='rejected' WHERE id=$1`, f.original)
					case "sample_unselected":
						_, err = pool.Exec(ctx, `UPDATE products SET preview_asset_id=NULL WHERE id=$1`, f.product)
					case "sample_becomes_paid_original":
						_, err = pool.Exec(ctx, `INSERT INTO products(id,seller_id,asset_id,title,description,product_type,price_cents,currency,license_code,status) SELECT $1,seller_id,preview_asset_id,'New paid original',description,product_type,price_cents,currency,license_code,'active' FROM products WHERE id=$2`, uuid.New(), f.product)
					case "locator_changed":
						_, err = pool.Exec(ctx, `UPDATE assets SET storage_key=$1::uuid::text||'-replacement.txt' WHERE id=$1`, f.sample)
					case "mime_changed":
						_, err = pool.Exec(ctx, `UPDATE assets SET mime_type='text/plain; charset=utf-8' WHERE id=$1`, f.sample)
					}
					return err
				}
				if change == "license_retired" {
					defer func() {
						if _, err := pool.Exec(ctx, `UPDATE licenses SET status='active' WHERE code='hcai-commercial-standard-v1'`); err != nil {
							t.Error(err)
						}
					}()
				}
				store := &changingContentStore{Store: f.store, change: func() error { return nil }}
				service := assets.NewServiceWithMedia(pool, media.NewCatalog(store), nil)
				full, err := service.Content(ctx, uuid.Nil, f.sample)
				if err != nil {
					t.Fatal(err)
				}
				rangeHandle, err := service.Content(ctx, uuid.Nil, f.sample)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = full.Stat(ctx); err != nil {
					t.Fatal(err)
				}
				if when == "before_open" {
					if err = mutate(); err != nil {
						t.Fatal(err)
					}
					if _, err = full.Stat(ctx); !errors.Is(err, assets.ErrForbidden) {
						t.Fatal("stale metadata authorized", err)
					}
				} else {
					store.change = mutate
				}
				for index, handle := range []assets.Content{full, rangeHandle} {
					var requested *media.ByteRange
					if index == 1 {
						requested = &media.ByteRange{Start: 0, End: 3}
					}
					object, err := handle.Open(ctx, requested)
					if object.Body != nil {
						object.Body.Close()
						t.Fatal("stale preview returned a body")
					}
					if !errors.Is(err, assets.ErrForbidden) {
						t.Fatalf("stale preview range=%v: %v", requested, err)
					}
				}
				expectedOpens := 0
				if when == "during_open" {
					expectedOpens = 1
				}
				if store.opens != expectedOpens {
					t.Fatal("unexpected storage read", store.opens)
				}
				if store.body != nil && (store.body.reads != 0 || store.body.closes != 1) {
					t.Fatalf("revoked body leaked or not closed: %+v", store.body)
				}
			})
		}
	}
}

func TestAssetContentRespectsIndependentPublicGrantsAndPrivateOwner(t *testing.T) {
	pool, cleanup := assetTestPool(t)
	defer cleanup()
	ctx := context.Background()
	for _, grant := range []string{"work", "site_icon"} {
		t.Run(grant, func(t *testing.T) {
			f := newPreviewAccessFixture(t, pool)
			service := assets.NewServiceWithMedia(pool, media.NewCatalog(f.store), nil)
			if grant == "work" {
				if _, err := pool.Exec(ctx, `INSERT INTO works(author_id,asset_id,title,summary,model_name,status,ai_disclosure,published_at) VALUES($1,$2,'Independent work','Separate publication','Imported','published','AI assisted',now())`, f.owner, f.sample); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := pool.Exec(ctx, `UPDATE system_settings SET site_configuration=jsonb_set(site_configuration,'{siteIconUrl}',to_jsonb($1::text)) WHERE singleton`, "/api/v1/assets/"+f.sample.String()+"/content"); err != nil {
					t.Fatal(err)
				}
			}
			public, err := service.Content(ctx, uuid.Nil, f.sample)
			if err != nil {
				t.Fatal(err)
			}
			owner, err := service.Content(ctx, f.owner, f.sample)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = pool.Exec(ctx, `UPDATE products SET status='paused' WHERE id=$1`, f.product); err != nil {
				t.Fatal(err)
			}
			object, err := public.Open(ctx, &media.ByteRange{Start: 0, End: 5})
			if err != nil {
				t.Fatal("independent grant lost", err)
			}
			body, readErr := io.ReadAll(object.Body)
			object.Body.Close()
			if readErr != nil || string(body) != "Public" {
				t.Fatal("wrong public bytes", readErr, string(body))
			}
			if grant == "work" {
				_, err = pool.Exec(ctx, `UPDATE works SET status='hidden' WHERE asset_id=$1`, f.sample)
			} else {
				_, err = pool.Exec(ctx, `UPDATE system_settings SET site_configuration=site_configuration-'siteIconUrl' WHERE singleton`)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = public.Open(ctx, nil); !errors.Is(err, assets.ErrForbidden) {
				t.Fatal("removed public grant remained readable", err)
			}
			object, err = owner.Open(ctx, nil)
			if err != nil {
				t.Fatal("owner lost private rights", err)
			}
			object.Body.Close()
			if _, err = pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, f.owner); err != nil {
				t.Fatal(err)
			}
			if _, err = owner.Open(ctx, nil); !errors.Is(err, assets.ErrForbidden) {
				t.Fatal("suspended private owner retained handle", err)
			}
			if _, err = service.Content(ctx, f.owner, f.sample); !errors.Is(err, assets.ErrForbidden) {
				t.Fatal("suspended owner acquired new private handle", err)
			}
		})
	}
}

func TestAssetHandleLocatorChangeRequiresFreshResolution(t *testing.T) {
	pool, cleanup := assetTestPool(t)
	defer cleanup()
	ctx := context.Background()
	f := newPreviewAccessFixture(t, pool)
	service := assets.NewServiceWithMedia(pool, media.NewCatalog(f.store), nil)
	stale, err := service.Content(ctx, uuid.Nil, f.sample)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.Put(ctx, "new-sample.txt", []byte("The newly selected bytes"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE assets SET storage_key='new-sample.txt' WHERE id=$1`, f.sample); err != nil {
		t.Fatal(err)
	}
	if _, err = stale.Open(ctx, nil); !errors.Is(err, assets.ErrForbidden) {
		t.Fatal("old locator survived", err)
	}
	fresh, err := service.Content(ctx, uuid.Nil, f.sample)
	if err != nil {
		t.Fatal(err)
	}
	object, err := fresh.Open(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(object.Body)
	object.Body.Close()
	if err != nil || string(body) != "The newly selected bytes" {
		t.Fatal("wrong refreshed content", err)
	}
}
