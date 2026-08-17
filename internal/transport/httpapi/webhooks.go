package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
	"github.com/hcai-chat/hcai-chat/internal/webhooks"
)

func (s *Server) getDeveloperWebhooks(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "developer:credentials")
	if !ok {
		return
	}
	item, err := s.webhooks.GetAccess(r.Context(), actor.ID)
	s.writeWebhookResult(w, r, http.StatusOK, item, err)
}

func (s *Server) listDeveloperWebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "developer:credentials")
	if !ok {
		return
	}
	endpointID, ok := pathUUID(w, r, "endpointID")
	if !ok {
		return
	}
	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_webhook_delivery_filters", "Use a page size from 1 to 50 and an unmodified Webhook delivery cursor.", false)
			return
		}
		limit = parsed
	}
	page, err := s.webhooks.ListEndpointDeliveries(r.Context(), actor.ID, endpointID, webhooks.OwnerDeliveryListInput{
		Cursor: r.URL.Query().Get("cursor"), Limit: limit,
	})
	if errors.Is(err, webhooks.ErrInvalidOwnerFilter) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_webhook_delivery_filters", "Use a page size from 1 to 50 and an unmodified Webhook delivery cursor.", false)
		return
	}
	s.writeWebhookResult(w, r, http.StatusOK, page, err)
}

func (s *Server) createDeveloperWebhook(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "developer:credentials")
	if !ok {
		return
	}
	var input webhooks.CreateInput
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.webhooks.Create(r.Context(), actor.ID, input, httputil.RequestID(r.Context()))
	s.writeWebhookResult(w, r, http.StatusCreated, item, err)
}

func (s *Server) rotateDeveloperWebhookSecret(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "developer:credentials")
	if !ok {
		return
	}
	endpointID, ok := pathUUID(w, r, "endpointID")
	if !ok {
		return
	}
	var input webhooks.Transition
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.webhooks.Rotate(r.Context(), actor.ID, endpointID, input, httputil.RequestID(r.Context()))
	s.writeWebhookResult(w, r, http.StatusCreated, item, err)
}

func (s *Server) revokeDeveloperWebhook(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "developer:credentials")
	if !ok {
		return
	}
	endpointID, ok := pathUUID(w, r, "endpointID")
	if !ok {
		return
	}
	var input webhooks.Transition
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.webhooks.Revoke(r.Context(), actor.ID, endpointID, input, httputil.RequestID(r.Context()))
	s.writeWebhookResult(w, r, http.StatusOK, item, err)
}

func (s *Server) testDeveloperWebhook(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "developer:credentials")
	if !ok {
		return
	}
	endpointID, ok := pathUUID(w, r, "endpointID")
	if !ok {
		return
	}
	item, err := s.webhooks.QueueTest(r.Context(), actor.ID, endpointID, httputil.RequestID(r.Context()))
	s.writeWebhookResult(w, r, http.StatusAccepted, item, err)
}

func (s *Server) adminListWebhookDeadLetters(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:developer"); !ok {
		return
	}
	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_webhook_filters", "Use supported Webhook recovery filters, a page size from 1 to 50, and an unmodified cursor.", false)
			return
		}
		limit = parsed
	}
	page, err := s.webhooks.ListDeadLetters(r.Context(), webhooks.DeadLetterListInput{
		Query: r.URL.Query().Get("q"), EventType: r.URL.Query().Get("eventType"), Cursor: r.URL.Query().Get("cursor"), Limit: limit,
	})
	if errors.Is(err, webhooks.ErrInvalidDeadLetterFilter) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_webhook_filters", "Use supported Webhook recovery filters, a page size from 1 to 50, and an unmodified cursor.", false)
		return
	}
	s.writeWebhookResult(w, r, http.StatusOK, page, err)
}

func (s *Server) adminReplayWebhookDelivery(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:developer")
	if !ok {
		return
	}
	deliveryID, ok := pathUUID(w, r, "deliveryID")
	if !ok {
		return
	}
	var input webhooks.Transition
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.webhooks.Replay(r.Context(), actor.ID, deliveryID, input, httputil.RequestID(r.Context()))
	s.writeWebhookResult(w, r, http.StatusAccepted, item, err)
}

func (s *Server) writeWebhookResult(w http.ResponseWriter, r *http.Request, status int, item any, err error) {
	switch {
	case errors.Is(err, webhooks.ErrDisabled):
		httputil.WriteError(w, r, http.StatusServiceUnavailable, "developer_access_disabled", "Developer Access is disabled by an administrator.", false)
	case errors.Is(err, webhooks.ErrInvalid):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_webhook_command", "Review the endpoint, event selection, confirmation, and reason.", false)
	case errors.Is(err, webhooks.ErrNotFound):
		httputil.WriteError(w, r, http.StatusNotFound, "webhook_resource_not_found", "The webhook resource was not found.", false)
	case errors.Is(err, webhooks.ErrConflict):
		httputil.WriteError(w, r, http.StatusConflict, "webhook_state_conflict", "The webhook changed or an endpoint limit was reached. Refresh and try again.", false)
	case err != nil:
		s.internalError(w, r, "webhook command", err)
	default:
		httputil.JSON(w, status, item)
	}
}
