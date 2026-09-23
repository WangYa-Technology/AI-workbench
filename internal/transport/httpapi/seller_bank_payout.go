package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/hcai-chat/hcai-chat/internal/payments"
	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
)

func (s *Server) adminSellerBankPayout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	actor, ok := s.requirePermission(w, r, "admin:finance")
	if !ok {
		return
	}
	id, ok := pathUUID(w, r, "requestID")
	if !ok {
		return
	}
	if len(r.URL.Query()) != 0 {
		s.writeSellerBankPayout(w, r, nil, payments.ErrSellerBankPayoutInvalid)
		return
	}
	if r.Method == http.MethodGet {
		item, err := s.payments.GetSellerBankPayoutOperation(r.Context(), actor.ID, id)
		s.writeSellerBankPayout(w, r, item, err)
		return
	}
	if len(r.Header.Values("Idempotency-Key")) != 1 {
		s.writeSellerBankPayout(w, r, nil, payments.ErrSellerBankPayoutInvalid)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var input payments.SellerBankPayoutInput
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF {
		s.writeSellerBankPayout(w, r, nil, payments.ErrSellerBankPayoutInvalid)
		return
	}
	item, err := s.payments.SubmitSellerBankPayout(r.Context(), actor.ID, id, input, idempotencyKey(r), httputil.RequestID(r.Context()))
	s.writeSellerBankPayout(w, r, item, err)
}

func (s *Server) adminResumeSellerBankPayout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	actor, ok := s.requirePermission(w, r, "admin:finance")
	if !ok {
		return
	}
	id, ok := pathUUID(w, r, "requestID")
	if !ok {
		return
	}
	if len(r.URL.Query()) != 0 || len(r.Header.Values("Idempotency-Key")) != 1 {
		s.writeSellerBankPayout(w, r, nil, payments.ErrSellerBankPayoutInvalid)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var input payments.SellerBankPayoutResumeInput
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF {
		s.writeSellerBankPayout(w, r, nil, payments.ErrSellerBankPayoutInvalid)
		return
	}
	item, err := s.payments.ResumeSellerBankPayout(r.Context(), actor.ID, id, input, idempotencyKey(r), httputil.RequestID(r.Context()))
	s.writeSellerBankPayout(w, r, item, err)
}

func (s *Server) writeSellerBankPayout(w http.ResponseWriter, r *http.Request, item any, err error) {
	switch {
	case errors.Is(err, payments.ErrSellerBankPayoutInvalid):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_seller_bank_payout", "Confirm the source transfer, approved revision, amount, bound bank and payout reason.", false)
	case errors.Is(err, payments.ErrSellerBankPayoutConflict), errors.Is(err, payments.ErrSellerPayoutBankConflict), errors.Is(err, payments.ErrCheckoutReconciliation):
		httputil.WriteError(w, r, http.StatusConflict, "seller_bank_payout_conflict", "The source, approval, bank or funds changed. Refresh before authorizing a bank payout.", false)
	default:
		s.writeSellerPayoutReview(w, r, item, err)
	}
}
