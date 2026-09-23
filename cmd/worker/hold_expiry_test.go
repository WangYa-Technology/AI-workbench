package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type expiryFunc func(context.Context, int) (int, error)

func (f expiryFunc) ReconcileOriginalMediaCleanups(context.Context, int) (int, error) { return 0, nil }

func (f expiryFunc) ReconcileDeletions(context.Context, int) (int, error) { return 0, nil }

func (f expiryFunc) ReconcileProductCleanups(context.Context, int) (int, error) { return 0, nil }

func (f expiryFunc) ResumeLegalHoldCleanups(context.Context, int) (int, error) { return 0, nil }

func (f expiryFunc) ExpireLegalHolds(ctx context.Context, limit int) (int, error) {
	return f(ctx, limit)
}

func TestHoldExpiryWorkerRetriesImmediatelyAndStops(t *testing.T) {
	var calls atomic.Int32
	var logs bytes.Buffer
	completed := make(chan struct{}, 1)
	service := expiryFunc(func(ctx context.Context, limit int) (int, error) {
		if limit != 100 {
			t.Errorf("limit=%d", limit)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Error("unbounded pass")
		}
		if calls.Add(1) == 1 {
			return 0, errors.New("PRIVATE-LEGAL-REFERENCE")
		}
		select {
		case completed <- struct{}{}:
		default:
		}
		return 1, nil
	})
	stop := startHoldExpiry(context.Background(), service, slog.New(slog.NewTextHandler(&logs, nil)), 10*time.Millisecond, time.Second, nil)
	defer stop()
	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("failed pass was not retried")
	}
	stop()
	if calls.Load() < 2 {
		t.Fatal("no retry")
	}
	if strings.Contains(logs.String(), "PRIVATE-LEGAL-REFERENCE") || !strings.Contains(logs.String(), "legal_hold_expiry_failed") {
		t.Fatal("unsafe or missing failure log", logs.String())
	}
}

func TestHoldExpiryWorkerStartsWithoutTickAndCancelsInFlight(t *testing.T) {
	entered := make(chan struct{})
	service := expiryFunc(func(ctx context.Context, _ int) (int, error) { close(entered); <-ctx.Done(); return 0, ctx.Err() })
	stop := startHoldExpiry(context.Background(), service, slog.Default(), time.Hour, time.Hour, nil)
	defer stop()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("startup waited for tick")
	}
	stopped := make(chan struct{})
	go func() { stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel pass")
	}
}

func TestHoldExpiryWorkerDeadlineRetries(t *testing.T) {
	var calls atomic.Int32
	retried := make(chan struct{})
	service := expiryFunc(func(ctx context.Context, _ int) (int, error) {
		if calls.Add(1) == 2 {
			close(retried)
		}
		<-ctx.Done()
		return 0, ctx.Err()
	})
	stop := startHoldExpiry(context.Background(), service, slog.Default(), time.Millisecond, 10*time.Millisecond, nil)
	defer stop()
	select {
	case <-retried:
	case <-time.After(time.Second):
		t.Fatal("deadline stopped future scans")
	}
}

type maintenanceFuncs struct{ expiry, cleanup, products, deletions, originals expiryFunc }

func (s maintenanceFuncs) ReconcileOriginalMediaCleanups(ctx context.Context, n int) (int, error) {
	if s.originals == nil {
		return 0, nil
	}
	return s.originals(ctx, n)
}

func (s maintenanceFuncs) ReconcileDeletions(ctx context.Context, n int) (int, error) {
	if s.deletions == nil {
		return 0, nil
	}
	return s.deletions(ctx, n)
}

func (s maintenanceFuncs) ReconcileProductCleanups(ctx context.Context, n int) (int, error) {
	if s.products == nil {
		return 0, nil
	}
	return s.products(ctx, n)
}

func (s maintenanceFuncs) ExpireLegalHolds(ctx context.Context, n int) (int, error) {
	return s.expiry(ctx, n)
}
func (s maintenanceFuncs) ResumeLegalHoldCleanups(ctx context.Context, n int) (int, error) {
	return s.cleanup(ctx, n)
}

