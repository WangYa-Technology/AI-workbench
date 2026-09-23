package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/community"
	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
	"github.com/hcai-chat/hcai-chat/internal/systemsettings"
)

func (s *Server) createCommunityPost(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "community:publish")
	if !ok {
		return
	}
	var input community.PostCreateInput
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.community.CreatePost(r.Context(), actor.ID, input, r.Header.Get("Idempotency-Key"))
	if err == nil {
		w.Header().Set("Location", "/community/posts/"+item.ID.String())
	}
	s.writeCommunityResult(w, r, item, err, http.StatusCreated)
}

func (s *Server) communityCapabilities(w http.ResponseWriter, r *http.Request) {
	settings, err := s.admin.GetSystemSettings(r.Context())
	if err != nil {
		s.internalError(w, r, "community capabilities", err)
		return
	}
	canPublish, canInteract, canReport := false, false, false
	if sessionToken(r) != "" {
		if user, err := s.identity.Authenticate(r.Context(), sessionToken(r)); err == nil {
			for _, permission := range user.Permissions {
				switch permission {
				case "community:publish":
					canPublish = true
				case "community:interact":
					canInteract = true
				case "community:report":
					canReport = true
				}
			}
		}
	}
	httputil.JSON(w, http.StatusOK, map[string]bool{"canPublish": canPublish && settings.PublishingEnabled, "canSaveDraft": canPublish, "canInteract": canInteract, "canReport": canReport, "publishingEnabled": settings.PublishingEnabled})
}

func (s *Server) getCommunityPost(w http.ResponseWriter, r *http.Request) {
	postID, ok := pathUUID(w, r, "postID")
	if !ok {
		return
	}
	item, err := s.community.GetPostForViewer(r.Context(), s.optionalViewer(r), postID)
	s.writeCommunityResult(w, r, item, err, http.StatusOK)
}

func (s *Server) getOwnedCommunityPost(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	id, ok := pathUUID(w, r, "postID")
	if !ok {
		return
	}
	item, err := s.community.GetOwnedPost(r.Context(), actor.ID, id)
	s.writeCommunityResult(w, r, item, err, http.StatusOK)
}

func (s *Server) updateCommunityPost(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "community:publish")
	if !ok {
		return
	}
	id, ok := pathUUID(w, r, "postID")
	if !ok {
		return
	}
	var input community.PostUpdateInput
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.community.UpdatePost(r.Context(), actor.ID, id, input, httputil.RequestID(r.Context()))
	s.writeCommunityResult(w, r, item, err, http.StatusOK)
}

func (s *Server) deleteCommunityPost(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	id, ok := pathUUID(w, r, "postID")
	if !ok {
		return
	}
	var input struct {
		ExpectedVersion int `json:"expectedVersion"`
	}
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	err := s.community.DeletePost(r.Context(), actor.ID, id, input.ExpectedVersion, httputil.RequestID(r.Context()))
	if err == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.writeCommunityResult(w, r, nil, err, http.StatusOK)
}

func (s *Server) getCommunityReport(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "community:report")
	if !ok {
		return
	}
	id, ok := pathUUID(w, r, "reportID")
	if !ok {
		return
	}
	item, err := s.community.GetReportForViewer(r.Context(), actor.ID, id)
	s.writeCommunityResult(w, r, item, err, http.StatusOK)
}

func (s *Server) listComments(w http.ResponseWriter, r *http.Request) {
	postID, ok := pathUUID(w, r, "postID")
	if !ok {
		return
	}
	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_comment_filters", "Use a page size from 1 to 50 and an unmodified comment cursor.", false)
			return
		}
		limit = parsed
	}
	page, err := s.community.ListComments(r.Context(), postID, community.CommentListInput{Cursor: r.URL.Query().Get("cursor"), Limit: limit})
	if err != nil {
		if errors.Is(err, community.ErrInvalidCommentFilter) {
			httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_comment_filters", "Use a page size from 1 to 50 and an unmodified comment cursor.", false)
			return
		}
		s.writeCommunityResult(w, r, page, err, http.StatusOK)
		return
	}
	httputil.JSON(w, http.StatusOK, page)
}

