package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/hcai-chat/hcai-chat/internal/payments"
	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
)

func (s *Server) adminSellerPayoutFunding(w http.ResponseWriter, r *http.Request) {
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
		s.writeSellerPayoutFunding(w, r, nil, payments.ErrSellerFundingAdmissionInvalid)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var input payments.SellerFundingAdmissionInput
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF {
		s.writeSellerPayoutFunding(w, r, nil, payments.ErrSellerFundingAdmissionInvalid)
		return
	}
	item, err := s.payments.AdmitSellerPayoutFunding(r.Context(), actor.ID, id, input, idempotencyKey(r), httputil.RequestID(r.Context()))
	s.writeSellerPayoutFunding(w, r, item, err)
}

func (s *Server) writeSellerPayoutFunding(w http.ResponseWriter, r *http.Request, item any, err error) {
	switch {
	case errors.Is(err, payments.ErrSellerFundingAdmissionInvalid):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_seller_funding_admission", "Confirm the approved revision, settlement, amount, bound bank and funding reason.", false)
	case errors.Is(err, payments.ErrSellerFundingAdmissionConflict), errors.Is(err, payments.ErrSellerPayoutBankConflict), errors.Is(err, payments.ErrCheckoutReconciliation):
		httputil.WriteError(w, r, http.StatusConflict, "seller_funding_admission_conflict", "The approval, request or funds changed. Refresh before authorizing funding.", false)
	default:
		s.writeSellerPayoutReview(w, r, item, err)
	}
}
