package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/hcai-chat/hcai-chat/internal/payments"
	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
)

func (s *Server) listProductWebhookQuarantines(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	actor, ok := s.requirePermission(w, r, "admin:finance")
	if !ok {
		return
	}
	f := payments.WebhookQuarantineFilter{}
	for key, values := range r.URL.Query() {
		if len(values) != 1 {
			s.writeWebhookQuarantine(w, r, nil, payments.ErrQuarantineInvalid)
			return
		}
		switch key {
		case "state":
			f.State = values[0]
		case "provider":
			f.Provider = values[0]
		case "mode":
			f.Mode = values[0]
		case "cursor":
			f.Cursor = values[0]
		case "limit":
			n, err := strconv.Atoi(values[0])
			if err != nil || n < 1 || n > 50 {
				s.writeWebhookQuarantine(w, r, nil, payments.ErrQuarantineInvalid)
				return
			}
			f.Limit = n
		default:
			s.writeWebhookQuarantine(w, r, nil, payments.ErrQuarantineInvalid)
			return
		}
	}
	page, err := s.payments.ListWebhookQuarantines(r.Context(), actor.ID, f)
	s.writeWebhookQuarantine(w, r, page, err)
}
func (s *Server) recheckProductWebhookQuarantine(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	actor, ok := s.requirePermission(w, r, "admin:finance")
	if !ok {
		return
	}
	id, ok := pathUUID(w, r, "quarantineID")
	if !ok {
		return
	}
	var input payments.RecheckWebhookInput
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.payments.RecheckWebhookQuarantine(r.Context(), actor.ID, id, input)
	s.writeWebhookQuarantine(w, r, item, err)
}
func (s *Server) writeWebhookQuarantine(w http.ResponseWriter, r *http.Request, item any, err error) {
	switch {
	case errors.Is(err, payments.ErrQuarantineForbidden):
		httputil.WriteError(w, r, 403, "forbidden", "Finance permission is required.", false)
	case errors.Is(err, payments.ErrQuarantineInvalid):
		httputil.WriteError(w, r, 422, "invalid_admin_command", "Use matching filters and cursor, a current version, and a reason of 10–1000 characters.", false)
	case errors.Is(err, payments.ErrQuarantineConflict):
		httputil.WriteError(w, r, 409, "admin_state_conflict", "Refresh the evidence before checking it again.", false)
	case errors.Is(err, payments.ErrQuarantineNotFound):
		httputil.WriteError(w, r, 404, "admin_resource_not_found", "The requested evidence was not found.", false)
	case errors.Is(err, payments.ErrDisabled):
		httputil.WriteError(w, r, 503, "payment_provider_unavailable", "Payment processing is disabled.", false)
	case err != nil:
		s.internalError(w, r, "product webhook evidence", err)
	default:
		httputil.JSON(w, http.StatusOK, item)
	}
}
