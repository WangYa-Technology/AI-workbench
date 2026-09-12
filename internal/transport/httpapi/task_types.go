package httpapi

import (
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
	"github.com/hcai-chat/hcai-chat/internal/tasktypes"
	"net/http"
)

func (s *Server) listTaskTypes(w http.ResponseWriter, r *http.Request) {
	items, err := s.taskTypes.List(r.Context())
	if err != nil {
		s.internalError(w, r, "list task types", err)
		return
	}
	httputil.JSON(w, http.StatusOK, map[string]any{"items": items})
}
func (s *Server) adminCreateTaskType(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:tasks"); !ok {
		return
	}
	var in tasktypes.Type
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httputil.WriteError(w, r, 400, "invalid_task_type", "Invalid task type.", false)
		return
	}
	item, err := s.taskTypes.Create(r.Context(), in)
	if err != nil {
		httputil.WriteError(w, r, 422, "invalid_task_type", err.Error(), false)
		return
	}
	httputil.JSON(w, http.StatusCreated, item)
}
func (s *Server) adminUpdateTaskType(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:tasks"); !ok {
		return
	}
	var in tasktypes.Type
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httputil.WriteError(w, r, 400, "invalid_task_type", "Invalid task type.", false)
		return
	}
	item, err := s.taskTypes.Update(r.Context(), chi.URLParam(r, "code"), in)
	if err != nil {
		httputil.WriteError(w, r, 422, "invalid_task_type", err.Error(), false)
		return
	}
	httputil.JSON(w, 200, item)
}
func (s *Server) adminDeleteTaskType(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:tasks"); !ok {
		return
	}
	var in struct {
		Replacement string `json:"replacement"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	if err := s.taskTypes.Delete(r.Context(), chi.URLParam(r, "code"), in.Replacement); err != nil {
		httputil.WriteError(w, r, 422, "invalid_task_type", err.Error(), false)
		return
	}
	httputil.JSON(w, 200, map[string]bool{"deleted": true})
}
