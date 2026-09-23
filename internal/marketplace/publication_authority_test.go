package marketplace_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

func publicationEvidence(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) string {
	t.Helper()
	var result string
	if err := pool.QueryRow(t.Context(), `SELECT jsonb_build_object(
 'product',(SELECT to_jsonb(p) FROM products p WHERE p.id=$1),
 'publication',(SELECT to_jsonb(p) FROM product_publications p WHERE p.product_id=$1),
 'commands',(SELECT jsonb_agg(to_jsonb(c) ORDER BY c.key_sha256) FROM product_listing_commands c WHERE c.product_id=$1),
 'audit',(SELECT jsonb_agg(to_jsonb(a) ORDER BY a.id) FROM audit_events a WHERE a.resource_id=$1),
 'notifications',(SELECT jsonb_agg(to_jsonb(n) ORDER BY n.id) FROM notifications n WHERE n.resource_id=$1))::text`, id).Scan(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

// Force a Repeatable Read snapshot before permission removal. A plain EXISTS
// sees the obsolete grant; a locking authority query must reject that snapshot.
func TestListingReviewRejectsObsoletePermissionSnapshot(t *testing.T) {
	for _, action := range []string{"get", "list", "file"} {
		t.Run(action, func(t *testing.T) {
			pool, cleanup := marketplaceTestPool(t)
			t.Cleanup(cleanup)
			seller, actor, draft := listingFixture(t, pool)
			item, err := marketplace.NewService(pool).MutateListing(t.Context(), seller, uuid.Nil, "create", "authority-create", "fixture", marketplace.ListingMutation{Draft: &draft})
			if err != nil {
				t.Fatal(err)
			}
			// listingActor first reads the actor. Pause the permission lookup so
			// deletion commits after the transaction's first snapshot was taken.
			traced, entered, release := testutil.GateQuery(t, pool, "FROM role_permissions WHERE role=")
			cfg := traced.Config()
			cfg.ConnConfig.RuntimeParams["default_transaction_isolation"] = "repeatable read"
			commandPool, err := pgxpool.NewWithConfig(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(commandPool.Close)
			service := marketplace.NewService(commandPool)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			var workers sync.WaitGroup
			t.Cleanup(func() { release(); cancel(); workers.Wait() })
			before := publicationEvidence(t, pool, item.ID)
			run := func() error {
				switch action {
				case "get":
					_, err := service.GetListing(ctx, actor, item.ID, true)
					return err
				case "list":
					_, err := service.ListListings(ctx, actor, true, marketplace.ListingFilter{})
					return err
				default:
					_, _, err := service.ReviewListingFile(ctx, actor, item.ID, "source", item.Version, "review")
					return err
				}
			}
			done := make(chan error, 1)
			workers.Add(1)
			go func() { defer workers.Done(); done <- run() }()
			select {
			case <-entered:
			case err := <-done:
				t.Fatal("permission gate not reached", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if _, err := pool.Exec(ctx, `DELETE FROM role_permissions WHERE role='admin' AND permission_id='admin:content'`); err != nil {
				t.Fatal(err)
			}
			release()
			if err := <-done; !errors.Is(err, marketplace.ErrListingForbidden) && !errors.Is(err, marketplace.ErrListingConflict) {
				t.Errorf("obsolete grant used: %v", err)
			}
			if before != publicationEvidence(t, pool, item.ID) {
				t.Error("revoked review changed evidence")
			}
			if err := run(); !errors.Is(err, marketplace.ErrListingForbidden) {
				t.Errorf("fresh revoked read accepted: %v", err)
			}
		})
	}
}

func TestListingReviewPinsPermissionThroughDecision(t *testing.T) {
	for _, action := range []string{"approve", "reject", "block", "reopen", "replay"} {
		t.Run(action, func(t *testing.T) {
			pool, cleanup := marketplaceTestPool(t)
			t.Cleanup(cleanup)
			seller, actor, draft := listingFixture(t, pool)
			svc := marketplace.NewService(pool)
			item, err := svc.MutateListing(t.Context(), seller, uuid.Nil, "create", "authority-create", "fixture", marketplace.ListingMutation{Draft: &draft})
			if err != nil {
				t.Fatal(err)
			}
			item, err = svc.MutateListing(t.Context(), seller, item.ID, "submit", "authority-submit", "fixture", marketplace.ListingMutation{ExpectedVersion: item.Version, RightsConfirmed: true})
			if err != nil {
				t.Fatal(err)
			}
			if action == "reopen" {
				item, err = svc.MutateListing(t.Context(), actor, item.ID, "block", "authority-block", "fixture", marketplace.ListingMutation{ExpectedVersion: item.Version, Confirmed: true, Reason: "Hold the listing for rights verification."})
				if err != nil {
					t.Fatal(err)
				}
			}
			command := action
			if action == "replay" {
				command = "approve"
			}
			input := marketplace.ListingMutation{ExpectedVersion: item.Version, Confirmed: true, Reason: "Review the accepted product and original rights."}
			if action == "replay" {
				if _, err := svc.MutateListing(t.Context(), actor, item.ID, command, "authority-decision", "original", input); err != nil {
					t.Fatal(err)
				}
			}
			query := "SELECT seller_id FROM products WHERE id=$1 FOR UPDATE"
			if action == "replay" {
				query = "SELECT product_id,request_sha256 FROM product_listing_commands"
			}
			traced, entered, release := testutil.GateQuery(t, pool, query)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			var workers sync.WaitGroup
			t.Cleanup(func() { release(); cancel(); workers.Wait() })
			done := make(chan error, 1)
			workers.Add(1)
			go func() {
				defer workers.Done()
				_, err := marketplace.NewService(traced).MutateListing(ctx, actor, item.ID, command, "authority-decision", "protected", input)
				done <- err
			}()
			select {
			case <-entered:
			case err := <-done:
				t.Fatal("decision gate not reached", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			conn, err := pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			pid := int32(conn.Conn().PgConn().PID())
			revoked := make(chan error, 1)
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer conn.Release()
				_, err := conn.Exec(ctx, `DELETE FROM role_permissions WHERE role='admin' AND permission_id='admin:content'`)
				revoked <- err
			}()
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				var blocked bool
				if err := pool.QueryRow(ctx, `SELECT cardinality(pg_blocking_pids($1))>0`, pid).Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				select {
				case err := <-revoked:
					t.Fatalf("permission removal bypassed active review: %v", err)
				case <-ticker.C:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			release()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if err := <-revoked; err != nil {
				t.Fatal(err)
			}
			var count int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM product_listing_commands WHERE actor_id=$1 AND product_id=$2 AND action=$3`, actor, item.ID, command).Scan(&count); err != nil || count != 1 {
				t.Fatal("review command changed", count, err)
			}
			if _, err := svc.MutateListing(ctx, actor, item.ID, command, "authority-decision", "revoked", input); !errors.Is(err, marketplace.ErrListingForbidden) {
				t.Fatal("revoked replay allowed", err)
			}
		})
	}
}

func TestListingFileGrantBindsExactReviewContext(t *testing.T) {
	pool, cleanup := marketplaceTestPool(t)
	t.Cleanup(cleanup)
	seller, actor, draft := bundleListingFixture(t, pool)
	svc := marketplace.NewService(pool)
	item, err := svc.MutateListing(t.Context(), seller, uuid.Nil, "create", "file-grant-create", "fixture", marketplace.ListingMutation{Draft: &draft})
	if err != nil {
		t.Fatal(err)
	}
	index := 1
	owner, file, err := svc.ReviewListingFileAt(t.Context(), actor, item.ID, "source", item.Version, "review", &index)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RecheckListingFile(t.Context(), actor, item.ID, owner, file, "source", item.Version, &index); err != nil {
		t.Fatal("valid member denied", err)
	}
	zero := 0
	for _, check := range []struct {
		name                        string
		actor, product, owner, file uuid.UUID
		kind, version               string
		index                       *int
	}{
		{"wrong_actor", seller, item.ID, owner, file, "source", item.Version, &index},
		{"wrong_product", actor, uuid.New(), owner, file, "source", item.Version, &index},
		{"wrong_owner", actor, item.ID, actor, file, "source", item.Version, &index},
		{"wrong_file", actor, item.ID, owner, draft.Files[0].AssetID, "source", item.Version, &index},
		{"wrong_index", actor, item.ID, owner, file, "source", item.Version, &zero},
		{"missing_index", actor, item.ID, owner, file, "source", item.Version, nil},
		{"invalid_version", actor, item.ID, owner, file, "source", "old-version", &index},
		{"source_as_preview", actor, item.ID, owner, file, "preview", item.Version, nil},
	} {
		t.Run(check.name, func(t *testing.T) {
			if err := svc.RecheckListingFile(t.Context(), check.actor, check.product, check.owner, check.file, check.kind, check.version, check.index); !errors.Is(err, marketplace.ErrListingForbidden) {
				t.Fatal("unbound grant accepted", err)
			}
		})
	}
	if item.PreviewAssetID != nil {
		if err := svc.RecheckListingFile(t.Context(), actor, item.ID, owner, *item.PreviewAssetID, "preview", item.Version, nil); err != nil {
			t.Fatal("valid preview denied", err)
		}
	}
}
