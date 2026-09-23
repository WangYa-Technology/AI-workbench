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

	"github.com/hcai-chat/hcai-chat/internal/observability"
)

type maintenanceRecordFunc func(context.Context, string, bool) error

func (f maintenanceRecordFunc) RecordMaintenance(ctx context.Context, kind string, ok bool) error {
	return f(ctx, kind, ok)
}

func TestMaintenanceDeadlineRecordsFailureWithFreshBudget(t *testing.T) {
	var logs bytes.Buffer
	called := false
	recorder := maintenanceRecordFunc(func(ctx context.Context, kind string, ok bool) error {
		called = true
		deadline, bounded := ctx.Deadline()
		if ctx.Err() != nil || !bounded || time.Until(deadline) > 2*time.Second || kind != observability.LegalHoldExpiry || ok {
			t.Errorf("invalid observation context/kind/outcome: %v %s %v", ctx.Err(), kind, ok)
		}
		return nil
	})
	runMaintenancePass(context.Background(), maintenanceTask{observability.LegalHoldExpiry, func(ctx context.Context, _ int) (int, error) { <-ctx.Done(); return 0, nil }}, recorder, slog.New(slog.NewTextHandler(&logs, nil)), time.Millisecond)
	if !called || !strings.Contains(logs.String(), "legal_hold_expiry_failed") {
		t.Fatal("deadline was reported as success", logs.String())
	}
}

func TestMaintenanceRecordingFailureDoesNotStarveScans(t *testing.T) {
	var logs bytes.Buffer
	observed := make(chan string, 20)
	recorder := maintenanceRecordFunc(func(ctx context.Context, kind string, ok bool) error {
		if !ok || ctx.Err() != nil {
			t.Error("successful pass lost")
		}
		observed <- kind
		return errors.New("PRIVATE-OBSERVATION-SQL")
	})
	nop := expiryFunc(func(context.Context, int) (int, error) { return 0, nil })
	service := maintenanceFuncs{expiry: nop, cleanup: nop, products: nop, deletions: nop, originals: nop}
	stop := startHoldExpiry(context.Background(), service, slog.New(slog.NewTextHandler(&logs, nil)), time.Hour, time.Second, recorder)
	defer stop()
	for _, want := range []string{observability.LegalHoldExpiry, observability.LegalHoldCleanup, observability.ProductCleanupReconciliation, observability.AccountDeletionReconciliation, observability.OriginalMediaCleanupReconciliation} {
		select {
		case got := <-observed:
			if got != want {
				t.Fatalf("got %s want %s", got, want)
			}
		case <-time.After(time.Second):
			t.Fatal("observation failure starved next scan")
		}
	}
	stop()
	if strings.Contains(logs.String(), "PRIVATE-") || strings.Count(logs.String(), "maintenance_observation_failed") != 5 {
		t.Fatal(logs.String())
	}
}

func TestMaintenanceShutdownCancelsRecording(t *testing.T) {
	entered := make(chan struct{})
	recorder := maintenanceRecordFunc(func(ctx context.Context, kind string, ok bool) error {
		if kind != observability.ProductRefundReconciliation || !ok {
			t.Error("wrong refund observation")
		}
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	})
	stop := startRefundReconciliation(context.Background(), refundScanFunc(func(context.Context, int) (int, error) { return 0, nil }), slog.Default(), time.Hour, time.Second, recorder)
	defer stop()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("recording did not start")
	}
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown stuck in recorder")
	}
}

func TestMaintenanceCancelledPassIsNotRecordedAsSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	runMaintenancePass(ctx, maintenanceTask{observability.LegalHoldExpiry, func(context.Context, int) (int, error) { cancel(); return 0, nil }}, maintenanceRecordFunc(func(context.Context, string, bool) error { calls.Add(1); return nil }), slog.Default(), time.Second)
	if calls.Load() != 0 {
		t.Fatal("shutdown fabricated completion")
	}
}
