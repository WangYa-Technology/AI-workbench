// Package productpolicy holds rules shared by current and historical product orders.
package productpolicy

import (
	"strings"
	"time"
	"unicode/utf8"
)

// ValidRefundReason counts Unicode code points, as the public API specifies.
func ValidRefundReason(reason string) bool {
	reason = strings.TrimSpace(reason)
	length := utf8.RuneCountInString(reason)
	return utf8.ValidString(reason) && !strings.ContainsRune(reason, '\x00') && length >= 10 && length <= 500
}

// RefundDeadline preserves the accepted order's window from order creation.
// Calendar arithmetic avoids duration overflow on historical database values.
func RefundDeadline(createdAt time.Time, days int) *time.Time {
	if createdAt.IsZero() || days <= 0 {
		return nil
	}
	deadline := createdAt.UTC().AddDate(0, 0, days)
	if deadline.Year() > 9999 {
		return nil
	}
	return &deadline
}

func RefundWindowOpen(createdAt time.Time, days int, now time.Time) bool {
	deadline := RefundDeadline(createdAt, days)
	return deadline != nil && now.Before(*deadline)
}
