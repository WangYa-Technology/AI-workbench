package mediainventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/database"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// All writes and migrations below are restricted to an isolated schema.
func inventoryPool(t *testing.T, migrate bool) *pgxpool.Pool {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		base = "postgres://hcai:hcai@localhost:5432/hcai?sslmode=disable"
	}
	root, err := pgxpool.New(t.Context(), base)
	if err != nil {
		testutil.DatabaseUnavailable(t, err)
	}
	if err = root.Ping(t.Context()); err != nil {
		root.Close()
		testutil.DatabaseUnavailable(t, err)
	}
	schema := "test_media_inventory_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = root.Exec(t.Context(), "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		root.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := root.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Error(err)
		}
		root.Close()
	})
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	pool, err := database.Open(t.Context(), u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if migrate {
		if err = database.Migrate(t.Context(), pool); err != nil {
			t.Fatal(err)
		}
	} else {
		// Projection fixtures exercise reference combinations without fabricating
		// valid historical contracts or bypassing real production triggers.
		_, err = pool.Exec(t.Context(), `CREATE TABLE assets(storage_backend text,storage_key text);
CREATE TABLE product_order_media_sources(storage_backend text,storage_key text);
CREATE TABLE product_delivery_snapshots(source_backend text,source_key text,storage_backend text,storage_key text,state text);
CREATE TABLE product_delivery_repairs(LIKE product_delivery_snapshots);
CREATE TABLE upload_writes(storage_backend text,storage_key text,verified_absent_at timestamptz);
CREATE TABLE generation_output_writes(LIKE upload_writes);
CREATE TABLE original_media_cleanup_receipts(storage_backend text,storage_key_sha256 text);`)
		if err != nil {
			t.Fatal(err)
		}
	}
	return pool
}

type inventoryFixture struct {
	root string
	walk func(context.Context, int, func([]media.InventoryEntry) error) error
}

func (f inventoryFixture) InventoryScope() media.InventoryScope {
	return media.InventoryScope{Backend: "local_file", Location: f.root}
}
func (f inventoryFixture) WalkInventory(ctx context.Context, n int, visit func([]media.InventoryEntry) error) error {
	return f.walk(ctx, n, visit)
}

func records(t *testing.T, path string) []objectRecord {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	var header map[string]any
	if err := decoder.Decode(&header); err != nil || header["kind"] != "inventory" || header["boundary"] == "" {
		t.Fatal(header, err)
	}
	var out []objectRecord
	for decoder.More() {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			t.Fatal(err)
		}
		var record objectRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			t.Fatal(err)
		}
		if record.Kind == "summary" {
			var summary Summary
			if err := json.Unmarshal(raw, &summary); err != nil || !summary.Complete || summary.Entries != len(out) {
				t.Fatal(summary, err)
			}
			return out
		}
		out = append(out, record)
	}
	t.Fatal("missing completion record")
	return nil
}

