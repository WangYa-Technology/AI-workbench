package payments

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrPayoutNotFound = errors.New("payout destination not found")
	ErrPayoutConflict = errors.New("payout destination state conflict")
)

type PayoutStatus struct {
	ProviderAvailable     bool       `json:"providerAvailable"`
	Provider              string     `json:"provider"`
	LiveMode              bool       `json:"liveMode"`
	Status                string     `json:"status"`
	DestinationID         *string    `json:"destinationId,omitempty"`
	AccountType           *string    `json:"accountType,omitempty"`
	ChargesEnabled        bool       `json:"chargesEnabled"`
	PayoutsEnabled        bool       `json:"payoutsEnabled"`
	DetailsSubmitted      bool       `json:"detailsSubmitted"`
	RequirementsDue       bool       `json:"requirementsDue"`
	CanStartOnboarding    bool       `json:"canStartOnboarding"`
	Version               int        `json:"version"`
	VerifiedAt            *time.Time `json:"verifiedAt,omitempty"`
	OnboardingStartedAt   *time.Time `json:"onboardingStartedAt,omitempty"`
	OnboardingCompletedAt *time.Time `json:"onboardingCompletedAt,omitempty"`
	UpdatedAt             *time.Time `json:"updatedAt,omitempty"`
}

type PayoutOnboardingLink struct {
	URL       string       `json:"url"`
	ExpiresAt time.Time    `json:"expiresAt"`
	Status    PayoutStatus `json:"status"`
}

