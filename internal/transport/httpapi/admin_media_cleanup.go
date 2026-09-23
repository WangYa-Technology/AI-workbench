package httpapi

import (
	"errors"
	"net/http"

	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
)

func (s *Server) adminListMediaCleanups(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:data-rights"); !ok {
		return
	}
	input, ok := parseDataRightsListInput(w, r, "invalid_media_cleanup_filters")
	if !ok {
		return
	}
	for _, key := range []string{"kind", "status", "cursor"} {
		if len(r.URL.Query()[key]) > 1 {
			httputil.WriteError(w, r, 422, "invalid_media_cleanup_filters", "Provide one value per filter.", false)
			return
		}
	}
	page, err := s.dataRights.ListMediaCleanups(r.Context(), datarights.MediaCleanupListInput{ListInput: input, Status: r.URL.Query().Get("status"), Kind: r.URL.Query().Get("kind")})
	if errors.Is(err, datarights.ErrInvalidList) {
		httputil.WriteError(w, r, 422, "invalid_media_cleanup_filters", "Use a supported job status, page size, and matching cursor.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "list media cleanup jobs", err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	httputil.JSON(w, http.StatusOK, page)
}
func (s *Server) adminRetryMediaCleanup(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:data-rights")
	if !ok {
		return
	}
	id, ok := pathUUID(w, r, "jobID")
	if !ok {
		return
	}
	var input datarights.MediaCleanupRetryInput
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.dataRights.RetryMediaCleanup(r.Context(), actor.ID, id, input, httputil.RequestID(r.Context()))
	switch {
	case errors.Is(err, datarights.ErrInvalid):
		httputil.WriteError(w, r, 422, "invalid_media_cleanup_retry", "Confirm recovery, provide the observed attempt count and a reason of 10 to 2000 characters.", false)
	case errors.Is(err, datarights.ErrCleanupForbidden):
		httputil.WriteError(w, r, 403, "forbidden", "This recovery requires data-rights permission.", false)
	case errors.Is(err, datarights.ErrNotFound):
		httputil.WriteError(w, r, 404, "media_cleanup_not_found", "The cleanup job was not found.", false)
	case errors.Is(err, datarights.ErrConflict):
		httputil.WriteError(w, r, 409, "media_cleanup_conflict", "Refresh the queue; this job changed, was retried, or cannot be recovered now.", false)
	case err != nil:
		s.internalError(w, r, "retry media cleanup", err)
	default:
		w.Header().Set("Cache-Control", "private, no-store")
		httputil.JSON(w, http.StatusCreated, item)
	}
}
