package payments

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestProductDisputeSettlementTransition(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)
	past := now.Add(-time.Hour)
	tests := []struct {
		name       string
		action     string
		status     string
		available  time.Time
		otherOpen  bool
		wantStatus string
		wantReason string
	}{
		{"review reholds available", "requires_review", "available", past, false, "dispute_hold", "provider_dispute"},
		{"review preserves transfer recovery", "under_review", "transferred", past, false, "recovery_required", "provider_dispute"},
		{"won restores pending hold", "won", "dispute_hold", future, false, "pending_hold", "refund_window"},
		{"won restores available", "won", "dispute_hold", past, false, "available", ""},
		{"won does not release alongside another dispute", "won", "dispute_hold", past, true, "dispute_hold", ""},
		{"won does not reopen recovery", "won", "recovery_required", past, false, "recovery_required", ""},
		{"lost freezes unpaid settlement", "lost", "pending_hold", past, false, "refund_hold", "provider_dispute"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, reason := productDisputeSettlementTransition(tt.action, tt.status, tt.available, now, tt.otherOpen)
			if status != tt.wantStatus || reason != tt.wantReason {
				t.Fatalf("transition = (%q, %q), want (%q, %q)", status, reason, tt.wantStatus, tt.wantReason)
			}
		})
	}
}

func TestIsNewerDisputeEventUsesStableTieBreak(t *testing.T) {
	at := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	low := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	high := uuid.MustParse("00000000-0000-4000-8000-000000000002")
	if !isNewerDisputeEvent(at, high, at, low) {
		t.Fatal("higher event id should win an equal-time ordering")
	}
	if isNewerDisputeEvent(at, low, at, high) {
		t.Fatal("lower event id must not replace an equal-time ordering")
	}
	if !isNewerDisputeEvent(at.Add(time.Second), low, at, high) {
		t.Fatal("later event time should win regardless of event id")
	}
	if isNewerDisputeEvent(at.Add(-time.Second), high, at, low) {
		t.Fatal("late event must not replace the current projection")
	}
}
