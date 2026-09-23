package payments

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
)

type repeatedRepairByte byte

func (b repeatedRepairByte) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(b)
	}
	return len(p), nil
}

type interruptedRepairBody struct{}

func (interruptedRepairBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type countedRepairBody struct{ reads int }

func (b *countedRepairBody) Read([]byte) (int, error) { b.reads++; return 0, io.EOF }

type gatedRepairBody struct {
	io.Reader
	started, release chan struct{}
	once             sync.Once
}

func (b *gatedRepairBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.started); <-b.release })
	return b.Reader.Read(p)
}
func waitRepairGate(t *testing.T, gate <-chan struct{}) {
	t.Helper()
	select {
	case <-gate:
	case <-time.After(10 * time.Second):
		t.Fatal("upload did not begin")
	}
}

func TestProductDeliveryUploadLargeExactBytesAndCleanup(t *testing.T) {
	for _, size := range []int64{11 << 20, productdelivery.MaxBytes} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx := context.Background()
			f := newPurchasedReferenceWithBytes(t, pool, "video", io.LimitReader(repeatedRepairByte('x'), size))
			actor := repairActor(t, pool)
			old := corruptDelivery(t, pool, f)
			if err := os.Remove(filepath.Join(f.root, old.SourceKey)); err != nil {
				t.Fatal(err)
			}
			svc := productdelivery.NewRepairService(pool, media.NewCatalog(media.NewLocalStore(f.root)))
			var contract string
			if err := pool.QueryRow(ctx, `SELECT contract::text FROM product_order_contracts WHERE order_id=$1`, f.order).Scan(&contract); err != nil {
				t.Fatal(err)
			}
			status, err := svc.PrepareUpload(ctx, actor, f.order, "large-upload-reservation", "test", repairCommand(0, nil))
			if err != nil || status.PendingID == nil || status.PendingSource != "upload" || status.SizeBytes != size {
				t.Fatalf("prepare: %+v %v", status, err)
			}
			repair := *status.PendingID
			if _, err = svc.Resume(ctx, actor, f.order, repair, "test"); !errors.Is(err, productdelivery.ErrRepairUploadRequired) {
				t.Fatal("missing bytes", err)
			}
			for range 2 {
				status, err = svc.Upload(ctx, actor, f.order, repair, true, "test", io.LimitReader(repeatedRepairByte('x'), size))
				if err != nil || status.Health != "healthy" || status.PendingID != nil {
					t.Fatalf("upload/replay: %+v %v", status, err)
				}
			}
			replay, err := svc.PrepareUpload(ctx, actor, f.order, "large-upload-reservation", "test", repairCommand(0, nil))
			if err != nil || replay.Revision != 1 || replay.Health != "healthy" {
				t.Fatalf("reservation replay: %+v %v", replay, err)
			}
			delivery, err := productdelivery.Load(ctx, pool, f.order)
			if err != nil || delivery.Key == old.Key || delivery.SHA256 != old.SHA256 || delivery.Size != size {
				t.Fatal("snapshot changed", err)
			}
			content, err := assets.NewService(pool, f.root).Content(ctx, f.buyer, f.purchased)
			if err != nil {
				t.Fatal(err)
			}
			obj, err := content.Open(ctx, &media.ByteRange{Start: size - 8, End: size - 1})
			if err != nil {
				t.Fatal(err)
			}
			actual, err := io.ReadAll(obj.Body)
			obj.Body.Close()
			if err != nil || string(actual) != "xxxxxxxx" {
				t.Fatalf("range %q %v", actual, err)
			}
			var after string
			var count, sources int
			if err = pool.QueryRow(ctx, `SELECT contract::text,(SELECT count(*) FROM audit_events WHERE resource_id=$1 AND action='marketplace.delivery_repair_completed'),(SELECT count(*) FROM product_delivery_repairs WHERE order_id=$1 AND source_kind='upload' AND source_asset_id IS NULL AND source_backend IS NULL AND source_key IS NULL) FROM product_order_contracts WHERE order_id=$1`, f.order).Scan(&after, &count, &sources); err != nil || contract != after || count != 1 || sources != 1 {
				t.Fatalf("evidence %d %d %v", count, sources, err)
			}
			pkg, _ := runProductExport(t, pool, f.buyer)
			// The shared export helper checks the complete section contract.
			// This boundary verifies the upload's exact public field allowlist.
			repairs := pkg.Data.Marketplace.Data["deliveryRepairs"]
			if len(repairs) != 1 || len(repairs[0]) != 6 {
				t.Fatal("unsafe upload export")
			}
			for _, field := range []string{"orderId", "revision", "state", "createdAt", "readyAt", "removedAt"} {
				if _, ok := repairs[0][field]; !ok {
					t.Fatalf("missing allowed upload export field %s", field)
				}
			}
			if repairs[0]["orderId"] != f.order.String() || repairs[0]["revision"] != float64(1) || repairs[0]["state"] != "ready" {
				t.Fatal("upload export did not retain the original order and completed revision")
			}
			deleteMarketplaceAccount(t, pool, f.root, f.buyer)
			if _, err = os.Stat(filepath.Join(f.root, delivery.Key)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("upload destination survived cleanup", err)
			}
		})
	}
}

