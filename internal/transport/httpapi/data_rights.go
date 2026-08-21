package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
)

func (s *Server) listDataRightsRequests(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	input, ok := parseDataRightsListInput(w, r, "invalid_data_rights_filters")
	if !ok {
		return
	}
	page, err := s.dataRights.List(r.Context(), user.ID, input)
	if errors.Is(err, datarights.ErrInvalidList) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_data_rights_filters", "Use a page size from 1 to 50 and an unmodified data-rights cursor.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "list data rights requests", err)
		return
	}
	httputil.JSON(w, http.StatusOK, page)
}

func (s *Server) createDataRightsRequest(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var input datarights.CreateInput
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.dataRights.Create(r.Context(), user.ID, sessionToken(r), input, httputil.RequestID(r.Context()))
	switch {
	case errors.Is(err, datarights.ErrInvalid):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_data_rights_request", "Choose a supported request and confirm your exact account handle.", false)
	case errors.Is(err, datarights.ErrIdentity):
		httputil.WriteError(w, r, http.StatusForbidden, "identity_confirmation_failed", "The handle confirmation did not match this account.", false)
	case errors.Is(err, datarights.ErrReauth):
		httputil.WriteError(w, r, http.StatusUnauthorized, "recent_authentication_required", "Sign in again before creating a data-rights request.", false)
	case errors.Is(err, datarights.ErrConflict):
		httputil.WriteError(w, r, http.StatusConflict, "data_rights_request_conflict", "An active request already exists or the 30-day request limit was reached.", false)
	case errors.Is(err, datarights.ErrDemoAccount):
		httputil.WriteError(w, r, http.StatusConflict, "demo_data_rights_disabled", "Create a personal test account to verify data export or deletion. Shared demo accounts cannot be changed.", false)
	case err != nil:
		s.internalError(w, r, "create data rights request", err)
	default:
		w.Header().Set("Location", "/api/v1/account/data-rights/"+item.ID.String())
		httputil.JSON(w, http.StatusCreated, item)
	}
}

func (s *Server) cancelDataRightsRequest(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	id, valid := pathUUID(w, r, "requestID")
	if !valid {
		return
	}
	item, err := s.dataRights.Cancel(r.Context(), user.ID, id, httputil.RequestID(r.Context()))
	switch {
	case errors.Is(err, datarights.ErrNotFound):
		httputil.WriteError(w, r, http.StatusNotFound, "data_rights_request_not_found", "The owned request could not be found.", false)
	case errors.Is(err, datarights.ErrNotCancelable):
		httputil.WriteError(w, r, http.StatusConflict, "data_rights_request_not_cancelable", "This request can no longer be cancelled.", false)
	case err != nil:
		s.internalError(w, r, "cancel data rights request", err)
	default:
		httputil.JSON(w, http.StatusOK, item)
	}
}

func (s *Server) downloadDataExport(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	id, valid := pathUUID(w, r, "requestID")
	if !valid {
		return
	}
	body, checksum, err := s.dataRights.Download(r.Context(), user.ID, id)
	switch {
	case errors.Is(err, datarights.ErrNotReady):
		httputil.WriteError(w, r, http.StatusNotFound, "data_export_not_ready", "The export is not ready or does not belong to this account.", false)
	case errors.Is(err, datarights.ErrExpired):
		httputil.WriteError(w, r, http.StatusGone, "data_export_expired", "The seven-day export window has ended and the package body was purged.", false)
	case err != nil:
		s.internalError(w, r, "download data export", err)
	default:
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="hcai-data-export-%s.json"`, id.String()))
		w.Header().Set("Digest", "sha-256="+checksum)
		w.Header().Set("Cache-Control", "private, no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}
}

func (s *Server) adminListDataRights(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:data-rights"); !ok {
		return
	}
	input, ok := parseDataRightsListInput(w, r, "invalid_admin_data_rights_filters")
	if !ok {
		return
	}
	page, err := s.dataRights.ListAdmin(r.Context(), input)
	if errors.Is(err, datarights.ErrInvalidList) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_data_rights_filters", "Use a page size from 1 to 50 and an unmodified data-rights cursor.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "admin list data rights", err)
		return
	}
	httputil.JSON(w, http.StatusOK, page)
}

func (s *Server) adminListDataRightsHolds(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:data-rights"); !ok {
		return
	}
	input, ok := parseDataRightsListInput(w, r, "invalid_admin_data_rights_hold_filters")
	if !ok {
		return
	}
	page, err := s.dataRights.ListHolds(r.Context(), input)
	if errors.Is(err, datarights.ErrInvalidHolds) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_data_rights_hold_filters", "Use a page size from 1 to 50 and an unmodified legal-hold cursor.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "admin list data rights legal holds", err)
		return
	}
	httputil.JSON(w, http.StatusOK, page)
}

func parseDataRightsListInput(w http.ResponseWriter, r *http.Request, code string) (datarights.ListInput, bool) {
	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			httputil.WriteError(w, r, http.StatusUnprocessableEntity, code, "Use a page size from 1 to 50 and an unmodified cursor.", false)
			return datarights.ListInput{}, false
		}
		limit = parsed
	}
	return datarights.ListInput{Cursor: r.URL.Query().Get("cursor"), Limit: limit}, true
}

func (s *Server) adminCreateDataRightsHold(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:data-rights")
	if !ok {
		return
	}
	var input datarights.HoldInput
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.dataRights.CreateHold(r.Context(), actor.ID, input, httputil.RequestID(r.Context()))
	s.writeDataRightsAdminResult(w, r, item, err, http.StatusCreated)
}

func (s *Server) adminReleaseDataRightsHold(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:data-rights")
	if !ok {
		return
	}
	id, valid := pathUUID(w, r, "holdID")
	if !valid {
		return
	}
	item, err := s.dataRights.ReleaseHold(r.Context(), actor.ID, id)
	s.writeDataRightsAdminResult(w, r, item, err, http.StatusOK)
}

func (s *Server) writeDataRightsAdminResult(w http.ResponseWriter, r *http.Request, item any, err error, successStatus int) {
	switch {
	case errors.Is(err, datarights.ErrInvalid):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_legal_hold", "Provide a user and a valid authority reference.", false)
	case errors.Is(err, datarights.ErrNotFound):
		httputil.WriteError(w, r, http.StatusNotFound, "legal_hold_not_found", "The legal-hold target could not be found.", false)
	case errors.Is(err, datarights.ErrConflict):
		httputil.WriteError(w, r, http.StatusConflict, "legal_hold_conflict", "An active legal hold already exists or this hold was already released.", false)
	case errors.Is(err, datarights.ErrHoldCutoff):
		httputil.WriteError(w, r, http.StatusConflict, "legal_hold_cutoff_passed", "Deletion processing has started, so a new legal hold cannot be introduced.", false)
	case err != nil:
		s.internalError(w, r, "control data rights legal hold", err)
	default:
		httputil.JSON(w, successStatus, item)
	}
}
