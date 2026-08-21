package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/hcai-chat/hcai-chat/internal/emailactions"
	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
)

func (s *Server) requestEmailVerification(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	item, err := s.emailActions.RequestVerification(r.Context(), user.ID, httputil.RequestID(r.Context()))
	s.writeEmailActionResult(w, r, http.StatusAccepted, item, err)
}

func (s *Server) listAccountEmailActions(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_email_action_filters", "Use a page size from 1 to 50 and an unmodified identity email-action cursor.", false)
			return
		}
		limit = parsed
	}
	page, err := s.emailActions.ListForUser(r.Context(), user.ID, emailactions.OwnerListInput{Cursor: r.URL.Query().Get("cursor"), Limit: limit})
	if errors.Is(err, emailactions.ErrInvalidOwnerFilter) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_email_action_filters", "Use a page size from 1 to 50 and an unmodified identity email-action cursor.", false)
		return
	}
	s.writeEmailActionResult(w, r, http.StatusOK, page, err)
}

func (s *Server) requestPasswordReset(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email string `json:"email"`
	}
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	if err := s.emailActions.RequestPasswordReset(r.Context(), input.Email, httputil.RequestID(r.Context())); err != nil {
		s.internalError(w, r, "request password reset", err)
		return
	}
	httputil.JSON(w, http.StatusAccepted, map[string]bool{"accepted": true})
}

func (s *Server) confirmEmailVerification(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Token string `json:"token"`
	}
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	err := s.emailActions.ConfirmVerification(r.Context(), input.Token, httputil.RequestID(r.Context()))
	s.writeEmailActionResult(w, r, http.StatusNoContent, nil, err)
}

func (s *Server) confirmPasswordReset(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	revoked, err := s.emailActions.ConfirmPasswordReset(r.Context(), input.Token, input.Password, httputil.RequestID(r.Context()))
	if err == nil {
		clearSessionCookie(w, s.config.CookieSecure)
	}
	s.writeEmailActionResult(w, r, http.StatusOK, map[string]any{"revokedSessionCount": revoked}, err)
}

func (s *Server) adminListEmailActionDeadLetters(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:email_delivery"); !ok {
		return
	}
	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_email_filters", "Use supported email recovery filters, a page size from 1 to 50, and an unmodified cursor.", false)
			return
		}
		limit = parsed
	}
	page, err := s.emailActions.ListDeadLetters(r.Context(), emailactions.DeadLetterListInput{
		Query: r.URL.Query().Get("q"), Kind: r.URL.Query().Get("kind"), Cursor: r.URL.Query().Get("cursor"), Limit: limit,
	})
	if errors.Is(err, emailactions.ErrInvalidDeadLetterFilter) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_email_filters", "Use supported email recovery filters, a page size from 1 to 50, and an unmodified cursor.", false)
		return
	}
	s.writeEmailActionResult(w, r, http.StatusOK, page, err)
}

func (s *Server) adminRetryEmailAction(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:email_delivery")
	if !ok {
		return
	}
	actionID, ok := pathUUID(w, r, "actionID")
	if !ok {
		return
	}
	var input emailactions.Transition
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.emailActions.Retry(r.Context(), actor.ID, actionID, input, httputil.RequestID(r.Context()))
	s.writeEmailActionResult(w, r, http.StatusAccepted, item, err)
}

func (s *Server) adminCancelEmailAction(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:email_delivery")
	if !ok {
		return
	}
	actionID, ok := pathUUID(w, r, "actionID")
	if !ok {
		return
	}
	var input emailactions.Transition
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.emailActions.Cancel(r.Context(), actor.ID, actionID, input, httputil.RequestID(r.Context()))
	s.writeEmailActionResult(w, r, http.StatusOK, item, err)
}

func (s *Server) writeEmailActionResult(w http.ResponseWriter, r *http.Request, status int, item any, err error) {
	switch {
	case errors.Is(err, emailactions.ErrInvalid):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_email_action", "Review the token, password, and current version.", false)
	case errors.Is(err, emailactions.ErrNotFound):
		httputil.WriteError(w, r, http.StatusNotFound, "email_action_not_found", "This email action is invalid or no longer available.", false)
	case errors.Is(err, emailactions.ErrExpired):
		httputil.WriteError(w, r, http.StatusGone, "email_action_expired", "This email action has expired. Request a new email.", false)
	case errors.Is(err, emailactions.ErrAlreadyVerified):
		httputil.WriteError(w, r, http.StatusConflict, "email_already_verified", "This email address is already verified.", false)
	case errors.Is(err, emailactions.ErrConflict):
		httputil.WriteError(w, r, http.StatusConflict, "email_action_conflict", "This email action was already used, cancelled, or changed. Request a new email.", false)
	case err != nil:
		s.internalError(w, r, "identity email action", err)
	default:
		if status == http.StatusNoContent {
			w.WriteHeader(status)
			return
		}
		httputil.JSON(w, status, item)
	}
}
