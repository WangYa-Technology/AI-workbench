package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
	"github.com/hcai-chat/hcai-chat/internal/reconciliation"
)

func (s *Server) adminListProviderCostReconciliations(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:finance"); !ok {
		return
	}
	limit := 0
	if value := strings.TrimSpace(r.URL.Query().Get("limit")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_provider_cost_reconciliation_filters", "Use a valid reconciliation status, page size from 1 to 50, and an unmodified cursor.", false)
			return
		}
		limit = parsed
	}
	page, err := s.reconciliation.List(r.Context(), reconciliation.ListInput{
		Status: r.URL.Query().Get("status"), Cursor: r.URL.Query().Get("cursor"), Limit: limit,
	})
	if errors.Is(err, reconciliation.ErrInvalid) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_provider_cost_reconciliation_filters", "Use a valid reconciliation status, page size from 1 to 50, and an unmodified cursor.", false)
		return
	}
	if errors.Is(err, reconciliation.ErrUnavailable) {
		httputil.WriteError(w, r, http.StatusServiceUnavailable, "provider_cost_reconciliation_unavailable", "Provider cost reconciliation is not enabled for this environment.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "list provider cost reconciliations", err)
		return
	}
	httputil.JSON(w, http.StatusOK, page)
}

func (s *Server) adminRequestProviderCostReconciliation(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:finance")
	if !ok {
		return
	}
	var input reconciliation.RequestInput
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.reconciliation.Request(r.Context(), actor.ID, input, httputil.RequestID(r.Context()))
	if errors.Is(err, reconciliation.ErrInvalid) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_provider_cost_reconciliation", "Choose an approved Provider, complete UTC-day period, reason, and explicit confirmation.", false)
		return
	}
	if errors.Is(err, reconciliation.ErrUnavailable) {
		httputil.WriteError(w, r, http.StatusServiceUnavailable, "provider_cost_reconciliation_unavailable", "Provider cost reconciliation is not enabled for this environment.", false)
		return
	}
	if errors.Is(err, reconciliation.ErrConflict) {
		httputil.WriteError(w, r, http.StatusConflict, "provider_cost_reconciliation_in_progress", "A reconciliation for this Provider and period is already in progress.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "request provider cost reconciliation", err)
		return
	}
	httputil.JSON(w, http.StatusCreated, item)
}
