package httpapi

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"io"
	"net/http"
	"strconv"

	"github.com/hcai-chat/hcai-chat/internal/payments"
	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
)

type sellerPayoutRequestInput struct {
	AmountCents  int       `json:"amountCents"`
	SettlementID uuid.UUID `json:"settlementId"`
}

func (s *Server) getSellerFunds(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.listingUser(w, r, false)
	if !ok {
		return
	}
	if len(r.URL.Query()) != 0 {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_seller_funds_filter", "The seller funds endpoint does not accept query filters.", false)
		return
	}
	item, err := s.payments.GetSellerFunds(r.Context(), actor)
	if err != nil {
		s.writeSellerFundsError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	httpStatusJSON(w, http.StatusOK, item)
}

func (s *Server) createSellerPayoutRequest(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.listingUser(w, r, false)
	if !ok {
		return
	}
	key := idempotencyKey(r)
	if len(r.Header.Values("Idempotency-Key")) != 1 || len(key) < 8 || len(key) > 160 {
		s.writeSellerFundsError(w, r, payments.ErrSellerPayoutConflict)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	var input sellerPayoutRequestInput
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		s.writeSellerFundsError(w, r, payments.ErrInvalidSellerPayout)
		return
	}
	var extra struct{}
	if err := decoder.Decode(&extra); err != io.EOF {
		s.writeSellerFundsError(w, r, payments.ErrInvalidSellerPayout)
		return
	}
	item, err := s.payments.CreateSellerPayoutRequestForSettlement(r.Context(), actor, input.SettlementID, input.AmountCents, key)
	if err != nil {
		s.writeSellerFundsError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	httpStatusJSON(w, http.StatusCreated, item)
}

func (s *Server) cancelSellerPayoutRequest(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.listingUser(w, r, false)
	if !ok {
		return
	}
	id, ok := pathUUID(w, r, "requestID")
	if !ok {
		return
	}
	if len(r.URL.Query()) != 0 {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_seller_payout_filter", "The payout cancellation endpoint does not accept query filters.", false)
		return
	}
	item, err := s.payments.CancelSellerPayoutRequest(r.Context(), actor, id)
	if err != nil {
		s.writeSellerFundsError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	httpStatusJSON(w, http.StatusOK, item)
}

func httpStatusJSON(w http.ResponseWriter, status int, value any) {
	httputil.JSON(w, status, value)
}

func (s *Server) writeSellerFundsError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, payments.ErrSellerPayoutFilter):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_seller_payout_filter", "Check the payout pagination parameters.", false)
	case errors.Is(err, payments.ErrSellerFundsReconciliation):
		httputil.WriteError(w, r, http.StatusConflict, "seller_funds_reconciliation_required", "Historical seller funds must be classified before a new payout.", false)
	case errors.Is(err, payments.ErrSellerFundsInsufficient):
		httputil.WriteError(w, r, http.StatusPaymentRequired, "seller_funds_insufficient", "The requested payout exceeds the available seller funds.", false)
	case errors.Is(err, payments.ErrSellerFundsRecoveryDue):
		httputil.WriteError(w, r, http.StatusConflict, "seller_recovery_due", "An outstanding recovery obligation must be resolved before requesting a payout.", false)
	case errors.Is(err, payments.ErrSellerPayoutModeDisabled):
		httputil.WriteError(w, r, http.StatusServiceUnavailable, "seller_payout_unavailable", "Seller payouts are not enabled while automatic settlement is active.", false)
	case errors.Is(err, payments.ErrSellerPayoutAllocation):
		httputil.WriteError(w, r, http.StatusConflict, "seller_payout_allocation_required", "The requested amount must match complete available settlements.", false)
	case errors.Is(err, payments.ErrDisabled):
		httputil.WriteError(w, r, http.StatusServiceUnavailable, "payment_provider_unavailable", "Seller funds are not available in this environment.", false)
	case errors.Is(err, payments.ErrSellerPayoutConflict):
		httputil.WriteError(w, r, http.StatusConflict, "seller_payout_idempotency_conflict", "The payout request key is missing or already belongs to a different amount.", false)
	case errors.Is(err, payments.ErrSellerPayoutNotCancellable):
		httputil.WriteError(w, r, http.StatusConflict, "seller_payout_not_cancellable", "This payout request has already entered an irreversible provider state.", false)
	case errors.Is(err, payments.ErrSellerPayoutNotFound):
		httputil.WriteError(w, r, http.StatusNotFound, "seller_payout_not_found", "The payout request was not found.", false)
	case errors.Is(err, payments.ErrInvalidSellerPayout):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_seller_payout_request", "Provide the selected settlement, its amount and an idempotency key.", false)
	default:
		s.internalError(w, r, "seller funds", err)
	}
}

func (s *Server) getSellerPayoutRequest(w http.ResponseWriter, r *http.Request) {
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
		s.writeSellerFundsError(w, r, payments.ErrSellerPayoutFilter)
		return
	}
	item, err := s.payments.GetSellerPayoutRequest(r.Context(), actor, id)
	if err != nil {
		s.writeSellerFundsError(w, r, err)
		return
	}
	httputil.JSON(w, http.StatusOK, item)
}

func (s *Server) listSellerPayoutRequests(w http.ResponseWriter, r *http.Request) {
	s.sellerPayoutCatalog(w, r, false)
}
func (s *Server) listSellerPayoutOptions(w http.ResponseWriter, r *http.Request) {
	s.sellerPayoutCatalog(w, r, true)
}
func (s *Server) sellerPayoutCatalog(w http.ResponseWriter, r *http.Request, options bool) {
	w.Header().Set("Cache-Control", "private, no-store")
	actor, ok := s.listingUser(w, r, false)
	if !ok {
		return
	}
	cursor := ""
	limit := 20
	for key, values := range r.URL.Query() {
		if len(values) != 1 {
			s.writeSellerFundsError(w, r, payments.ErrSellerPayoutFilter)
			return
		}
		switch key {
		case "cursor":
			cursor = values[0]
			if cursor == "" {
				s.writeSellerFundsError(w, r, payments.ErrSellerPayoutFilter)
				return
			}
		case "limit":
			var err error
			limit, err = strconv.Atoi(values[0])
			if err != nil || limit < 1 || limit > 50 {
				s.writeSellerFundsError(w, r, payments.ErrSellerPayoutFilter)
				return
			}
		default:
			s.writeSellerFundsError(w, r, payments.ErrSellerPayoutFilter)
			return
		}
	}
	var item any
	var err error
	if options {
		item, err = s.payments.SellerPayoutOptions(r.Context(), actor, cursor, limit)
	} else {
		item, err = s.payments.ListSellerPayoutRequests(r.Context(), actor, cursor, limit)
	}
	if err != nil {
		s.writeSellerFundsError(w, r, err)
		return
	}
	httputil.JSON(w, http.StatusOK, item)
}
