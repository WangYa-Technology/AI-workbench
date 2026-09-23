package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/hcai-chat/hcai-chat/internal/payments"
	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
)

func (s *Server) getSellerPayoutBankTarget(w http.ResponseWriter, r *http.Request) {
	s.sellerPayoutBankTarget(w, r, "get")
}

func (s *Server) bindSellerPayoutBankTarget(w http.ResponseWriter, r *http.Request) {
	s.sellerPayoutBankTarget(w, r, "bind")
}

func (s *Server) listSellerPayoutBanks(w http.ResponseWriter, r *http.Request) {
	s.sellerPayoutBankTarget(w, r, "list")
}

func (s *Server) sellerPayoutBankTarget(w http.ResponseWriter, r *http.Request, action string) {
	w.Header().Set("Cache-Control", "private, no-store")
	actor, ok := s.listingUser(w, r, false)
	if !ok {
		return
	}
	id, ok := pathUUID(w, r, "requestID")
	if !ok {
		return
	}
	if len(r.URL.Query()) != 0 {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_seller_payout_bank", "This endpoint does not accept query filters.", false)
		return
	}
	var item any
	var err error
	if action == "bind" {
		var input struct {
			BankDestinationID string `json:"bankDestinationId"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		err = decoder.Decode(&input)
		var extra any
		if err != nil || decoder.Decode(&extra) != io.EOF {
			httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_seller_payout_bank", "Provide only the selected bank destination identifier.", false)
			return
		}
		item, err = s.payments.BindSellerPayoutBankTarget(r.Context(), actor, id, input.BankDestinationID)
	} else if action == "list" {
		item, err = s.payments.ListSellerPayoutBanks(r.Context(), actor, id)
	} else {
		item, err = s.payments.GetSellerPayoutBankTarget(r.Context(), actor, id)
	}
	switch {
	case errors.Is(err, payments.ErrSellerPayoutBankConflict), errors.Is(err, payments.ErrCheckoutReconciliation):
		httputil.WriteError(w, r, http.StatusConflict, "seller_payout_bank_conflict", "The selected bank or payout request requires review. An existing selection cannot be replaced.", false)
	case errors.Is(err, payments.ErrInvalidSellerPayout):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_seller_payout_bank", "Provide a valid bank destination identifier.", false)
	case errors.Is(err, payments.ErrProviderUnavailable), errors.Is(err, context.DeadlineExceeded):
		httputil.WriteError(w, r, http.StatusServiceUnavailable, "seller_payout_bank_unavailable", "Bank verification is temporarily unavailable.", true)
	case err != nil:
		var classified interface{ Retryable() bool }
		if errors.As(err, &classified) {
			httputil.WriteError(w, r, http.StatusServiceUnavailable, "seller_payout_bank_unavailable", "The provider could not verify this bank destination.", classified.Retryable())
			return
		}
		s.writeSellerFundsError(w, r, err)
	default:
		httputil.JSON(w, http.StatusOK, item)
	}
}