func TestHoldCleanupWorkerHasIndependentBudgetAndSurvivesExpiryFailure(t *testing.T) {
	var calls atomic.Int32
	var logs bytes.Buffer
	done := make(chan struct{})
	service := maintenanceFuncs{
		expiry: func(ctx context.Context, _ int) (int, error) { <-ctx.Done(); return 0, ctx.Err() },
		cleanup: func(ctx context.Context, n int) (int, error) {
			if ctx.Err() != nil || n != 100 {
				t.Error("cleanup inherited exhausted expiry budget")
			}
			if calls.Add(1) == 2 {
				close(done)
			}
			return 0, errors.New("PRIVATE-CLEANUP-DETAIL")
		},
	}
	stop := startHoldExpiry(context.Background(), service, slog.New(slog.NewTextHandler(&logs, nil)), time.Millisecond, 10*time.Millisecond, nil)
	defer stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cleanup was starved")
	}
	stop()
	if strings.Contains(logs.String(), "PRIVATE-CLEANUP-DETAIL") || !strings.Contains(logs.String(), "legal_hold_cleanup_failed") {
		t.Fatal("unsafe cleanup logging", logs.String())
	}
}

func TestProductCleanupWorkerHasIndependentBudgetAndKeepsScanning(t *testing.T) {
	var calls atomic.Int32
	var logs bytes.Buffer
	done := make(chan struct{})
	service := maintenanceFuncs{
		expiry:  func(ctx context.Context, _ int) (int, error) { <-ctx.Done(); return 0, ctx.Err() },
		cleanup: func(ctx context.Context, _ int) (int, error) { <-ctx.Done(); return 0, ctx.Err() },
		products: func(ctx context.Context, n int) (int, error) {
			if ctx.Err() != nil || n != 100 {
				t.Error("product scan inherited exhausted budget")
			}
			if calls.Add(1) == 2 {
				close(done)
			}
			return 0, errors.New("PRIVATE-ORDER-PATH")
		},
	}
	stop := startHoldExpiry(context.Background(), service, slog.New(slog.NewTextHandler(&logs, nil)), time.Millisecond, 10*time.Millisecond, nil)
	defer stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("product scan starved")
	}
	stop()
	if strings.Contains(logs.String(), "PRIVATE-ORDER-PATH") || !strings.Contains(logs.String(), "product_cleanup_reconciliation_failed") {
		t.Fatal("unsafe logging", logs.String())
	}
}

func TestDeletionReconciliationWorkerHasIndependentBudget(t *testing.T) {
	var calls atomic.Int32
	var logs bytes.Buffer
	done := make(chan struct{})
	exhausted := expiryFunc(func(ctx context.Context, _ int) (int, error) { <-ctx.Done(); return 0, ctx.Err() })
	service := maintenanceFuncs{expiry: exhausted, cleanup: exhausted, products: exhausted, deletions: func(ctx context.Context, n int) (int, error) {
		if ctx.Err() != nil || n != 100 {
			t.Error("deletion reconciliation inherited exhausted budget")
		}
		if calls.Add(1) == 2 {
			close(done)
		}
		return 0, errors.New("PRIVATE-DELETION-REQUEST")
	}}
	stop := startHoldExpiry(context.Background(), service, slog.New(slog.NewTextHandler(&logs, nil)), time.Millisecond, 10*time.Millisecond, nil)
	defer stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("deletion reconciliation starved")
	}
	stop()
	if strings.Contains(logs.String(), "PRIVATE-DELETION-REQUEST") || !strings.Contains(logs.String(), "account_deletion_reconciliation_failed") {
		t.Fatal("unsafe or missing log", logs.String())
	}
}

func TestOriginalMediaCleanupWorkerHasIndependentBudget(t *testing.T) {
	var calls atomic.Int32
	var logs bytes.Buffer
	done := make(chan struct{})
	exhausted := expiryFunc(func(ctx context.Context, _ int) (int, error) { <-ctx.Done(); return 0, ctx.Err() })
	service := maintenanceFuncs{expiry: exhausted, cleanup: exhausted, products: exhausted, deletions: exhausted, originals: func(ctx context.Context, n int) (int, error) {
		if ctx.Err() != nil || n != 100 {
			t.Error("original media scan inherited exhausted budget")
		}
		if calls.Add(1) == 2 {
			close(done)
		}
		return 0, errors.New("PRIVATE-ORIGINAL-STORAGE-KEY")
	}}
	stop := startHoldExpiry(context.Background(), service, slog.New(slog.NewTextHandler(&logs, nil)), time.Millisecond, 10*time.Millisecond, nil)
	defer stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("original media scan starved")
	}
	stop()
	if strings.Contains(logs.String(), "PRIVATE-ORIGINAL-STORAGE-KEY") || !strings.Contains(logs.String(), "original_media_cleanup_reconciliation_failed") {
		t.Fatal("unsafe or missing log", logs.String())
	}
}