func TestInventoryReferenceClassification(t *testing.T) {
	pool := inventoryPool(t, false)
	_, err := pool.Exec(t.Context(), `INSERT INTO assets VALUES('local_file','asset'),('s3','foreign'),('local_file','receipt');
INSERT INTO product_order_media_sources VALUES('local_file','contract');
INSERT INTO product_delivery_snapshots VALUES('local_file','snapshot-source','local_file','snapshot-target','ready'),
 (NULL,NULL,'local_file','removed-snapshot','removed');
INSERT INTO product_delivery_repairs VALUES('local_file','repair-source','local_file','repair-target','prepared'),
 (NULL,NULL,'local_file','removed-repair','removed');
INSERT INTO upload_writes VALUES('local_file','upload',NULL),('local_file','cleaned-upload',now());
INSERT INTO generation_output_writes VALUES('local_file','generation',NULL),('local_file','cleaned-generation',now());
INSERT INTO original_media_cleanup_receipts VALUES('local_file',encode(public.digest('receipt','sha256'),'hex'));`)
	if err != nil {
		t.Fatal(err)
	}
	wants := map[string]string{"asset": "asset", "contract": "contract_source", "snapshot-source": "snapshot_source", "snapshot-target": "snapshot_target",
		"repair-source": "repair_source", "repair-target": "repair_target", "upload": "upload_journal", "generation": "generation_journal",
		"removed-snapshot": "snapshot_removed", "removed-repair": "repair_removed", "cleaned-upload": "upload_absence", "cleaned-generation": "generation_absence", "receipt": "original_cleanup_receipt",
		"unknown": "", "foreign": ""}
	root := t.TempDir()
	for key := range wants {
		if err := os.WriteFile(filepath.Join(root, key), []byte("evidence"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	report := filepath.Join(t.TempDir(), "report.jsonl")
	summary, err := Write(t.Context(), pool, media.NewLocalStore(root), report, 100)
	if err != nil || !summary.Complete || summary.Entries != 15 || summary.Referenced != 8 || summary.AfterCleanup != 5 || summary.Unreferenced != 2 {
		t.Fatal(summary, err)
	}
	info, err := os.Stat(report)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
	for _, record := range records(t, report) {
		key := string(record.Key)
		want := wants[key]
		all := append(append([]string{}, record.References...), record.CleanupEvidence...)
		if want != "" && !strings.Contains(strings.Join(all, ","), want) {
			t.Fatal(key, record)
		}
		if want == "" && record.Classification != "unreferenced_in_snapshot" {
			t.Fatal(record)
		}
		if key == "receipt" && (record.Classification != "present_with_cleanup_evidence" || len(record.References) != 1) {
			t.Fatal("cleanup alert suppressed by active reference", record)
		}
	}
}

func TestInventoryKeepsDatabaseSnapshotDuringConcurrentWrite(t *testing.T) {
	pool := inventoryPool(t, false)
	report := filepath.Join(t.TempDir(), "snapshot.jsonl")
	inventory := inventoryFixture{root: t.TempDir(), walk: func(ctx context.Context, _ int, visit func([]media.InventoryEntry) error) error {
		if _, err := pool.Exec(ctx, "INSERT INTO assets VALUES('local_file','later')"); err != nil {
			return err
		}
		return visit([]media.InventoryEntry{{Key: "later", Kind: "object", Size: 1}})
	}}
	summary, err := Write(t.Context(), pool, inventory, report, 10)
	if err != nil || summary.Unreferenced != 1 {
		t.Fatal(summary, err)
	}
	// A new run sees the committed reference, proving the first did not silently
	// switch to a later database view while enumerating storage.
	inventory.walk = func(_ context.Context, _ int, visit func([]media.InventoryEntry) error) error {
		return visit([]media.InventoryEntry{{Key: "later", Kind: "object", Size: 1}})
	}
	summary, err = Write(t.Context(), pool, inventory, filepath.Join(t.TempDir(), "next.jsonl"), 10)
	if err != nil || summary.Referenced != 1 {
		t.Fatal(summary, err)
	}
}

func TestInventoryPublicationFailuresAndUnsupportedNames(t *testing.T) {
	pool := inventoryPool(t, false)
	for _, mode := range []string{"failure", "limit", "cancel", "exists", "symlink", "publish_race", "unsupported"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			output := filepath.Join(dir, "report.jsonl")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "exists" {
				if err := os.WriteFile(output, []byte("original"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "symlink" {
				if err := os.Symlink(filepath.Join(dir, "absent"), output); err != nil {
					t.Fatal(err)
				}
			}
			inventory := inventoryFixture{root: t.TempDir(), walk: func(_ context.Context, _ int, visit func([]media.InventoryEntry) error) error {
				key := "private"
				kind := "object"
				if mode == "unsupported" {
					key = string([]byte{0xff, 'x'})
					kind = "invalid_key"
				}
				if err := visit([]media.InventoryEntry{{Key: key, Kind: kind, Size: 1}}); err != nil {
					return err
				}
				switch mode {
				case "failure":
					return errors.New("storage failure")
				case "limit":
					return media.ErrInventoryLimit
				case "cancel":
					cancel()
				case "publish_race":
					return os.WriteFile(output, []byte("original"), 0600)
				}
				return nil
			}}
			summary, err := Write(ctx, pool, inventory, output, 100)
			if mode == "unsupported" {
				if err != nil || summary.Unsupported != 1 {
					t.Fatal(summary, err)
				}
				if got := records(t, output); string(got[0].Key) != string([]byte{0xff, 'x'}) {
					t.Fatal("raw name lost", got)
				}
			} else {
				if err == nil || summary.Complete {
					t.Fatal("false complete report", summary, err)
				}
				if mode == "exists" || mode == "publish_race" {
					body, _ := os.ReadFile(output)
					if string(body) != "original" {
						t.Fatal("overwrote report")
					}
				} else if mode == "symlink" {
					if _, err := os.Readlink(output); err != nil {
						t.Fatal(err)
					}
				} else if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("published incomplete report", err)
				}
			}
			files, err := filepath.Glob(filepath.Join(dir, ".media-inventory-*"))
			if err != nil || len(files) != 0 {
				t.Fatal("staging leak", files, err)
			}
		})
	}
}

func TestInventoryCurrentSchemaCompatibilityAndMissingSchema(t *testing.T) {
	pool := inventoryPool(t, true)
	output := filepath.Join(t.TempDir(), "empty.jsonl")
	summary, err := Write(t.Context(), pool, media.NewLocalStore(t.TempDir()), output, 10)
	if err != nil || !summary.Complete || summary.Entries != 0 {
		t.Fatal(summary, err)
	}
	// Parse and execute the nonempty lookup against the actual current schema.
	tx, err := pool.BeginTx(t.Context(), pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err = referenceBatch(t.Context(), tx, "local_file", []string{"nonexistent"}); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), "CREATE TABLE must_not_write(id int)"); err == nil {
		t.Fatal("transaction was writable")
	}
	if _, err := pool.Exec(t.Context(), "ALTER TABLE upload_writes RENAME TO missing_upload_writes"); err != nil {
		t.Fatal(err)
	}
	output = filepath.Join(t.TempDir(), "missing-schema.jsonl")
	summary, err = Write(t.Context(), pool, media.NewLocalStore(t.TempDir()), output, 10)
	if err == nil || summary.Complete {
		t.Fatal("missing schema accepted", summary, err)
	}
}

func TestInventoryBatchesLargeDirectory(t *testing.T) {
	pool := inventoryPool(t, false)
	root := t.TempDir()
	for i := 0; i < 205; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("file-%03d", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	summary, err := Write(t.Context(), pool, media.NewLocalStore(root), filepath.Join(t.TempDir(), "all.jsonl"), 205)
	if err != nil || summary.Unreferenced != 205 {
		t.Fatal(summary, err)
	}
}

func TestInventoryRejectsOutputInsideScannedRoot(t *testing.T) {
	root := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{root, alias, child} {
		_, err := Write(t.Context(), nil, media.NewLocalStore(root), filepath.Join(dir, "report.jsonl"), 10)
		if err == nil || !strings.Contains(err.Error(), "outside") {
			t.Fatal(err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.Contains(entry.Name(), "inventory") || entry.Name() == "report.jsonl" {
				t.Fatal("report polluted scanned root")
			}
		}
	}
}