func (s *Server) createComment(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "community:interact")
	if !ok {
		return
	}
	postID, valid := pathUUID(w, r, "postID")
	if !valid {
		return
	}
	var input struct {
		Body string `json:"body"`
	}
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.community.CreateComment(r.Context(), actor.ID, postID, input.Body, r.Header.Get("Idempotency-Key"))
	s.writeCommunityResult(w, r, item, err, http.StatusCreated)
}

func (s *Server) setPostReaction(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "community:interact")
	if !ok {
		return
	}
	postID, valid := pathUUID(w, r, "postID")
	if !valid {
		return
	}
	var input struct {
		Active bool `json:"active"`
	}
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.community.SetReaction(r.Context(), actor.ID, postID, chi.URLParam(r, "kind"), input.Active)
	s.writeCommunityResult(w, r, item, err, http.StatusOK)
}

func (s *Server) setCommunityFollow(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "community:interact")
	if !ok {
		return
	}
	authorID, valid := pathUUID(w, r, "authorID")
	if !valid {
		return
	}
	var input struct {
		Active bool `json:"active"`
	}
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.community.SetFollow(r.Context(), actor.ID, authorID, input.Active)
	s.writeCommunityResult(w, r, item, err, http.StatusOK)
}

func (s *Server) reportCommunityPost(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "community:report")
	if !ok {
		return
	}
	postID, valid := pathUUID(w, r, "postID")
	if !valid {
		return
	}
	var input community.ReportInput
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.community.ReportPost(r.Context(), actor.ID, postID, input)
	s.writeCommunityResult(w, r, item, err, http.StatusCreated)
}

func (s *Server) listMyCommunityReports(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "community:report")
	if !ok {
		return
	}
	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_community_report_filters", "Use a page size from 1 to 50 and an unmodified cursor.", false)
			return
		}
		limit = parsed
	}
	page, err := s.community.ListMyReports(r.Context(), actor.ID, community.ReportListInput{Cursor: r.URL.Query().Get("cursor"), Limit: limit})
	if err != nil {
		if errors.Is(err, community.ErrInvalidReportFilter) {
			httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_community_report_filters", "Use a page size from 1 to 50 and an unmodified cursor.", false)
			return
		}
		s.writeCommunityResult(w, r, page, err, http.StatusOK)
		return
	}
	httputil.JSON(w, http.StatusOK, page)
}

func (s *Server) createCommunityAppeal(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "community:report")
	if !ok {
		return
	}
	reportID, valid := pathUUID(w, r, "reportID")
	if !valid {
		return
	}
	var input struct {
		Reason string `json:"reason"`
	}
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.community.CreateAppeal(r.Context(), actor.ID, reportID, input.Reason)
	s.writeCommunityResult(w, r, item, err, http.StatusCreated)
}

func (s *Server) writeCommunityResult(w http.ResponseWriter, r *http.Request, item any, err error, successStatus int) {
	switch {
	case errors.Is(err, community.ErrInvalid):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_community_command", "Review the selected option and provide the required detail.", false)
	case errors.Is(err, community.ErrNotFound):
		httputil.WriteError(w, r, http.StatusNotFound, "community_resource_not_found", "The Community resource is not available.", false)
	case errors.Is(err, community.ErrForbidden):
		httputil.WriteError(w, r, http.StatusForbidden, "community_action_forbidden", "You cannot perform this action on the resource.", false)
	case errors.Is(err, community.ErrConflict):
		httputil.WriteError(w, r, http.StatusConflict, "community_state_conflict", "This action is already recorded or is not available in the current state.", false)
	case errors.Is(err, systemsettings.ErrDisabled):
		httputil.WriteError(w, r, http.StatusServiceUnavailable, "feature_disabled", "Community publishing is temporarily unavailable by a platform setting.", false)
	case err != nil:
		s.internalError(w, r, "community command", err)
	default:
		httputil.JSON(w, successStatus, item)
	}
}

func (s *Server) optionalViewer(r *http.Request) uuid.UUID {
	if sessionToken(r) == "" {
		return uuid.Nil
	}
	user, err := s.identity.Authenticate(r.Context(), sessionToken(r))
	if err != nil {
		return uuid.Nil
	}
	return user.ID
}