func (s *Service) GetPayoutStatus(ctx context.Context, userID uuid.UUID) (PayoutStatus, error) {
	if s == nil || s.pool == nil || userID == uuid.Nil {
		return PayoutStatus{}, ErrPayoutNotFound
	}
	item := PayoutStatus{ProviderAvailable: s.config.Enabled, Provider: "stripe", LiveMode: s.config.LiveMode, Status: "not_started", CanStartOnboarding: s.config.Enabled}
	var destinationID, accountType string
	var updatedAt time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT destination_id,account_type,status,charges_enabled,payouts_enabled,details_submitted,requirements_due,
		       version,verified_at,onboarding_started_at,onboarding_completed_at,updated_at
		FROM payment_destinations WHERE provider='stripe' AND user_id=$1`, userID).Scan(
		&destinationID, &accountType, &item.Status, &item.ChargesEnabled, &item.PayoutsEnabled, &item.DetailsSubmitted, &item.RequirementsDue,
		&item.Version, &item.VerifiedAt, &item.OnboardingStartedAt, &item.OnboardingCompletedAt, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, nil
	}
	if err != nil {
		return PayoutStatus{}, err
	}
	item.DestinationID = &destinationID
	item.AccountType = &accountType
	item.UpdatedAt = &updatedAt
	item.CanStartOnboarding = s.config.Enabled && item.Status != "verified" && item.Status != "disabled"
	return item, nil
}

func (s *Service) BeginPayoutOnboarding(ctx context.Context, userID uuid.UUID, refreshURL, returnURL, requestID string) (PayoutOnboardingLink, error) {
	if s == nil || s.pool == nil || !s.config.Enabled || userID == uuid.Nil {
		return PayoutOnboardingLink{}, ErrDisabled
	}
	runtime, err := s.runtimes.Runtime("stripe")
	if err != nil {
		return PayoutOnboardingLink{}, err
	}
	status, err := s.GetPayoutStatus(ctx, userID)
	if err != nil {
		return PayoutOnboardingLink{}, err
	}
	if status.Status == "verified" || status.Status == "disabled" {
		return PayoutOnboardingLink{}, ErrPayoutConflict
	}
	if status.DestinationID == nil {
		var email string
		if err := s.pool.QueryRow(ctx, `SELECT email FROM users WHERE id=$1 AND status='active'`, userID).Scan(&email); errors.Is(err, pgx.ErrNoRows) {
			return PayoutOnboardingLink{}, ErrPayoutNotFound
		} else if err != nil {
			return PayoutOnboardingLink{}, err
		}
		account, err := runtime.CreateConnectAccount(ctx, ConnectAccountRequest{UserID: userID, Email: email})
		if err != nil {
			return PayoutOnboardingLink{}, SanitizeProviderError(err)
		}
		if err := s.persistConnectAccount(ctx, userID, account, requestID); err != nil {
			return PayoutOnboardingLink{}, err
		}
		status, err = s.GetPayoutStatus(ctx, userID)
		if err != nil {
			return PayoutOnboardingLink{}, err
		}
	}
	link, err := runtime.CreateAccountLink(ctx, AccountLinkRequest{DestinationID: *status.DestinationID, RefreshURL: refreshURL, ReturnURL: returnURL})
	if err != nil {
		return PayoutOnboardingLink{}, SanitizeProviderError(err)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return PayoutOnboardingLink{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var destinationRecordID uuid.UUID
	if err := tx.QueryRow(ctx, `
		UPDATE payment_destinations SET onboarding_started_at=COALESCE(onboarding_started_at,now()),version=version+1,updated_at=now()
		WHERE provider='stripe' AND user_id=$1 AND destination_id=$2 AND status NOT IN ('verified','disabled')
		RETURNING id`, userID, *status.DestinationID).Scan(&destinationRecordID); errors.Is(err, pgx.ErrNoRows) {
		return PayoutOnboardingLink{}, ErrPayoutConflict
	} else if err != nil {
		return PayoutOnboardingLink{}, err
	}
	if err := insertPayoutAudit(ctx, tx, userID, "payment.payout_onboarding_link_created", destinationRecordID, requestID, map[string]any{
		"provider": "stripe", "liveMode": s.config.LiveMode, "expiresAt": link.ExpiresAt,
	}); err != nil {
		return PayoutOnboardingLink{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PayoutOnboardingLink{}, err
	}
	status, err = s.GetPayoutStatus(ctx, userID)
	if err != nil {
		return PayoutOnboardingLink{}, err
	}
	return PayoutOnboardingLink{URL: link.URL, ExpiresAt: link.ExpiresAt, Status: status}, nil
}

func (s *Service) persistConnectAccount(ctx context.Context, userID uuid.UUID, account ConnectAccount, requestID string) error {
	status := payoutStatusFromEvidence(account.ChargesEnabled, account.PayoutsEnabled, account.DetailsSubmitted, account.RequirementsDue, false)
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var destinationRecordID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO payment_destinations(
		  provider,user_id,destination_id,account_type,status,charges_enabled,payouts_enabled,details_submitted,requirements_due,
		  verified_at,onboarding_completed_at
		) VALUES('stripe',$1,$2,'express',$3,$4,$5,$6,$7,
		  CASE WHEN $3='verified' THEN now() END,CASE WHEN $6 THEN now() END)
		ON CONFLICT(provider,user_id) DO NOTHING RETURNING id`,
		userID, account.ID, status, account.ChargesEnabled, account.PayoutsEnabled, account.DetailsSubmitted, account.RequirementsDue).Scan(&destinationRecordID)
	if errors.Is(err, pgx.ErrNoRows) {
		var existingDestinationID string
		if err := tx.QueryRow(ctx, `SELECT id,destination_id FROM payment_destinations WHERE provider='stripe' AND user_id=$1 FOR UPDATE`, userID).Scan(&destinationRecordID, &existingDestinationID); err != nil {
			return err
		}
		if existingDestinationID != account.ID {
			return ErrPayoutConflict
		}
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	if err := insertPayoutAudit(ctx, tx, userID, "payment.payout_destination_created", destinationRecordID, requestID, map[string]any{
		"provider": "stripe", "accountType": "express", "status": status, "liveMode": account.LiveMode,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func payoutStatusFromEvidence(chargesEnabled, payoutsEnabled, detailsSubmitted, requirementsDue, adminDisabled bool) string {
	if adminDisabled {
		return "disabled"
	}
	if !detailsSubmitted {
		return "pending_onboarding"
	}
	if chargesEnabled && payoutsEnabled && !requirementsDue {
		return "verified"
	}
	if requirementsDue {
		return "restricted"
	}
	return "pending_verification"
}

func insertPayoutAudit(ctx context.Context, tx pgx.Tx, actorID uuid.UUID, action string, resourceID uuid.UUID, requestID string, metadata map[string]any) error {
	body, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" || len(requestID) > 160 {
		requestID = "payout-onboarding"
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO audit_events(actor_id,action,resource_type,resource_id,request_id,metadata)
		VALUES($1,$2,'payment_destination',$3,$4,$5)`, actorID, action, resourceID, requestID, body)
	return err
}
