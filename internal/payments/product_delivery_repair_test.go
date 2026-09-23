package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/community"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func repairActor(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(context.Background(), `INSERT INTO users(id,email,handle,display_name,role) VALUES($1,$2,$3,'Repair operator','admin')`, id, id.String()+"@test.local", "repair_"+id.String()[:8]); err != nil {
		t.Fatal(err)
	}
	return id
}

func repairCommand(revision int, backup *uuid.UUID) productdelivery.RepairInput {
	return productdelivery.RepairInput{ExpectedRevision: &revision, SourceAssetID: backup, Confirmed: true, Reason: "Restore the accepted bytes from verified evidence."}
}

func corruptDelivery(t *testing.T, pool *pgxpool.Pool, f purchasedReferenceFixture) productdelivery.Snapshot {
	t.Helper()
	snapshot, err := productdelivery.Load(context.Background(), pool, f.order)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(f.root, snapshot.Key), []byte("damaged immutable copy"), 0600); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func repairBackup(t *testing.T, pool *pgxpool.Pool, f purchasedReferenceFixture, owner uuid.UUID, content []byte) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(context.Background(), `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key)
 VALUES($1,$2,'image','Recovery backup',$3,'image/jpeg','clean','upload','creator-owned','local_file',$1::uuid::text||'.jpg')`, id, owner, "/api/v1/assets/"+id.String()+"/content"); err != nil {
		t.Fatal(err)
	}
	if err := media.NewLocalStore(f.root).Put(context.Background(), id.String()+".jpg", content, "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestProductDeliveryRepairPreservesContractAndAuthorization(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	f := newPurchasedReferenceFixture(t, pool, "video")
	actor := repairActor(t, pool)
	snapshot := corruptDelivery(t, pool, f)
	stores := media.NewCatalog(media.NewLocalStore(f.root))
	service := productdelivery.NewRepairService(pool, stores)
	var contractBefore string
	if err := pool.QueryRow(ctx, `SELECT contract::text FROM product_order_contracts WHERE order_id=$1`, f.order).Scan(&contractBefore); err != nil {
		t.Fatal(err)
	}
	for _, user := range []uuid.UUID{uuid.Nil, f.buyer} {
		if _, err := service.Inspect(ctx, user, f.order); !errors.Is(err, productdelivery.ErrRepairForbidden) {
			t.Fatalf("unauthorized inspection: %v", err)
		}
	}
	status, err := service.Inspect(ctx, actor, f.order)
	if err != nil || !status.CanRepair || status.Health != "corrupt" || status.Revision != 0 {
		t.Fatalf("inspection: %+v %v", status, err)
	}
	if _, err = service.Repair(ctx, f.buyer, f.order, "buyer-repair-attempt", "test", repairCommand(0, nil)); !errors.Is(err, productdelivery.ErrRepairForbidden) {
		t.Fatalf("buyer repair: %v", err)
	}
	status, err = service.Repair(ctx, actor, f.order, "restore-original-command", "test", repairCommand(0, nil))
	if err != nil || status.Health != "healthy" || status.CanRepair || status.Revision != 1 || status.RepairedAt == nil {
		t.Fatalf("repair: %+v %v", status, err)
	}
	assertPurchasedBytes(t, pool, f.root, f.buyer, f.purchased, f.content)
	repaired, err := productdelivery.Load(ctx, pool, f.order)
	if err != nil || repaired.Key == snapshot.Key || repaired.SHA256 != snapshot.SHA256 || repaired.Size != snapshot.Size {
		t.Fatalf("accepted evidence changed: %+v %v", repaired, err)
	}
	old, err := os.ReadFile(filepath.Join(f.root, snapshot.Key))
	if err != nil || string(old) != "damaged immutable copy" {
		t.Fatalf("old evidence overwritten: %q %v", old, err)
	}
	content, err := assets.NewService(pool, f.root).Content(ctx, f.buyer, f.purchased)
	if err != nil {
		t.Fatal(err)
	}
	obj, err := content.Open(ctx, &media.ByteRange{Start: 1, End: 6})
	if err != nil {
		t.Fatal(err)
	}
	part, readErr := io.ReadAll(obj.Body)
	obj.Body.Close()
	if readErr != nil || string(part) != string(f.content[1:7]) {
		t.Fatalf("range: %q %v", part, readErr)
	}
	if _, err = assets.NewService(pool, f.root).Content(ctx, actor, f.purchased); !errors.Is(err, assets.ErrForbidden) {
		t.Fatalf("repair granted operator buyer access: %v", err)
	}
	if _, err = service.Repair(ctx, actor, f.order, "restore-original-command", "test", repairCommand(0, nil)); err != nil {
		t.Fatal("idempotent replay", err)
	}
	if _, err = service.Repair(ctx, actor, f.order, "restore-original-command", "test", repairCommand(1, nil)); !errors.Is(err, productdelivery.ErrRepairConflict) {
		t.Fatalf("changed replay accepted: %v", err)
	}
	if _, err = service.Repair(ctx, actor, f.order, "overwrite-healthy-command", "test", repairCommand(1, nil)); !errors.Is(err, productdelivery.ErrRepairConflict) {
		t.Fatalf("healthy replacement: %v", err)
	}
	var contractAfter string
	var count, audits int
	if err = pool.QueryRow(ctx, `SELECT contract::text,(SELECT count(*) FROM product_delivery_repairs WHERE order_id=$1),
 (SELECT count(*) FROM audit_events WHERE action='marketplace.delivery_repair_completed' AND resource_id=$1)
 FROM product_order_contracts WHERE order_id=$1`, f.order).Scan(&contractAfter, &count, &audits); err != nil || contractAfter != contractBefore || count != 1 || audits != 1 {
		t.Fatalf("contract/replay evidence: count=%d audits=%d err=%v", count, audits, err)
	}
	for _, sql := range []string{`UPDATE product_delivery_repairs SET reason='rewrite private evidence' WHERE order_id=$1`, `DELETE FROM product_delivery_repairs WHERE order_id=$1`} {
		if _, err = pool.Exec(ctx, sql, f.order); err == nil {
			t.Fatal("mutable repair evidence", sql)
		}
	}
	pkg, _ := runProductExport(t, pool, f.buyer)
	repairs := pkg.Data.Marketplace.Data["deliveryRepairs"]
	if len(repairs) != 1 || repairs[0]["orderId"] != f.order.String() || repairs[0]["state"] != "ready" || len(repairs[0]) != 6 {
		t.Fatalf("buyer export: %+v", repairs)
	}
	pkg, _ = runProductExport(t, pool, actor)
	if len(pkg.Data.Marketplace.Data["deliveryRepairs"]) != 0 {
		t.Fatal("operator inherited buyer export")
	}
	down, err := os.ReadFile("../platform/database/migrations/0095_product_delivery_repair.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(down)); err == nil {
		t.Fatal("rollback discarded repair evidence")
	}
}

