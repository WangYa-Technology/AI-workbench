package httpapi

import (
	"github.com/go-chi/chi/v5"
	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
	"github.com/hcai-chat/hcai-chat/internal/tasktypes"
	"net/http"
)

func (s *Server) listTaskTypes(w http.ResponseWriter, r *http.Request) {
	scope := r.URL.Query().Get("scope")
	var items []tasktypes.Type
	var err error
	if scope == "" {
		items, err = s.taskTypes.List(r.Context())
	} else {
		items, err = s.taskTypes.List(r.Context(), scope)
	}
	if err != nil {
		s.internalError(w, r, "list task types", err)
		return
	}
	httputil.JSON(w, http.StatusOK, map[string]any{"items": items})
}
func (s *Server) adminCreateTaskType(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, categoryPermission(r)); !ok {
		return
	}
	var in tasktypes.Type
	if !httputil.DecodeJSON(w, r, &in) {
		return
	}
	in.Scope = categoryScope(r)
	item, err := s.taskTypes.Create(r.Context(), in)
	if err != nil {
		httputil.WriteError(w, r, 422, "invalid_task_type", "Invalid or duplicate category. Check the code, names, icon and order.", false)
		return
	}
	httputil.JSON(w, http.StatusCreated, item)
}
func (s *Server) adminUpdateTaskType(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, categoryPermission(r)); !ok {
		return
	}
	var in tasktypes.Type
	if !httputil.DecodeJSON(w, r, &in) {
		return
	}
	in.Scope = categoryScope(r)
	item, err := s.taskTypes.Update(r.Context(), chi.URLParam(r, "code"), in)
	if err != nil {
		httputil.WriteError(w, r, 422, "invalid_task_type", "Category does not exist or its fields are invalid.", false)
		return
	}
	httputil.JSON(w, 200, item)
}
func (s *Server) adminDeleteTaskType(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, categoryPermission(r)); !ok {
		return
	}
	var in struct {
		Replacement string `json:"replacement"`
	}
	if !httputil.DecodeJSON(w, r, &in) {
		return
	}
	if err := s.taskTypes.Delete(r.Context(), chi.URLParam(r, "code"), in.Replacement, categoryScope(r)); err != nil {
		httputil.WriteError(w, r, 422, "invalid_task_type", "Select a different category in the same directory to receive existing content.", false)
		return
	}
	httputil.JSON(w, 200, map[string]bool{"deleted": true})
}

func categoryPermission(r *http.Request) string {
	if r.URL.Query().Get("scope") == "community" || r.URL.Query().Get("scope") == "marketplace" {
		return "admin:content"
	}
	return "admin:tasks"
}
func (s *Server) adminAssignCategory(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:content"); !ok {
		return
	}
	var in struct {
		Category string `json:"category"`
	}
	if !httputil.DecodeJSON(w, r, &in) {
		return
	}
	if err := s.taskTypes.Assign(r.Context(), categoryScope(r), chi.URLParam(r, "id"), in.Category); err != nil {
		httputil.WriteError(w, r, 422, "invalid_task_type", "Content or category does not exist in this directory.", false)
		return
	}
	httputil.JSON(w, 200, map[string]bool{"updated": true})
}
func (s *Server) adminListCategoryContent(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:content"); !ok {
		return
	}
	items, next, err := s.taskTypes.ListContent(r.Context(), categoryScope(r), r.URL.Query().Get("q"), r.URL.Query().Get("cursor"))
	if err != nil {
		httputil.WriteError(w, r, 422, "invalid_task_type", "Invalid content directory or cursor.", false)
		return
	}
	httputil.JSON(w, 200, map[string]any{"items": items, "nextCursor": next})
}
func categoryScope(r *http.Request) string {
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		return "task"
	}
	return scope
}
