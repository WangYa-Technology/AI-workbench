package httpapi

import (
	"errors"
	"net/http"

	"github.com/hcai-chat/hcai-chat/internal/payments"
	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
)

func (s *Server) adminRefundHistory(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:finance"); !ok {
		return
	}
	id, ok := pathUUID(w, r, "paymentID")
	if !ok {
		return
	}
	limit, ok := adminDirectoryLimit(w, r, "invalid_admin_payment_filters", "refund history")
	if !ok {
		return
	}
	item, err := s.payments.RefundHistory(r.Context(), id, r.URL.Query().Get("cursor"), limit)
	s.writeRefundCheckResult(w, r, item, err)
}
func (s *Server) adminRequestRefundCheck(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:finance")
	if !ok {
		return
	}
	id, ok := pathUUID(w, r, "paymentID")
	if !ok {
		return
	}
	var input struct {
		ExpectedVersion int `json:"expectedVersion"`
	}
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.payments.RequestRefundCheck(r.Context(), actor.ID, id, input.ExpectedVersion)
	s.writeRefundCheckResult(w, r, item, err)
}

func (s *Server) adminListRefundChecks(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:finance"); !ok {
		return
	}
	id, ok := pathUUID(w, r, "paymentID")
	if !ok {
		return
	}
	limit, ok := adminDirectoryLimit(w, r, "invalid_admin_payment_filters", "refund checks")
	if !ok {
		return
	}
	item, err := s.payments.ListRefundChecks(r.Context(), id, r.URL.Query().Get("review"), r.URL.Query().Get("cursor"), limit)
	s.writeRefundCheckResult(w, r, item, err)
}

func (s *Server) adminGetRefundCheck(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:finance"); !ok {
		return
	}
	paymentID, ok := pathUUID(w, r, "paymentID")
	if !ok {
		return
	}
	checkID, ok := pathUUID(w, r, "checkID")
	if !ok {
		return
	}
	item, err := s.payments.GetRefundCheck(r.Context(), paymentID, checkID)
	s.writeRefundCheckResult(w, r, item, err)
}
func (s *Server) writeRefundCheckResult(w http.ResponseWriter, r *http.Request, item any, err error) {
	w.Header().Set("Cache-Control", "private, no-store")
	switch {
	case errors.Is(err, payments.ErrFinanceForbidden):
		httputil.WriteError(w, r, http.StatusForbidden, "forbidden", "Finance permission is required.", false)
	case errors.Is(err, payments.ErrRefundHistoryNotFound):
		httputil.WriteError(w, r, http.StatusNotFound, "admin_resource_not_found", "The requested product payment was not found.", false)
	case errors.Is(err, payments.ErrInvalidRefund):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_command", "Use a valid version and refund history cursor.", false)
	case errors.Is(err, payments.ErrRefundConflict):
		httputil.WriteError(w, r, http.StatusConflict, "admin_state_conflict", "Refresh the payment before requesting another check.", false)
	case errors.Is(err, payments.ErrDisabled), errors.Is(err, payments.ErrProviderUnavailable):
		httputil.WriteError(w, r, http.StatusServiceUnavailable, "payment_provider_unavailable", "Refund queries are unavailable for this payment provider.", false)
	case err != nil:
		s.internalError(w, r, "admin refund check", err)
	default:
		httputil.JSON(w, http.StatusOK, item)
	}
}

func (s *Server) adminRefundReadReceipts(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:finance"); !ok {
		return
	}
	paymentID, ok := pathUUID(w, r, "paymentID")
	if !ok {
		return
	}
	checkID, ok := pathUUID(w, r, "checkID")
	if !ok {
		return
	}
	for key, values := range r.URL.Query() {
		if key != "cursor" || len(values) != 1 {
			s.writeRefundCheckResult(w, r, nil, payments.ErrInvalidRefund)
			return
		}
	}
	page, err := s.payments.ListRefundReadReceipts(r.Context(), paymentID, checkID, r.URL.Query().Get("cursor"))
	s.writeRefundCheckResult(w, r, page, err)
}