func TestProductDeliveryUploadValidationReplayAndMigration(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	f := newPurchasedReferenceFixture(t, pool, "video")
	actor := repairActor(t, pool)
	corruptDelivery(t, pool, f)
	svc := productdelivery.NewRepairService(pool, media.NewCatalog(media.NewLocalStore(f.root)))
	// Stored evidence remains readable after rolling back/reapplying 0096.
	if _, err := svc.Repair(ctx, actor, f.order, "stored-before-migration", "test", repairCommand(0, nil)); err != nil {
		t.Fatal(err)
	}
	restoreBundleLifecycle := rollbackBundleLifecycle(t, pool)
	for _, direction := range []string{"down", "up"} {
		sql, err := os.ReadFile("../platform/database/migrations/0096_product_delivery_upload." + direction + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, string(sql)); err != nil {
			t.Fatal("stored evidence migration", err)
		}
	}
	restoreBundleLifecycle()
	corruptDelivery(t, pool, f)
	status, err := svc.PrepareUpload(ctx, actor, f.order, "upload-reservation-key", "test", repairCommand(1, nil))
	if err != nil || status.PendingID == nil {
		t.Fatal(status, err)
	}
	repair := *status.PendingID
	same, err := svc.PrepareUpload(ctx, actor, f.order, "upload-reservation-key", "test", repairCommand(1, nil))
	if err != nil || same.PendingID == nil || *same.PendingID != repair {
		t.Fatal("unstable reservation", err)
	}
	if _, err = svc.Repair(ctx, actor, f.order, "upload-reservation-key", "test", repairCommand(1, nil)); !errors.Is(err, productdelivery.ErrRepairConflict) {
		t.Fatal("cross-mode key replay", err)
	}
	changed := repairCommand(1, nil)
	changed.Reason += " Altered."
	if _, err = svc.PrepareUpload(ctx, actor, f.order, "upload-reservation-key", "test", changed); !errors.Is(err, productdelivery.ErrRepairConflict) {
		t.Fatal("altered replay", err)
	}
	for _, c := range []struct {
		name      string
		actor     uuid.UUID
		confirmed bool
		body      io.Reader
		want      error
	}{
		{"unconfirmed", actor, false, &countedRepairBody{}, productdelivery.ErrRepairInvalid},
		{"foreign", f.buyer, true, &countedRepairBody{}, productdelivery.ErrRepairForbidden},
		{"short", actor, true, strings.NewReader("short"), media.ErrIntegrity},
		{"long", actor, true, bytes.NewReader(append(append([]byte{}, f.content...), 1)), media.ErrIntegrity},
		{"wrong_hash", actor, true, strings.NewReader(strings.Repeat("x", len(f.content))), media.ErrIntegrity},
		{"interrupted", actor, true, interruptedRepairBody{}, io.ErrUnexpectedEOF},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := svc.Upload(ctx, c.actor, f.order, repair, c.confirmed, "test", c.body); !errors.Is(err, c.want) {
				t.Fatalf("%v", err)
			}
			if b, ok := c.body.(*countedRepairBody); ok && b.reads != 0 {
				t.Fatal("read unauthorized body")
			}
		})
	}
	var target, state string
	if err = pool.QueryRow(ctx, `SELECT storage_key,state FROM product_delivery_repairs WHERE id=$1`, repair).Scan(&target, &state); err != nil || state != "prepared" {
		t.Fatal("lost pending", err)
	}
	if _, err = os.Stat(filepath.Join(f.root, target)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid upload wrote destination", err)
	}
	down, err := os.ReadFile("../platform/database/migrations/0096_product_delivery_upload.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(down)); err == nil {
		t.Fatal("migration removed upload evidence")
	}
	if _, err = svc.PrepareUpload(ctx, actor, f.order, "new-upload-revision", "test", repairCommand(2, nil)); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.PrepareUpload(ctx, actor, f.order, "upload-reservation-key", "test", repairCommand(1, nil)); !errors.Is(err, productdelivery.ErrRepairConflict) {
		t.Fatal("old command returned new reservation", err)
	}
	body := &countedRepairBody{}
	if _, err = svc.Upload(ctx, actor, f.order, repair, true, "test", body); !errors.Is(err, productdelivery.ErrRepairConflict) || body.reads != 0 {
		t.Fatal("superseded upload accepted", err)
	}
}

