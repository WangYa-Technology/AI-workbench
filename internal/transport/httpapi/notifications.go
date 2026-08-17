package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/notifications"
	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
)

func (s *Server) listNotifications(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	input := notifications.ListInput{
		ReadState: strings.TrimSpace(r.URL.Query().Get("readState")),
		Kind:      strings.TrimSpace(r.URL.Query().Get("kind")),
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		input.Limit, _ = strconv.Atoi(raw)
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("cursor")); raw != "" {
		cursor, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			httputil.WriteError(w, r, http.StatusBadRequest, "invalid_notification_cursor", "The notification cursor is invalid.", false)
			return
		}
		input.Before = &cursor
	}
	page, err := s.notifications.List(r.Context(), user.ID, input)
	if errors.Is(err, notifications.ErrInvalid) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_notification_filter", "Choose a supported read state, notification type, and page size.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "list notifications", err)
		return
	}
	httputil.JSON(w, http.StatusOK, page)
}

func (s *Server) listNotificationDeliveries(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	input := notifications.DeliveryEvidenceInput{Cursor: r.URL.Query().Get("cursor")}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_notification_delivery_filters", "Use a page size from 1 to 50 and an unmodified notification-delivery cursor.", false)
			return
		}
		input.Limit = limit
	}
	page, err := s.notifications.ListDeliveryEvidencePage(r.Context(), user.ID, input)
	if errors.Is(err, notifications.ErrInvalidDeliveryFilter) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_notification_delivery_filters", "Use a page size from 1 to 50 and an unmodified notification-delivery cursor.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "list notification deliveries", err)
		return
	}
	httputil.JSON(w, http.StatusOK, page)
}

func (s *Server) markNotificationRead(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "notificationID"))
	if err != nil {
		httputil.WriteError(w, r, http.StatusBadRequest, "invalid_notification_id", "The notification identifier is invalid.", false)
		return
	}
	item, err := s.notifications.MarkRead(r.Context(), user.ID, id)
	switch {
	case errors.Is(err, notifications.ErrNotFound):
		httputil.WriteError(w, r, http.StatusNotFound, "notification_not_found", "The notification could not be found.", false)
	case err != nil:
		s.internalError(w, r, "mark notification read", err)
	default:
		httputil.JSON(w, http.StatusOK, item)
	}
}

func (s *Server) markAllNotificationsRead(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	count, err := s.notifications.MarkAllRead(r.Context(), user.ID)
	if err != nil {
		s.internalError(w, r, "mark all notifications read", err)
		return
	}
	httputil.JSON(w, http.StatusOK, map[string]any{"markedCount": count})
}

func (s *Server) listNotificationPreferences(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	items, err := s.notifications.ListPreferences(r.Context(), user.ID)
	if err != nil {
		s.internalError(w, r, "list notification preferences", err)
		return
	}
	httputil.JSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) updateNotificationPreference(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var input struct {
		InAppEnabled    bool `json:"inAppEnabled"`
		ExpectedVersion int  `json:"expectedVersion"`
	}
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.notifications.UpdatePreference(r.Context(), user.ID, chi.URLParam(r, "kind"), input.InAppEnabled, input.ExpectedVersion)
	switch {
	case errors.Is(err, notifications.ErrInvalid):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_notification_preference", "The notification preference or version is invalid.", false)
	case errors.Is(err, notifications.ErrConflict):
		httputil.WriteError(w, r, http.StatusConflict, "notification_preference_changed", "This preference changed in another session. Refresh and try again.", false)
	case err != nil:
		s.internalError(w, r, "update notification preference", err)
	default:
		httputil.JSON(w, http.StatusOK, item)
	}
}
