package payments

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

func (s *Service) ListSellerPayoutBanks(ctx context.Context, sellerID, requestID uuid.UUID) (PayoutBankDirectory, error) {
	if s == nil || s.pool == nil || sellerID == uuid.Nil {
		return PayoutBankDirectory{}, ErrDisabled
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PayoutBankDirectory{}, err
	}
	defer tx.Rollback(context.Background())
	settlement, err := lockSellerPayoutBankRequest(ctx, tx, sellerID, requestID)
	if err != nil {
		return PayoutBankDirectory{}, err
	}
	if _, err := readSellerPayoutBankTarget(ctx, tx, sellerID, requestID); err == nil {
		return PayoutBankDirectory{}, ErrSellerPayoutBankConflict
	} else if !errors.Is(err, ErrSellerPayoutNotFound) {
		return PayoutBankDirectory{}, err
	}
	if !s.config.Enabled {
		return PayoutBankDirectory{}, ErrDisabled
	}
	state, err := sellerPayoutBankStateTx(ctx, tx, sellerID, requestID, settlement, "")
	if err != nil {
		return PayoutBankDirectory{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PayoutBankDirectory{}, err
	}
	runtime, err := s.runtimes.Runtime("stripe")
	if err != nil {
		return PayoutBankDirectory{}, err
	}
	lister, ok := runtime.(SellerPayoutBankLister)
	if !ok {
		return PayoutBankDirectory{}, ErrProviderUnavailable
	}
	started := time.Now()
	out, err := lister.ListPayoutBanks(ctx, state.Input)
	if err != nil {
		return PayoutBankDirectory{}, err
	}
	if err := ctx.Err(); err != nil {
		return PayoutBankDirectory{}, err
	}
	if out.Items == nil || len(out.Items) > 1000 || out.ObservedAt.Before(started) || out.ObservedAt.After(time.Now()) {
		return PayoutBankDirectory{}, ErrSellerPayoutBankConflict
	}
	seen := map[string]bool{}
	for _, bank := range out.Items {
		if !validPayoutBankOption(bank) || seen[bank.BankDestinationID] {
			return PayoutBankDirectory{}, ErrSellerPayoutBankConflict
		}
		seen[bank.BankDestinationID] = true
	}
	tx, err = s.pool.Begin(ctx)
	if err != nil {
		return PayoutBankDirectory{}, err
	}
	defer tx.Rollback(context.Background())
	if _, err := lockSellerPayoutBankRequest(ctx, tx, sellerID, requestID); err != nil {
		return PayoutBankDirectory{}, err
	}
	if _, err := readSellerPayoutBankTarget(ctx, tx, sellerID, requestID); err == nil {
		return PayoutBankDirectory{}, ErrSellerPayoutBankConflict
	} else if !errors.Is(err, ErrSellerPayoutNotFound) {
		return PayoutBankDirectory{}, err
	}
	current, err := sellerPayoutBankStateTx(ctx, tx, sellerID, requestID, settlement, "")
	if err != nil {
		return PayoutBankDirectory{}, err
	}
	if current.Input != state.Input || current.AmountCents != state.AmountCents {
		return PayoutBankDirectory{}, ErrSellerPayoutBankConflict
	}
	return out, nil
}