func TestProductDeliveryUploadSlotsAndStagingRechecks(t *testing.T) {
	for _, scenario := range []string{"capacity_cancel", "revoked", "deleted", "suspended"} {
		t.Run(scenario, func(t *testing.T) {
			pool, cleanup := paymentTestPool(t)
			defer cleanup()
			ctx := context.Background()
			f := newPurchasedReferenceFixture(t, pool, "video")
			actor := repairActor(t, pool)
			corruptDelivery(t, pool, f)
			svc := productdelivery.NewRepairService(pool, media.NewCatalog(media.NewLocalStore(f.root)))
			status, err := svc.PrepareUpload(ctx, actor, f.order, "gated-upload-reservation", "test", repairCommand(0, nil))
			if err != nil {
				t.Fatal(err)
			}
			repair := *status.PendingID
			var gates []*gatedRepairBody
			var results []chan error
			count := 1
			if scenario == "capacity_cancel" {
				count = 2
			}
			uploadCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			for range count {
				gate := &gatedRepairBody{Reader: bytes.NewReader(f.content), started: make(chan struct{}), release: make(chan struct{})}
				result := make(chan error, 1)
				gates = append(gates, gate)
				results = append(results, result)
				go func() { _, e := svc.Upload(uploadCtx, actor, f.order, repair, true, "test", gate); result <- e }()
				waitRepairGate(t, gate.started)
			}
			switch scenario {
			case "capacity_cancel":
				body := &countedRepairBody{}
				if _, err = svc.Upload(ctx, actor, f.order, repair, true, "test", body); !errors.Is(err, productdelivery.ErrRepairBusy) || body.reads != 0 {
					t.Fatal("capacity not bounded", err)
				}
				cancel()
			case "revoked":
				bounded, done := context.WithTimeout(ctx, 3*time.Second)
				defer done()
				_, err = pool.Exec(bounded, `UPDATE entitlements SET status='refunded',revoked_at=now() WHERE order_id=$1`, f.order)
				if err != nil {
					t.Fatal("staging held business locks", err)
				}
			case "deleted":
				deleteMarketplaceAccount(t, pool, f.root, f.buyer)
			case "suspended":
				if _, err = pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, actor); err != nil {
					t.Fatal(err)
				}
			}
			for _, gate := range gates {
				close(gate.release)
			}
			for _, result := range results {
				select {
				case err = <-result:
				case <-time.After(10 * time.Second):
					t.Fatal("upload did not finish")
				}
				want := productdelivery.ErrRepairConflict
				if scenario == "capacity_cancel" {
					want = context.Canceled
				}
				if scenario == "suspended" {
					want = productdelivery.ErrRepairForbidden
				}
				if !errors.Is(err, want) {
					t.Fatalf("staging recheck: %v", err)
				}
			}
			var target string
			if err = pool.QueryRow(ctx, `SELECT storage_key FROM product_delivery_repairs WHERE id=$1`, repair).Scan(&target); err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(filepath.Join(f.root, target)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("rejected upload created destination", err)
			}
			if scenario == "capacity_cancel" {
				if status, err = svc.Upload(ctx, actor, f.order, repair, true, "test", bytes.NewReader(f.content)); err != nil || status.Health != "healthy" {
					t.Fatal("slot leaked", err)
				}
			}
		})
	}
}

func TestProductDeliveryUploadLostReadyCommitResumesWithoutSource(t *testing.T) {
	pool, cleanup := paymentTestPool(t)
	defer cleanup()
	ctx := context.Background()
	f := newPurchasedReferenceFixture(t, pool, "video")
	actor := repairActor(t, pool)
	old := corruptDelivery(t, pool, f)
	if err := os.Remove(filepath.Join(f.root, old.SourceKey)); err != nil {
		t.Fatal(err)
	}
	svc := productdelivery.NewRepairService(pool, media.NewCatalog(media.NewLocalStore(f.root)))
	status, err := svc.PrepareUpload(ctx, actor, f.order, "upload-lost-ready-command", "test", repairCommand(0, nil))
	if err != nil {
		t.Fatal(err)
	}
	repair := *status.PendingID
	if _, err = pool.Exec(ctx, `CREATE FUNCTION reject_upload_ready() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.state='ready' THEN RAISE EXCEPTION 'injected ready commit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_upload_ready BEFORE UPDATE ON product_delivery_repairs FOR EACH ROW EXECUTE FUNCTION reject_upload_ready()`); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Upload(ctx, actor, f.order, repair, true, "test", bytes.NewReader(f.content)); err == nil {
		t.Fatal("injected commit failure ignored")
	}
	if _, err = pool.Exec(ctx, `DROP TRIGGER reject_upload_ready ON product_delivery_repairs`); err != nil {
		t.Fatal(err)
	}
	status, err = svc.Resume(ctx, actor, f.order, repair, "test")
	if err != nil || status.Health != "healthy" || status.PendingID != nil {
		t.Fatal("resume uploaded bytes", err)
	}
	assertPurchasedBytes(t, pool, f.root, f.buyer, f.purchased, f.content)
}
