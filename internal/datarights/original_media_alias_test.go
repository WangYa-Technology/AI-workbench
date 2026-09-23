//go:build unix

package datarights_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
)

func TestOriginalMediaAliasCannotProduceCleanupReceipt(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			pool, cleanup := dataRightsTestPool(t)
			defer cleanup()
			ctx := context.Background()
			f := completedOriginalFixture(t, pool)
			path := filepath.Join(f.root, f.key)
			backup := filepath.Join(f.root, "separate-original.txt")
			if kind == "symlink" {
				if err := os.Rename(path, backup); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(backup, path); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Link(path, backup); err != nil {
				t.Fatal(err)
			}
			if n, err := f.service.ReconcileOriginalMediaCleanups(ctx, 100); err != nil || n != 1 {
				t.Fatal("cleanup not queued", n, err)
			}
			repo := jobs.NewRepository(pool)
			job, err := repo.Claim(ctx, "alias-cleanup", time.Minute)
			if err != nil || job.ID != originalJob(t, pool, f.owner).ID {
				t.Fatal("wrong job", err)
			}
			cause := f.service.HandleMediaCleanupJob(ctx, job)
			if !errors.Is(cause, media.ErrIntegrity) {
				t.Fatal("unsafe cleanup did not stop", cause)
			}
			if err := repo.Fail(ctx, job, "alias-cleanup", cause); err != nil {
				t.Fatal(err)
			}
			var receipts, audits int
			if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM original_media_cleanup_receipts WHERE owner_id=$1),
 (SELECT count(*) FROM audit_events WHERE action='data_rights.original_media_cleanup_verified' AND metadata->>'ownerId'=$1::text)`, f.owner).Scan(&receipts, &audits); err != nil || receipts != 0 || audits != 0 {
				t.Fatal("unsafe cleanup forged physical completion", receipts, audits, err)
			}
			for _, name := range []string{path, backup} {
				body, err := os.ReadFile(name)
				if err != nil || string(body) != "historical retained bytes" {
					t.Fatal("cleanup changed retained source", err)
				}
			}
			// Simulate an operator resolving the filesystem fault, without
			// replacing the owner's request or erasing the failed job attempt.
			if kind == "symlink" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(backup, path); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Remove(backup); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE jobs SET available_at=now() WHERE id=$1 AND status='queued'`, job.ID); err != nil {
				t.Fatal(err)
			}
			retry, err := repo.Claim(ctx, "alias-cleanup-retry", time.Minute)
			if err != nil || retry.ID != job.ID {
				t.Fatal("original cleanup retry missing", err)
			}
			if err := f.service.HandleMediaCleanupJob(ctx, retry); err != nil {
				t.Fatal(err)
			}
			if err := repo.Complete(ctx, retry, "alias-cleanup-retry"); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM original_media_cleanup_receipts WHERE owner_id=$1`, f.owner).Scan(&receipts); err != nil || receipts != 1 {
				t.Fatal("resolved cleanup lacks receipt", receipts, err)
			}
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("cleanup did not remove resolved file", err)
			}
		})
	}
}
