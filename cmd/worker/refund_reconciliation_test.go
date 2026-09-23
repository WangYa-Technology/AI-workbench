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

type refundScanFunc func(context.Context, int) (int, error)

func (f refundScanFunc) ReconcileProductRefunds(ctx context.Context, n int) (int, error) {
	return f(ctx, n)
}

func TestRefundReconciliationStartsRetriesAndSanitizes(t *testing.T) {
	var calls atomic.Int32
	var logs bytes.Buffer
	done := make(chan struct{})
	scan := refundScanFunc(func(ctx context.Context, n int) (int, error) {
		if n != 100 {
			t.Errorf("limit=%d", n)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Error("missing budget")
		}
		if calls.Add(1) == 1 {
			return 0, errors.New("PRIVATE-PAYMENT-DETAIL")
		}
		if calls.Load() == 2 {
			close(done)
		}
		return 1, nil
	})
	stop := startRefundReconciliation(context.Background(), scan, slog.New(slog.NewTextHandler(&logs, nil)), time.Millisecond, time.Second, nil)
	defer stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("no retry")
	}
	stop()
	if strings.Contains(logs.String(), "PRIVATE-PAYMENT-DETAIL") || !strings.Contains(logs.String(), "product_refund_reconciliation_failed") {
		t.Fatal(logs.String())
	}
}

func TestRefundReconciliationStartupCancellation(t *testing.T) {
	entered := make(chan struct{})
	scan := refundScanFunc(func(ctx context.Context, _ int) (int, error) { close(entered); <-ctx.Done(); return 0, ctx.Err() })
	stop := startRefundReconciliation(context.Background(), scan, slog.Default(), time.Hour, time.Hour, nil)
	defer stop()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("waited for initial tick")
	}
	stopped := make(chan struct{})
	go func() { stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked")
	}
}
