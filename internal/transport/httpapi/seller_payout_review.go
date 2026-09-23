package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/hcai-chat/hcai-chat/internal/payments"
	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
)

func (s *Server) adminSellerPayoutDirectory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	actor, ok := s.requirePermission(w, r, "admin:finance")
	if !ok {
		return
	}
	cursor, limit := "", 20
	for k, values := range r.URL.Query() {
		if len(values) != 1 {
			s.writeSellerPayoutReview(w, r, nil, payments.ErrSellerPayoutFilter)
			return
		}
		switch k {
		case "cursor":
			cursor = values[0]
			if cursor == "" {
				s.writeSellerPayoutReview(w, r, nil, payments.ErrSellerPayoutFilter)
				return
			}
		case "limit":
			var err error
			limit, err = strconv.Atoi(values[0])
			if err != nil || limit < 1 || limit > 50 {
				s.writeSellerPayoutReview(w, r, nil, payments.ErrSellerPayoutFilter)
				return
			}
		default:
			s.writeSellerPayoutReview(w, r, nil, payments.ErrSellerPayoutFilter)
			return
		}
	}
	item, err := s.payments.SellerPayoutReviewDirectory(r.Context(), actor.ID, cursor, limit)
	s.writeSellerPayoutReview(w, r, item, err)
}

func (s *Server) adminSellerPayoutReview(w http.ResponseWriter, r *http.Request) {
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
		s.writeSellerPayoutReview(w, r, nil, payments.ErrSellerPayoutReviewInvalid)
		return
	}
	if r.Method == http.MethodGet {
		item, err := s.payments.GetSellerPayoutReview(r.Context(), actor.ID, id)
		s.writeSellerPayoutReview(w, r, item, err)
		return
	}
	if len(r.Header.Values("Idempotency-Key")) != 1 {
		s.writeSellerPayoutReview(w, r, nil, payments.ErrSellerPayoutReviewInvalid)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var input struct {
		payments.SellerPayoutReviewInput
		ExpectedRevision *int `json:"expectedRevision"`
	}
	// Decode the required revision separately: omission must not silently mean
	// the first review.
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF || input.ExpectedRevision == nil {
		s.writeSellerPayoutReview(w, r, nil, payments.ErrSellerPayoutReviewInvalid)
		return
	}
	typed := input.SellerPayoutReviewInput
	typed.ExpectedRevision = *input.ExpectedRevision
	item, err := s.payments.ReviewSellerPayout(r.Context(), actor.ID, id, typed, idempotencyKey(r), httputil.RequestID(r.Context()))
	s.writeSellerPayoutReview(w, r, item, err)
}

func (s *Server) writeSellerPayoutReview(w http.ResponseWriter, r *http.Request, item any, err error) {
	switch {
	case err == nil:
		httputil.JSON(w, http.StatusOK, item)
	case errors.Is(err, payments.ErrFinanceForbidden):
		httputil.WriteError(w, r, http.StatusForbidden, "forbidden", "An independent finance reviewer is required.", false)
	case errors.Is(err, payments.ErrSellerPayoutReviewInvalid):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_seller_payout_review", "Provide the review revision, settlement, amount, selected bank, decision, internal reason and seller message.", false)
	case errors.Is(err, payments.ErrSellerPayoutReviewConflict), errors.Is(err, payments.ErrSellerPayoutBankConflict), errors.Is(err, payments.ErrCheckoutReconciliation):
		httputil.WriteError(w, r, http.StatusConflict, "seller_payout_review_conflict", "The review, request or funds eligibility changed. Refresh before deciding.", false)
	default:
		s.writeSellerFundsError(w, r, err)
	}
}
