package productpolicy

import (
	"strings"
	"testing"
	"time"
)

func TestRefundReasonUnicodeBoundaries(t *testing.T) {
	for _, c := range []struct {
		reason string
		valid  bool
	}{
		{strings.Repeat("界", 9), false}, {strings.Repeat("界", 10), true},
		{strings.Repeat("界", 500), true}, {strings.Repeat("界", 501), false},
		{strings.Repeat("😀", 9), false}, {strings.Repeat("😀", 10), true},
		{strings.Repeat("😀", 500), true}, {strings.Repeat("😀", 501), false},
		{" \t" + strings.Repeat("a", 10) + "\n", true},
		{strings.Repeat("a", 10) + "\x00", false}, {strings.Repeat("a", 10) + string([]byte{0xff}), false},
	} {
		if ValidRefundReason(c.reason) != c.valid {
			t.Errorf("unexpected validity for %q", c.reason)
		}
	}
}

func TestRefundDeadlineExclusiveAndTimezoneIndependent(t *testing.T) {
	created := time.Date(2026, 3, 7, 12, 0, 0, 0, time.FixedZone("offset", -5*60*60))
	deadline := created.Add(7 * 24 * time.Hour)
	for _, c := range []struct {
		now     time.Time
		allowed bool
	}{
		{deadline.Add(-time.Nanosecond), true}, {deadline, false}, {deadline.Add(time.Nanosecond), false},
	} {
		if RefundWindowOpen(created, 7, c.now) != c.allowed {
			t.Errorf("wrong window at %v", c.now)
		}
	}
	for _, days := range []int{-1, 0, 2147483647} {
		if RefundDeadline(created, days) != nil || RefundWindowOpen(created, days, created) {
			t.Fatalf("invalid window accepted: %d", days)
		}
	}
}
