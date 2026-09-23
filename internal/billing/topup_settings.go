package billing

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const MaximumTopupAmountCents = 99999999

var ErrTopupAmountOutOfRange = errors.New("wallet top-up amount is outside current settings")

type WalletTopupSettings struct {
	ID                 uuid.UUID `json:"-"`
	Version            int64     `json:"version"`
	Currency           string    `json:"currency"`
	MinimumAmountCents int       `json:"minimumAmountCents"`
	MaximumAmountCents int       `json:"maximumAmountCents"`
	PresetAmountsCents []int     `json:"presetAmountsCents"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

type TopupSettingsReader interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// lock pins the rule until the new payment intent is committed. Replays use
// their existing intent and never reprice or reject an already accepted amount.
func ReadWalletTopupSettings(ctx context.Context, db TopupSettingsReader, lock bool) (WalletTopupSettings, error) {
	query := `SELECT id,version,minimum_amount_cents,preset_amounts_cents,updated_at FROM wallet_topup_settings WHERE singleton=true`
	if lock {
		query += " FOR SHARE"
	}
	item := WalletTopupSettings{Currency: "USD", MaximumAmountCents: MaximumTopupAmountCents}
	err := db.QueryRow(ctx, query).Scan(&item.ID, &item.Version, &item.MinimumAmountCents, &item.PresetAmountsCents, &item.UpdatedAt)
	return item, err
}

func (s *Service) WalletTopupSettings(ctx context.Context) (WalletTopupSettings, error) {
	return ReadWalletTopupSettings(ctx, s.pool, false)
}

func ValidTopupSettings(minimum int, amounts []int) bool {
	if minimum < 50 || minimum > MaximumTopupAmountCents || amounts == nil || len(amounts) > 12 {
		return false
	}
	seen := make(map[int]bool, len(amounts))
	for _, amount := range amounts {
		if amount < minimum || amount > MaximumTopupAmountCents || seen[amount] {
			return false
		}
		seen[amount] = true
	}
	return true
}