func TestProductDeliveryRepairBackupEligibilityAndPublicProtection(t *testing.T) {
	for _, scenario := range []string{"valid", "wrong_bytes", "foreign", "rejected", "pending", "public_work", "preview", "alias"} {
		t.Run(scenario, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx := context.Background()
			f := newPurchasedReferenceFixture(t, pool, "video")
			actor := repairActor(t, pool)
			corruptDelivery(t, pool, f)
			owner := actor
			if scenario == "foreign" {
				owner = f.buyer
			}
			body := f.content
			if scenario == "wrong_bytes" {
				body = []byte("different bytes")
			}
			backup := repairBackup(t, pool, f, owner, body)
			var err error
			switch scenario {
			case "rejected", "pending":
				_, err = pool.Exec(ctx, `UPDATE assets SET scan_status=$2 WHERE id=$1`, backup, scenario)
			case "public_work":
				_, err = community.NewRepository(pool).Publish(ctx, actor, community.PublishInput{AssetID: backup, Title: "A previously public upload", PromptVisibility: "private", AIDisclosure: "Independent uploaded content."})
			case "preview":
				_, err = pool.Exec(ctx, `UPDATE products SET preview_asset_id=$1 WHERE asset_id=$2`, backup, f.source)
			case "alias":
				_, err = pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key)
 SELECT $1,owner_id,kind,'Alias',media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key FROM assets WHERE id=$2`, uuid.New(), backup)
				var duplicate *pgconn.PgError
				if !errors.As(err, &duplicate) || duplicate.Code != "23505" || duplicate.ConstraintName != "assets_storage_object_unique" {
					t.Fatalf("duplicate source registration was not rejected: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			service := productdelivery.NewRepairService(pool, media.NewCatalog(media.NewLocalStore(f.root)))
			status, err := service.Repair(ctx, actor, f.order, "backup-repair-command", "test", repairCommand(0, &backup))
			if scenario != "valid" {
				want := productdelivery.ErrRepairInvalid
				if scenario == "wrong_bytes" {
					want = media.ErrIntegrity
				}
				if !errors.Is(err, want) {
					t.Fatalf("invalid backup: %+v %v", status, err)
				}
				return
			}
			if err != nil || status.Health != "healthy" {
				t.Fatalf("backup repair: %+v %v", status, err)
			}
			for _, view := range []string{"product_preview_candidates", "product_source_candidates"} {
				var candidate bool
				if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM `+view+` WHERE asset_id=$1)`, backup).Scan(&candidate); err != nil || candidate {
					t.Fatalf("protected backup entered %s: %v %v", view, candidate, err)
				}
			}
			if _, err = community.NewRepository(pool).Publish(ctx, actor, community.PublishInput{AssetID: backup, Title: "Publish protected backup", PromptVisibility: "private", AIDisclosure: "Independent uploaded content."}); !errors.Is(err, community.ErrForbidden) {
				t.Fatalf("backup publication: %v", err)
			}
		})
	}
}

