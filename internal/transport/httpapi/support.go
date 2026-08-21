package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
	"github.com/hcai-chat/hcai-chat/internal/support"
)

func (s *Server) listSupportCases(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_support_filters", "Use a page size from 1 to 50 and an unmodified support-case cursor.", false)
			return
		}
		limit = parsed
	}
	page, err := s.support.ListOwned(r.Context(), user.ID, support.OwnedListInput{Cursor: r.URL.Query().Get("cursor"), Limit: limit})
	if errors.Is(err, support.ErrInvalidOwnerFilter) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_support_filters", "Use a page size from 1 to 50 and an unmodified support-case cursor.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "list support cases", err)
		return
	}
	httputil.JSON(w, http.StatusOK, page)
}

func (s *Server) createSupportCase(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var input support.CreateInput
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.support.Create(r.Context(), user.ID, input, httputil.RequestID(r.Context()))
	if err != nil {
		s.writeSupportResult(w, r, item, err, http.StatusCreated)
		return
	}
	httputil.JSON(w, http.StatusCreated, item)
}

func (s *Server) getSupportCase(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	id, valid := pathUUID(w, r, "caseID")
	if !valid {
		return
	}
	item, err := s.support.GetOwned(r.Context(), user.ID, id)
	s.writeSupportResult(w, r, item, err, http.StatusOK)
}

func (s *Server) replySupportCase(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	id, valid := pathUUID(w, r, "caseID")
	if !valid {
		return
	}
	var input support.ReplyInput
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.support.ReplyOwned(r.Context(), user.ID, id, input, httputil.RequestID(r.Context()))
	s.writeSupportResult(w, r, item, err, http.StatusOK)
}

func (s *Server) adminListSupportCases(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:support"); !ok {
		return
	}
	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_support_filters", "Use supported support filters, a page size from 1 to 50, and an unmodified cursor.", false)
			return
		}
		limit = parsed
	}
	page, err := s.support.ListAdmin(r.Context(), support.ListFilter{
		Query: r.URL.Query().Get("q"), Status: r.URL.Query().Get("status"), Category: r.URL.Query().Get("category"),
		Cursor: r.URL.Query().Get("cursor"), Limit: limit,
	})
	if errors.Is(err, support.ErrInvalidAdminFilter) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_support_filters", "Use supported support filters, a page size from 1 to 50, and an unmodified cursor.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "admin list support cases", err)
		return
	}
	httputil.JSON(w, http.StatusOK, page)
}

func (s *Server) adminGetSupportCase(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:support"); !ok {
		return
	}
	id, valid := pathUUID(w, r, "caseID")
	if !valid {
		return
	}
	item, err := s.support.GetAdmin(r.Context(), id)
	s.writeSupportResult(w, r, item, err, http.StatusOK)
}

func (s *Server) adminReplySupportCase(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:support")
	if !ok {
		return
	}
	id, valid := pathUUID(w, r, "caseID")
	if !valid {
		return
	}
	var input support.AdminReplyInput
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.support.AdminReply(r.Context(), actor.ID, id, input, httputil.RequestID(r.Context()))
	s.writeSupportResult(w, r, item, err, http.StatusOK)
}

func (s *Server) adminUpdateSupportCase(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:support")
	if !ok {
		return
	}
	id, valid := pathUUID(w, r, "caseID")
	if !valid {
		return
	}
	var input support.AdminUpdateInput
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.support.AdminUpdate(r.Context(), actor.ID, id, input, httputil.RequestID(r.Context()))
	s.writeSupportResult(w, r, item, err, http.StatusOK)
}

func (s *Server) writeSupportResult(w http.ResponseWriter, r *http.Request, item any, err error, successStatus int) {
	switch {
	case errors.Is(err, support.ErrNotFound), errors.Is(err, support.ErrRelatedNotFound):
		httputil.WriteError(w, r, http.StatusNotFound, "support_resource_not_found", "The support case or related resource could not be found.", false)
	case errors.Is(err, support.ErrConflict):
		httputil.WriteError(w, r, http.StatusConflict, "support_case_changed", "This case changed in another session or no longer allows that operation. Refresh and try again.", false)
	case errors.Is(err, support.ErrSensitiveData):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "sensitive_data_not_accepted", "Do not submit payment card numbers, government identifiers, signatures, or raw legal documents.", false)
	case errors.Is(err, support.ErrInvalid):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_support_case", "Review the required fields, current version, and resource reference.", false)
	case err != nil:
		s.internalError(w, r, "support operation", err)
	default:
		httputil.JSON(w, successStatus, item)
	}
}
