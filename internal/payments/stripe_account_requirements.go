package payments

import "strings"

// Stripe may omit or return null for requirements. Absence is not evidence
// that an account has no outstanding verification work.
type stripeAccountRequirements struct {
	CurrentlyDue        []string `json:"currently_due"`
	PastDue             []string `json:"past_due"`
	PendingVerification []string `json:"pending_verification"`
	DisabledReason      string   `json:"disabled_reason"`
}

func (r *stripeAccountRequirements) unresolved() bool {
	return r == nil || r.CurrentlyDue == nil || r.PastDue == nil || r.PendingVerification == nil ||
		len(r.CurrentlyDue) > 0 || len(r.PastDue) > 0 || len(r.PendingVerification) > 0 || strings.TrimSpace(r.DisabledReason) != ""
}