type ambiguousRepairStore struct {
	*media.LocalStore
}

func (s *ambiguousRepairStore) PutStream(ctx context.Context, key string, body io.ReadSeeker, size int64, digest, mime string) error {
	if err := s.LocalStore.PutStream(ctx, key, body, size, digest, mime); err != nil {
		return err
	}
	return errors.New("write completed but response was lost")
}

func TestProductDeliveryRepairResumesInterruptedCopies(t *testing.T) {
	for _, scenario := range []string{"copy_not_written", "ready_commit_lost", "backup_ready_commit_lost", "ambiguous_put", "superseded"} {
		t.Run(scenario, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx := context.Background()
			f := newPurchasedReferenceFixture(t, pool, "video")
			actor := repairActor(t, pool)
			corruptDelivery(t, pool, f)
			input := repairCommand(0, nil)
			if scenario == "backup_ready_commit_lost" {
				backup := repairBackup(t, pool, f, actor, f.content)
				input.SourceAssetID = &backup
			}
			store := &failedDeliveryStore{LocalStore: media.NewLocalStore(f.root), fail: scenario == "copy_not_written" || scenario == "superseded"}
			stores := media.NewCatalog(store)
			if scenario == "ambiguous_put" {
				stores = media.NewCatalog(&ambiguousRepairStore{LocalStore: store.LocalStore})
			}
			service := productdelivery.NewRepairService(pool, stores)
			if strings.HasSuffix(scenario, "ready_commit_lost") {
				if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_repair_ready() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'injected commit loss'; END $$ LANGUAGE plpgsql;
 CREATE TRIGGER reject_repair_ready BEFORE UPDATE ON product_delivery_repairs FOR EACH ROW EXECUTE FUNCTION reject_repair_ready()`); err != nil {
					t.Fatal(err)
				}
			}
			status, err := service.Repair(ctx, actor, f.order, "interrupted-repair-key", "test", input)
			if scenario == "ambiguous_put" {
				if err != nil || status.Health != "healthy" {
					t.Fatalf("ambiguous write should verify target: %+v %v", status, err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected injected failure")
			}
			var pending uuid.UUID
			var state string
			if err = pool.QueryRow(ctx, `SELECT id,state FROM product_delivery_repairs WHERE order_id=$1`, f.order).Scan(&pending, &state); err != nil || state != "prepared" {
				t.Fatalf("reservation lost: %s %v", state, err)
			}
			store.fail = false
			if strings.HasSuffix(scenario, "ready_commit_lost") {
				if _, err = pool.Exec(ctx, `DROP TRIGGER reject_repair_ready ON product_delivery_repairs`); err != nil {
					t.Fatal(err)
				}
				// The target already exists; resume must not need mutable source bytes.
				if err = os.Remove(filepath.Join(f.root, f.source.String()+".jpg")); err != nil {
					t.Fatal(err)
				}
				if input.SourceAssetID != nil {
					// An operator can leave after the target write. A different
					// authorized operator must still finish from verified bytes.
					deleteMarketplaceAccount(t, pool, f.root, actor)
					actor = repairActor(t, pool)
				}
			}
			if scenario == "superseded" {
				status, err = service.Repair(ctx, actor, f.order, "replacement-repair-key", "test", repairCommand(1, nil))
				if err != nil || status.Revision != 2 {
					t.Fatalf("new revision: %+v %v", status, err)
				}
				if _, err = service.Resume(ctx, actor, f.order, pending, "test"); !errors.Is(err, productdelivery.ErrRepairConflict) {
					t.Fatalf("stale pending continued: %v", err)
				}
			} else {
				status, err = service.Resume(ctx, actor, f.order, pending, "test")
				if err != nil || status.Health != "healthy" || status.PendingID != nil {
					t.Fatalf("resume: %+v %v", status, err)
				}
			}
			assertPurchasedBytes(t, pool, f.root, f.buyer, f.purchased, f.content)
			if scenario == "superseded" {
				deleteMarketplaceAccount(t, pool, f.root, f.buyer)
				var count int
				if err = pool.QueryRow(ctx, `SELECT count(*) FROM product_delivery_repairs WHERE order_id=$1 AND state='removed'`, f.order).Scan(&count); err != nil || count != 2 {
					t.Fatalf("superseded evidence escaped cleanup: %d %v", count, err)
				}
			}
		})
	}
}

func TestProductDeliveryRepairAliasDeletionAndRefundCleanup(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	f := newPurchasedReferenceFixture(t, pool, "video")
	actor := repairActor(t, pool)
	original := corruptDelivery(t, pool, f)
	stores := media.NewCatalog(media.NewLocalStore(f.root))
	service := productdelivery.NewRepairService(pool, stores)
	if _, err := service.Repair(ctx, actor, f.order, "repair-before-alias-delete", "test", repairCommand(0, nil)); err != nil {
		t.Fatal(err)
	}
	repaired, err := productdelivery.Load(ctx, pool, f.order)
	if err != nil {
		t.Fatal(err)
	}
	alias := uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO assets(id,owner_id,kind,title,media_url,mime_type,scan_status,source_type,license_code,storage_backend,storage_key)
 SELECT $1,$2,kind,'Registered target alias',media_url,mime_type,'clean','upload',license_code,$3,$4 FROM assets WHERE id=$5`, alias, actor, repaired.Backend, repaired.Key, f.source); err != nil {
		t.Fatal(err)
	}
	deleteMarketplaceAccount(t, pool, f.root, actor)
	assertPurchasedBytes(t, pool, f.root, f.buyer, f.purchased, f.content)
	var payment, product uuid.UUID
	if err = pool.QueryRow(ctx, `SELECT id,resource_id FROM payment_intents WHERE order_id=$1`, f.order).Scan(&payment, &product); err != nil {
		t.Fatal(err)
	}
	payments := newPaymentTestService(t, pool, ServiceConfig{Enabled: true, APIVersion: testStripeAPIVersion, WebhookSecret: testStripeWebhookSecret, WebhookTolerance: 5 * time.Minute}, NewRuntimeCatalog(&productCheckoutRuntime{}))
	if _, err = payments.BeginProductRefund(ctx, f.buyer, f.order, "repaired-order-refund", "test", "The resource did not meet the expected requirements."); err != nil {
		t.Fatal(err)
	}
	if err = payments.HandleProductRefundJob(ctx, currentProductRefundJob(t, pool, payment)); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	payments.verifier.now = func() time.Time { return now }
	event := receivePaymentWorkflowEvent(t, payments, productRefundEvent("evt_repair_refunded", "re_workflow123", "succeeded", payment, product, now.Unix(), 1900), now)
	if err = payments.HandlePaymentEventJob(ctx, jobs.Job{Payload: []byte(fmt.Sprintf(`{"eventId":%q}`, event.EventID))}); err != nil {
		t.Fatal(err)
	}
	if _, err = assets.NewService(pool, f.root).Content(ctx, f.buyer, f.purchased); !errors.Is(err, assets.ErrForbidden) {
		t.Fatalf("refund retained access: %v", err)
	}
	var job jobs.Job
	if err = pool.QueryRow(ctx, `SELECT id,payload FROM jobs WHERE kind=$1 AND payload->>'orderId'=$2 LIMIT 1`, productdelivery.CleanupJobKind, f.order.String()).Scan(&job.ID, &job.Payload); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO data_rights_legal_holds(user_id,reason,authority_reference_hash,review_at,expires_at,created_by)
 VALUES($1,'Preserve repaired transaction',$2,now()+interval '1 day',now()+interval '2 days',$1)`, f.buyer, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if err = productdelivery.CleanupHandler(pool, stores)(ctx, job); !errors.Is(err, productdelivery.ErrLegalHold) {
		t.Fatalf("hold bypassed: %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE data_rights_legal_holds SET status='released',released_by=$1,released_at=now() WHERE user_id=$1`, f.buyer); err != nil {
		t.Fatal(err)
	}
	// Cleanup visits stable backend/key order. A later acknowledgement that
	// leaves bytes behind must not mark any snapshot or repair removed, even
	// though an earlier physical deletion cannot be rolled back.
	first, last := original.Key, repaired.Key
	if first > last {
		first, last = last, first
	}
	unverified := &unverifiableDeletionStore{Store: stores.Primary(), mode: "noop", target: last}
	if err := productdelivery.CleanupHandler(pool, media.NewCatalog(unverified))(ctx, job); !errors.Is(err, media.ErrDeletionUnverified) {
		t.Fatal("partial cleanup was reported complete", err)
	}
	if _, err := stores.Primary().Stat(ctx, first); !errors.Is(err, media.ErrNotFound) {
		t.Fatal("partial deletion was not exercised", err)
	}
	if _, err := stores.Primary().Stat(ctx, last); err != nil {
		t.Fatal("uncertain object disappeared", err)
	}
	var premature int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM product_delivery_snapshots WHERE order_id=$1 AND (state='removed' OR removed_at IS NOT NULL))+
 (SELECT count(*) FROM product_delivery_repairs WHERE order_id=$1 AND (state='removed' OR removed_at IS NOT NULL))`, f.order).Scan(&premature); err != nil || premature != 0 {
		t.Fatal("partial cleanup committed removal evidence", premature, err)
	}
	for range 2 {
		if err = productdelivery.CleanupHandler(pool, stores)(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []string{original.Key, repaired.Key} {
		if _, err = stores.Primary().Stat(ctx, key); !errors.Is(err, media.ErrNotFound) {
			t.Fatalf("copy not removed: %s %v", key, err)
		}
	}
	if _, err = stores.Primary().Stat(ctx, original.SourceKey); err != nil {
		t.Fatal("unrelated original removed", err)
	}
	var state string
	if err = pool.QueryRow(ctx, `SELECT state FROM product_delivery_repairs WHERE order_id=$1`, f.order).Scan(&state); err != nil || state != "removed" {
		t.Fatalf("repair removal evidence: %s %v", state, err)
	}
	encoded, _ := json.Marshal(job)
	if strings.Contains(string(encoded), repaired.Key) {
		t.Fatal("job payload contains storage key")
	}
}

func TestProductDeliveryRepairSerializesBackupPublication(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	f := newPurchasedReferenceFixture(t, pool, "video")
	actor := repairActor(t, pool)
	corruptDelivery(t, pool, f)
	backup := repairBackup(t, pool, f, actor, f.content)
	gate, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(ctx)
	if _, err = gate.Exec(ctx, `SELECT id FROM assets WHERE id=$1 FOR UPDATE`, backup); err != nil {
		t.Fatal(err)
	}
	service := productdelivery.NewRepairService(pool, media.NewCatalog(media.NewLocalStore(f.root)))
	done := make(chan error, 1)
	go func() {
		_, e := service.Repair(ctx, actor, f.order, "publication-race-repair", "test", repairCommand(0, &backup))
		done <- e
	}()
	waitForProductBlockingTx(t, ctx, pool, int32(gate.Conn().PgConn().PID()))
	// This is the commit of a competing work publication while repair waits
	// on the shared source lock. Candidate inspection needs a fresh snapshot.
	if _, err = gate.Exec(ctx, `INSERT INTO works(id,author_id,asset_id,title,summary,prompt_visibility,model_name,status,ai_disclosure,published_at)
 VALUES($1,$2,$3,'Published while recovery waits','','private','Imported asset','published','Independent upload',now())`, uuid.New(), actor, backup); err != nil {
		t.Fatal(err)
	}
	if err = gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, productdelivery.ErrRepairInvalid) {
		t.Fatalf("stale eligibility allowed public backup: %v", err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM product_delivery_repairs WHERE order_id=$1`, f.order).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejected command reserved evidence: %d %v", count, err)
	}
}

