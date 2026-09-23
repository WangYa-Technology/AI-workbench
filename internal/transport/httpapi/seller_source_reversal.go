package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/hcai-chat/hcai-chat/internal/payments"
	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
)

func (s *Server) adminSellerSourceReversal(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	actor, ok := s.requirePermission(w, r, "admin:finance")
	if !ok {
		return
	}
	id, ok := pathUUID(w, r, "requestID")
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		s.writeSellerSourceReversal(w, r, nil, payments.ErrSellerSourceReversalInvalid)
		return
	}
	if r.Method == http.MethodGet {
		item, err := s.payments.GetSellerSourceReversalOperation(r.Context(), actor.ID, id)
		s.writeSellerSourceReversal(w, r, item, err)
		return
	}
	var input payments.SellerSourceReversalInput
	if !decodeSellerSourceCommand(w, r, &input) {
		s.writeSellerSourceReversal(w, r, nil, payments.ErrSellerSourceReversalInvalid)
		return
	}
	item, err := s.payments.SubmitSellerSourceReversal(r.Context(), actor.ID, id, input, idempotencyKey(r), httputil.RequestID(r.Context()))
	s.writeSellerSourceReversal(w, r, item, err)
}

func (s *Server) adminCloseSellerSourceReversal(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	actor, ok := s.requirePermission(w, r, "admin:finance")
	if !ok {
		return
	}
	id, ok := pathUUID(w, r, "commandID")
	if !ok {
		return
	}
	var input payments.SellerSourceClosureInput
	if r.URL.RawQuery != "" || !decodeSellerSourceCommand(w, r, &input) {
		s.writeSellerSourceReversal(w, r, nil, payments.ErrSellerSourceClosureInvalid)
		return
	}
	item, err := s.payments.CloseSellerSourceReversal(r.Context(), actor.ID, id, input, idempotencyKey(r), httputil.RequestID(r.Context()))
	s.writeSellerSourceReversal(w, r, item, err)
}

func decodeSellerSourceCommand(w http.ResponseWriter, r *http.Request, input any) bool {
	if len(r.Header.Values("Idempotency-Key")) != 1 {
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	return d.Decode(input) == nil && d.Decode(new(any)) == io.EOF
}

func (s *Server) writeSellerSourceReversal(w http.ResponseWriter, r *http.Request, item any, err error) {
	switch {
	case errors.Is(err, payments.ErrSellerSourceReversalInvalid):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_seller_source_reversal", "Confirm the current source, request version, bank evidence and reason.", false)
	case errors.Is(err, payments.ErrSellerSourceClosureInvalid):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_seller_source_closure", "Confirm the accepted return read, current request version and reason.", false)
	case errors.Is(err, payments.ErrSellerSourceReversalConflict):
		httputil.WriteError(w, r, http.StatusConflict, "seller_source_reversal_conflict", "The source or bank obligation changed. Refresh and review the original evidence.", false)
	case errors.Is(err, payments.ErrSellerSourceClosureConflict):
		httputil.WriteError(w, r, http.StatusConflict, "seller_source_closure_conflict", "The return, refund or bank evidence is incomplete or changed. Funds remain subject to reconciliation.", false)
	default:
		s.writeSellerPayoutReview(w, r, item, err)
	}
}
