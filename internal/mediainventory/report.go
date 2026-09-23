// Package mediainventory reconciles observed storage entries with database
// evidence. It is deliberately separate from cleanup and never deletes media.
package mediainventory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Summary struct {
	Kind         string `json:"kind"`
	Complete     bool   `json:"complete"`
	Entries      int    `json:"entries"`
	Referenced   int    `json:"referenced"`
	Unreferenced int    `json:"unreferencedInSnapshot"`
	AfterCleanup int    `json:"presentWithCleanupEvidence"`
	Unsupported  int    `json:"unsupportedEntries"`
}

type objectRecord struct {
	Kind string `json:"kind"`
	// Base64 preserves even non-UTF8 local names; never print private names to stdout.
	Key             []byte   `json:"keyBase64"`
	KeySHA256       string   `json:"keySha256"`
	EntryKind       string   `json:"entryKind"`
	Size            int64    `json:"size"`
	Classification  string   `json:"classification"`
	References      []string `json:"references"`
	CleanupEvidence []string `json:"cleanupEvidence"`
}

type evidence struct{ references, cleanup []string }

// Write publishes a complete private JSONL report without replacing an existing
// file. Errors leave no report at output. Crashes can leave a private staging
// file, which must not be interpreted as a completed inventory.
func Write(ctx context.Context, pool *pgxpool.Pool, inventory media.Inventory, output string, limit int) (Summary, error) {
	summary := Summary{Kind: "summary"}
	if limit < 1 || limit > 1_000_000 || output == "" {
		return summary, errors.New("invalid inventory options")
	}
	// Cap the lifetime of the MVCC snapshot, even for direct library callers.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	scope := inventory.InventoryScope()
	if scope.Backend != "local_file" && scope.Backend != "s3" {
		return summary, errors.New("unsupported inventory backend")
	}
	if scope.Backend == "local_file" {
		root, err := filepath.EvalSymlinks(scope.Location)
		if err != nil {
			return summary, err
		}
		parent, err := filepath.EvalSymlinks(filepath.Dir(output))
		if err != nil {
			return summary, err
		}
		root, err = filepath.Abs(root)
		if err != nil {
			return summary, err
		}
		parent, err = filepath.Abs(parent)
		if err != nil {
			return summary, err
		}
		relative, err := filepath.Rel(root, parent)
		if err != nil {
			return summary, err
		}
		if relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return summary, errors.New("inventory output must be outside the media root")
		}
	}
	if _, err := os.Lstat(output); err == nil {
		return summary, os.ErrExist
	} else if !errors.Is(err, os.ErrNotExist) {
		return summary, err
	}
	file, err := os.CreateTemp(filepath.Dir(output), ".media-inventory-*.jsonl.tmp")
	if err != nil {
		return summary, err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return summary, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	if _, err = tx.Exec(ctx, "SET LOCAL statement_timeout='15s'"); err != nil {
		return summary, err
	}
	// If RLS is introduced later, incomplete visibility must fail rather than
	// silently classify another account's private objects as unreferenced.
	if _, err = tx.Exec(ctx, "SET LOCAL row_security=off"); err != nil {
		return summary, err
	}
	var snapshotAt time.Time
	if err = tx.QueryRow(ctx, "SELECT transaction_timestamp()").Scan(&snapshotAt); err != nil {
		return summary, err
	}
	// Resolve every required relation even for an empty store. A missing schema
	// must not yield a misleading successful empty inventory.
	if _, err = referenceBatch(ctx, tx, scope.Backend, []string{}); err != nil {
		return summary, err
	}
	encoder := json.NewEncoder(file)
	if err = encoder.Encode(struct {
		Kind       string               `json:"kind"`
		Version    int                  `json:"version"`
		Scope      media.InventoryScope `json:"scope"`
		SnapshotAt time.Time            `json:"databaseSnapshotAt"`
		Limit      int                  `json:"entryLimit"`
		Boundary   string               `json:"boundary"`
	}{"inventory", 1, scope, snapshotAt.UTC(), limit,
		"Observed current objects versus one read-only database snapshot; not atomic across storage and database. No content verification, deletion authorization, backup/version/multipart inventory or missing-object check."}); err != nil {
		return summary, err
	}
	err = inventory.WalkInventory(ctx, limit, func(entries []media.InventoryEntry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(entries) == 0 || len(entries) > media.InventoryBatchSize || summary.Entries+len(entries) > limit {
			return media.ErrInventoryLimit
		}
		keys := make([]string, 0, len(entries))
		for _, entry := range entries {
			if entry.Kind == "object" {
				keys = append(keys, entry.Key)
			}
		}
		matches, err := referenceBatch(ctx, tx, scope.Backend, keys)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			match := matches[entry.Key]
			classification := "referenced"
			summary.Entries++
			switch {
			case entry.Kind != "object":
				classification = "unsupported_entry"
				summary.Unsupported++
			case len(match.cleanup) > 0:
				classification = "present_with_cleanup_evidence"
				summary.AfterCleanup++
			case len(match.references) == 0:
				classification = "unreferenced_in_snapshot"
				summary.Unreferenced++
			default:
				summary.Referenced++
			}
			digest := sha256.Sum256([]byte(entry.Key))
			if err := encoder.Encode(objectRecord{"object", []byte(entry.Key), hex.EncodeToString(digest[:]), entry.Kind,
				entry.Size, classification, match.references, match.cleanup}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return summary, fmt.Errorf("inventory incomplete: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return summary, err
	}
	summary.Complete = true
	if err = encoder.Encode(summary); err != nil {
		summary.Complete = false
		return summary, err
	}
	if err = file.Sync(); err != nil {
		summary.Complete = false
		return summary, err
	}
	if err = file.Close(); err != nil {
		summary.Complete = false
		return summary, err
	}
	if err = ctx.Err(); err != nil {
		summary.Complete = false
		return summary, err
	}
	// Link is atomic and fails even if an existing destination is a symlink.
	if err = os.Link(file.Name(), output); err != nil {
		summary.Complete = false
		return summary, err
	}
	return summary, nil
}

func referenceBatch(ctx context.Context, tx pgx.Tx, backend string, keys []string) (map[string]evidence, error) {
	result := make(map[string]evidence, len(keys))
	rows, err := tx.Query(ctx, `SELECT k.key,
 ARRAY_REMOVE(ARRAY[
 CASE WHEN EXISTS(SELECT 1 FROM assets WHERE storage_backend=$1 AND storage_key=k.key) THEN 'asset' END,
 CASE WHEN EXISTS(SELECT 1 FROM product_order_media_sources WHERE storage_backend=$1 AND storage_key=k.key) THEN 'contract_source' END,
 CASE WHEN EXISTS(SELECT 1 FROM product_delivery_snapshots WHERE source_backend=$1 AND source_key=k.key) THEN 'snapshot_source' END,
 CASE WHEN EXISTS(SELECT 1 FROM product_delivery_snapshots WHERE storage_backend=$1 AND storage_key=k.key) THEN 'snapshot_target' END,
 CASE WHEN EXISTS(SELECT 1 FROM product_delivery_repairs WHERE source_backend=$1 AND source_key=k.key) THEN 'repair_source' END,
 CASE WHEN EXISTS(SELECT 1 FROM product_delivery_repairs WHERE storage_backend=$1 AND storage_key=k.key) THEN 'repair_target' END,
 CASE WHEN EXISTS(SELECT 1 FROM upload_writes WHERE storage_backend=$1 AND storage_key=k.key) THEN 'upload_journal' END,
 CASE WHEN EXISTS(SELECT 1 FROM generation_output_writes WHERE storage_backend=$1 AND storage_key=k.key) THEN 'generation_journal' END
 ]::text[],NULL),
 ARRAY_REMOVE(ARRAY[
 CASE WHEN EXISTS(SELECT 1 FROM original_media_cleanup_receipts WHERE storage_backend=$1
 AND storage_key_sha256=encode(public.digest(k.key,'sha256'),'hex')) THEN 'original_cleanup_receipt' END,
 CASE WHEN EXISTS(SELECT 1 FROM product_delivery_snapshots WHERE storage_backend=$1 AND storage_key=k.key AND state='removed') THEN 'snapshot_removed' END,
 CASE WHEN EXISTS(SELECT 1 FROM product_delivery_repairs WHERE storage_backend=$1 AND storage_key=k.key AND state='removed') THEN 'repair_removed' END,
 CASE WHEN EXISTS(SELECT 1 FROM upload_writes WHERE storage_backend=$1 AND storage_key=k.key AND verified_absent_at IS NOT NULL) THEN 'upload_absence' END,
 CASE WHEN EXISTS(SELECT 1 FROM generation_output_writes WHERE storage_backend=$1 AND storage_key=k.key AND verified_absent_at IS NOT NULL) THEN 'generation_absence' END
 ]::text[],NULL)
 FROM unnest($2::text[]) AS k(key)`, backend, keys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var match evidence
		if err = rows.Scan(&key, &match.references, &match.cleanup); err != nil {
			return nil, err
		}
		result[key] = match
	}
	return result, rows.Err()
}