func TestProductDeliveryRepairConcurrentRevisionAndRevocation(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	f := newPurchasedReferenceFixture(t, pool, "video")
	actor := repairActor(t, pool)
	corruptDelivery(t, pool, f)
	service := productdelivery.NewRepairService(pool, media.NewCatalog(media.NewLocalStore(f.root)))
	done := make(chan error, 2)
	for _, key := range []string{"concurrent-repair-first", "concurrent-repair-second"} {
		go func() { _, e := service.Repair(ctx, actor, f.order, key, "test", repairCommand(0, nil)); done <- e }()
	}
	one, two := <-done, <-done
	if !((one == nil && errors.Is(two, productdelivery.ErrRepairConflict)) || (two == nil && errors.Is(one, productdelivery.ErrRepairConflict))) {
		t.Fatalf("concurrent revision: %v %v", one, two)
	}
	corruptDelivery(t, pool, f)
	gate, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(ctx)
	if _, err = gate.Exec(ctx, `SELECT id FROM payment_intents WHERE order_id=$1 FOR UPDATE`, f.order); err != nil {
		t.Fatal(err)
	}
	go func() {
		_, e := service.Repair(ctx, actor, f.order, "concurrent-refund-repair", "test", repairCommand(1, nil))
		done <- e
	}()
	waitForProductBlockingTx(t, ctx, pool, int32(gate.Conn().PgConn().PID()))
	if _, err = gate.Exec(ctx, `UPDATE entitlements SET status='refunded',revoked_at=now() WHERE order_id=$1`, f.order); err != nil {
		t.Fatal(err)
	}
	if err = gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, productdelivery.ErrRepairConflict) {
		t.Fatalf("repair ignored concurrent revocation: %v", err)
	}
}
