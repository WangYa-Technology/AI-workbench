package admin

import (
	"context"
	"sort"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/billing"
	"github.com/jackc/pgx/v5"
)

type WalletTopupSettingsUpdate struct {
	ExpectedVersion    int64 `json:"expectedVersion"`
	MinimumAmountCents int   `json:"minimumAmountCents"`
	PresetAmountsCents []int `json:"presetAmountsCents"`
}

func (s *Service) UpdateWalletTopupSettings(ctx context.Context, actorID uuid.UUID, input WalletTopupSettingsUpdate, requestID string) (billing.WalletTopupSettings, error) {
	if input.ExpectedVersion < 1 || !billing.ValidTopupSettings(input.MinimumAmountCents, input.PresetAmountsCents) {
		return billing.WalletTopupSettings{}, ErrInvalid
	}
	// Do not mutate the caller's draft while sorting the persisted suggestions.
	amounts := append([]int{}, input.PresetAmountsCents...)
	sort.Ints(amounts)
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return billing.WalletTopupSettings{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := financeAuthorityTx(ctx, tx, actorID, false); err != nil {
		return billing.WalletTopupSettings{}, err
	}
	var currentVersion int64
	if err := tx.QueryRow(ctx, `SELECT version FROM wallet_topup_settings WHERE singleton=true FOR UPDATE`).Scan(&currentVersion); err != nil {
		return billing.WalletTopupSettings{}, financeCommandError(err)
	}
	if currentVersion != input.ExpectedVersion {
		return billing.WalletTopupSettings{}, ErrConflict
	}
	before, err := billing.ReadWalletTopupSettings(ctx, tx, false)
	if err != nil {
		return billing.WalletTopupSettings{}, err
	}
	if err := financeAuthorityTx(ctx, tx, actorID, true); err != nil {
		return billing.WalletTopupSettings{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE wallet_topup_settings SET minimum_amount_cents=$1,preset_amounts_cents=$2,version=version+1,updated_by=$3,updated_at=now() WHERE singleton=true`, input.MinimumAmountCents, amounts, actorID); err != nil {
		return billing.WalletTopupSettings{}, financeCommandError(err)
	}
	after, err := billing.ReadWalletTopupSettings(ctx, tx, false)
	if err != nil {
		return billing.WalletTopupSettings{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id,metadata) VALUES($1,'admin.wallet_topup_settings_updated','wallet_topup_settings',$2,'Updated wallet top-up minimum and suggestions',$3,$4)`, actorID, after.ID, requestID, map[string]any{"before": before, "after": after}); err != nil {
		return billing.WalletTopupSettings{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return billing.WalletTopupSettings{}, financeCommandError(err)
	}
	return after, nil
}
