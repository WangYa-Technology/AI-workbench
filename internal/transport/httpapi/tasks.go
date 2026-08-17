package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/billing"
	"github.com/hcai-chat/hcai-chat/internal/identity"
	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
	"github.com/hcai-chat/hcai-chat/internal/systemsettings"
	"github.com/hcai-chat/hcai-chat/internal/tasks"
)

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	viewerID := s.optionalViewer(r)
	mine := r.URL.Query().Get("mine") == "true"
	if mine && viewerID == uuid.Nil {
		if _, ok := s.requireUser(w, r); !ok {
			return
		}
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := s.tasks.List(r.Context(), viewerID, tasks.ListFilter{
		Query: r.URL.Query().Get("q"), DeliverableType: r.URL.Query().Get("type"),
		Status: r.URL.Query().Get("status"), Sort: r.URL.Query().Get("sort"),
		Mine: mine, Limit: limit,
	})
	if err != nil {
		s.internalError(w, r, "list tasks", err)
		return
	}
	httputil.JSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getTask(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "taskID")
	if !ok {
		return
	}
	item, err := s.tasks.Get(r.Context(), s.optionalViewer(r), id)
	s.writeTaskResult(w, r, http.StatusOK, item, err)
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var input tasks.CreateInput
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.tasks.Create(r.Context(), user.ID, input, idempotencyKey(r))
	if err == nil {
		w.Header().Set("Location", "/api/v1/tasks/"+item.ID.String())
	}
	s.writeTaskResult(w, r, http.StatusCreated, item, err)
}

func (s *Server) proposeTask(w http.ResponseWriter, r *http.Request) {
	user, taskID, ok := s.taskActorAndID(w, r)
	if !ok {
		return
	}
	var input tasks.ProposeInput
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.tasks.Propose(r.Context(), user.ID, taskID, input, idempotencyKey(r))
	s.writeTaskResult(w, r, http.StatusCreated, item, err)
}

func (s *Server) claimTask(w http.ResponseWriter, r *http.Request) {
	user, taskID, ok := s.taskActorAndID(w, r)
	if !ok {
		return
	}
	item, err := s.tasks.Claim(r.Context(), user.ID, taskID, idempotencyKey(r))
	s.writeTaskResult(w, r, http.StatusOK, item, err)
}

func (s *Server) acceptTaskProposal(w http.ResponseWriter, r *http.Request) {
	user, taskID, ok := s.taskActorAndID(w, r)
	if !ok {
		return
	}
	proposalID, ok := pathUUID(w, r, "proposalID")
	if !ok {
		return
	}
	item, err := s.tasks.AcceptProposal(r.Context(), user.ID, taskID, proposalID, idempotencyKey(r))
	s.writeTaskResult(w, r, http.StatusOK, item, err)
}

func (s *Server) deliverTask(w http.ResponseWriter, r *http.Request) {
	user, taskID, ok := s.taskActorAndID(w, r)
	if !ok {
		return
	}
	var input tasks.DeliverInput
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.tasks.Deliver(r.Context(), user.ID, taskID, input, idempotencyKey(r))
	s.writeTaskResult(w, r, http.StatusCreated, item, err)
}

func (s *Server) reviewTask(w http.ResponseWriter, r *http.Request) {
	user, taskID, ok := s.taskActorAndID(w, r)
	if !ok {
		return
	}
	var input tasks.ReviewInput
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.tasks.Review(r.Context(), user.ID, taskID, input, idempotencyKey(r))
	s.writeTaskResult(w, r, http.StatusOK, item, err)
}

func (s *Server) disputeTask(w http.ResponseWriter, r *http.Request) {
	user, taskID, ok := s.taskActorAndID(w, r)
	if !ok {
		return
	}
	var input struct {
		Reason string `json:"reason"`
	}
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.tasks.OpenDispute(r.Context(), user.ID, taskID, input.Reason, idempotencyKey(r))
	s.writeTaskResult(w, r, http.StatusCreated, item, err)
}

func (s *Server) cancelTask(w http.ResponseWriter, r *http.Request) {
	user, taskID, ok := s.taskActorAndID(w, r)
	if !ok {
		return
	}
	var input struct {
		Reason string `json:"reason"`
	}
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.tasks.Cancel(r.Context(), user.ID, taskID, input.Reason, idempotencyKey(r))
	s.writeTaskResult(w, r, http.StatusOK, item, err)
}

func (s *Server) taskActorAndID(w http.ResponseWriter, r *http.Request) (identityUser, uuidValue, bool) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return identityUser{}, uuidValue{}, false
	}
	id, ok := pathUUID(w, r, "taskID")
	if !ok {
		return identityUser{}, uuidValue{}, false
	}
	return identityUser(user), uuidValue(id), true
}

type identityUser = identity.User
type uuidValue = uuid.UUID

func idempotencyKey(r *http.Request) string {
	return strings.TrimSpace(r.Header.Get("Idempotency-Key"))
}

func (s *Server) writeTaskResult(w http.ResponseWriter, r *http.Request, successStatus int, item tasks.Detail, err error) {
	switch {
	case errors.Is(err, tasks.ErrNotFound):
		httputil.WriteError(w, r, http.StatusNotFound, "task_not_found", "The requested task was not found.", false)
	case errors.Is(err, tasks.ErrInvalid):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_task_command", "Review the required fields and provide an Idempotency-Key of at least 8 characters.", false)
	case errors.Is(err, tasks.ErrForbidden):
		httputil.WriteError(w, r, http.StatusForbidden, "task_action_forbidden", "Your account cannot perform this action for the task.", false)
	case errors.Is(err, tasks.ErrConflict):
		httputil.WriteError(w, r, http.StatusConflict, "task_state_conflict", "The task changed or this action is no longer available. Refresh and review its current state.", false)
	case errors.Is(err, billing.ErrInsufficientFunds):
		httputil.WriteError(w, r, http.StatusPaymentRequired, "insufficient_credits", "The commissioner does not have enough available Local Test credits to settle this task.", false)
	case errors.Is(err, systemsettings.ErrDisabled):
		httputil.WriteError(w, r, http.StatusServiceUnavailable, "feature_disabled", "New task publishing is temporarily unavailable by an audited platform setting.", false)
	case err != nil:
		s.internalError(w, r, "task command", err)
	default:
		httputil.JSON(w, successStatus, item)
	}
}
