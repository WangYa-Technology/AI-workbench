package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/billing"
	"github.com/hcai-chat/hcai-chat/internal/identity"
	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
)

func (s *Server) adminOverview(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:overview"); !ok {
		return
	}
	item, err := s.admin.Overview(r.Context())
	if err != nil {
		s.internalError(w, r, "admin overview", err)
		return
	}
	httputil.JSON(w, http.StatusOK, item)
}

func (s *Server) adminListUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:users"); !ok {
		return
	}
	limit := 0
	if value := strings.TrimSpace(r.URL.Query().Get("limit")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_user_filters", "Use supported user filters, a page size from 1 to 50, and an unmodified cursor.", false)
			return
		}
		limit = parsed
	}
	page, err := s.admin.ListUsers(r.Context(), admin.UserListInput{
		Query: r.URL.Query().Get("q"), Role: r.URL.Query().Get("role"), Status: r.URL.Query().Get("status"),
		Cursor: r.URL.Query().Get("cursor"), Limit: limit,
	})
	if errors.Is(err, admin.ErrInvalidUserFilter) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_user_filters", "Use supported user filters, a page size from 1 to 50, and an unmodified cursor.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "admin list users", err)
		return
	}
	httputil.JSON(w, http.StatusOK, page)
}

func (s *Server) adminUpdateUser(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:users")
	if !ok {
		return
	}
	id, valid := pathUUID(w, r, "userID")
	if !valid {
		return
	}
	var input admin.UserUpdate
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.admin.UpdateUser(r.Context(), actor.ID, id, input, httputil.RequestID(r.Context()))
	s.writeAdminResult(w, r, item, err)
}

func (s *Server) adminListContent(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:content"); !ok {
		return
	}
	limit := 0
	if value := strings.TrimSpace(r.URL.Query().Get("limit")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_content_filters", "Use supported content filters, a page size from 1 to 50, and an unmodified cursor.", false)
			return
		}
		limit = parsed
	}
	page, err := s.admin.ListContent(r.Context(), admin.ContentListInput{
		Query: r.URL.Query().Get("q"), ResourceType: r.URL.Query().Get("type"), Status: r.URL.Query().Get("status"),
		Cursor: r.URL.Query().Get("cursor"), Limit: limit,
	})
	if errors.Is(err, admin.ErrInvalidContentFilter) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_content_filters", "Use supported content filters, a page size from 1 to 50, and an unmodified cursor.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "admin list content", err)
		return
	}
	httputil.JSON(w, http.StatusOK, page)
}

func (s *Server) adminUpdateContent(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:content")
	if !ok {
		return
	}
	id, valid := pathUUID(w, r, "workID")
	if !valid {
		return
	}
	var input admin.ContentUpdate
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.admin.UpdateContent(r.Context(), actor.ID, id, input, httputil.RequestID(r.Context()))
	s.writeAdminResult(w, r, item, err)
}

func (s *Server) adminListMedia(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:media"); !ok {
		return
	}
	limit := 0
	if value := strings.TrimSpace(r.URL.Query().Get("limit")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_media_filters", "Use supported media filters, a page size from 1 to 50, and an unmodified cursor.", false)
			return
		}
		limit = parsed
	}
	page, err := s.admin.ListMedia(r.Context(), admin.MediaListInput{
		Query: r.URL.Query().Get("q"), Kind: r.URL.Query().Get("kind"), Status: r.URL.Query().Get("status"),
		Cursor: r.URL.Query().Get("cursor"), Limit: limit,
	})
	if errors.Is(err, admin.ErrInvalidMediaFilter) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_media_filters", "Use supported media filters, a page size from 1 to 50, and an unmodified cursor.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "admin list media", err)
		return
	}
	httputil.JSON(w, http.StatusOK, page)
}

func (s *Server) adminReviewMedia(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:media")
	if !ok {
		return
	}
	id, valid := pathUUID(w, r, "assetID")
	if !valid {
		return
	}
	var input admin.MediaReview
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.admin.ReviewMedia(r.Context(), actor.ID, id, input, httputil.RequestID(r.Context()))
	s.writeAdminResult(w, r, item, err)
}

func (s *Server) adminListGenerations(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:generations"); !ok {
		return
	}
	limit, ok := adminDirectoryLimit(w, r, "invalid_admin_generation_filters", "generation")
	if !ok {
		return
	}
	page, err := s.admin.ListGenerations(r.Context(), admin.GenerationListInput{
		Query: r.URL.Query().Get("q"), Mode: r.URL.Query().Get("mode"), Status: r.URL.Query().Get("status"),
		Cursor: r.URL.Query().Get("cursor"), Limit: limit,
	})
	if errors.Is(err, admin.ErrInvalidGenerationFilter) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_generation_filters", "Use supported generation filters, a page size from 1 to 50, and an unmodified cursor.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "admin list generations", err)
		return
	}
	httputil.JSON(w, http.StatusOK, page)
}

func (s *Server) adminCancelGeneration(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:generations")
	if !ok {
		return
	}
	id, valid := pathUUID(w, r, "generationID")
	if !valid {
		return
	}
	var input struct {
		Reason    string `json:"reason"`
		Confirmed bool   `json:"confirmed"`
	}
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	if !input.Confirmed {
		s.writeAdminResult(w, r, nil, admin.ErrInvalid)
		return
	}
	item, err := s.admin.CancelGeneration(r.Context(), actor.ID, id, input.Reason, httputil.RequestID(r.Context()))
	s.writeAdminResult(w, r, item, err)
}

func (s *Server) adminListTasks(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:tasks"); !ok {
		return
	}
	limit := 0
	if value := strings.TrimSpace(r.URL.Query().Get("limit")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_task_filters", "Use supported task filters, a page size from 1 to 50, and an unmodified cursor.", false)
			return
		}
		limit = parsed
	}
	page, err := s.admin.ListTaskOperations(r.Context(), admin.TaskOperationListInput{
		Query: r.URL.Query().Get("q"), Status: r.URL.Query().Get("status"), DisputeStatus: r.URL.Query().Get("disputeStatus"),
		Cursor: r.URL.Query().Get("cursor"), Limit: limit,
	})
	if errors.Is(err, admin.ErrInvalidTaskFilter) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_task_filters", "Use supported task filters, a page size from 1 to 50, and an unmodified cursor.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "admin list tasks", err)
		return
	}
	httputil.JSON(w, http.StatusOK, page)
}

func (s *Server) adminResolveTaskDispute(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:tasks")
	if !ok {
		return
	}
	id, valid := pathUUID(w, r, "taskID")
	if !valid {
		return
	}
	var input admin.TaskDisputeResolution
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.admin.ResolveTaskDispute(r.Context(), actor.ID, id, input, httputil.RequestID(r.Context()))
	s.writeAdminResult(w, r, item, err)
}

func (s *Server) adminListProviders(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:providers"); !ok {
		return
	}
	items, err := s.admin.ListProviders(r.Context())
	if err != nil {
		s.internalError(w, r, "admin list providers", err)
		return
	}
	httputil.JSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) adminUpdateProvider(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:providers")
	if !ok {
		return
	}
	var input admin.ProviderUpdate
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.admin.UpdateProvider(r.Context(), actor.ID, chi.URLParam(r, "providerID"), input, httputil.RequestID(r.Context()))
	s.writeAdminResult(w, r, item, err)
}

func (s *Server) adminGetModelRoutes(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:models"); !ok {
		return
	}
	limit, ok := adminDirectoryLimit(w, r, "invalid_admin_model_route_history_filters", "model route history")
	if !ok {
		return
	}
	item, err := s.admin.GetModelRoutePolicy(r.Context(), admin.ModelRouteHistoryInput{Mode: r.URL.Query().Get("mode"), Cursor: r.URL.Query().Get("cursor"), Limit: limit})
	if errors.Is(err, admin.ErrInvalidModelRouteHistory) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_model_route_history_filters", "Use a supported creation mode, a page size from 1 to 50, and an unmodified model-route cursor.", false)
		return
	}
	s.writeAdminResult(w, r, item, err)
}

func (s *Server) adminUpdateModelRoute(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:models")
	if !ok {
		return
	}
	var input admin.ModelRouteUpdate
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.admin.UpdateModelRoute(r.Context(), actor.ID, chi.URLParam(r, "mode"), input, httputil.RequestID(r.Context()))
	s.writeAdminResult(w, r, item, err)
}

func (s *Server) adminGetSystemSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:settings"); !ok {
		return
	}
	limit, ok := adminDirectoryLimit(w, r, "invalid_admin_system_setting_history_filters", "system setting history")
	if !ok {
		return
	}
	item, err := s.admin.GetSystemSettingPolicy(r.Context(), admin.RevisionHistoryInput{Cursor: r.URL.Query().Get("cursor"), Limit: limit})
	if errors.Is(err, admin.ErrInvalidSystemSettingHistory) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_system_setting_history_filters", "Use a system setting history page size from 1 to 50 and an unmodified cursor.", false)
		return
	}
	s.writeAdminResult(w, r, item, err)
}

func (s *Server) adminUpdateSystemSettings(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:settings")
	if !ok {
		return
	}
	var input admin.SystemSettingUpdate
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.admin.UpdateSystemSettingPolicy(r.Context(), actor.ID, input, httputil.RequestID(r.Context()))
	s.writeAdminResult(w, r, item, err)
}

func (s *Server) adminListFinance(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:finance"); !ok {
		return
	}
	limit, ok := adminDirectoryLimit(w, r, "invalid_admin_finance_filters", "finance")
	if !ok {
		return
	}
	page, err := s.admin.ListFinance(r.Context(), admin.FinanceListInput{
		Query: r.URL.Query().Get("q"), State: r.URL.Query().Get("state"), Cursor: r.URL.Query().Get("cursor"), Limit: limit,
	})
	if errors.Is(err, admin.ErrInvalidFinanceFilter) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_finance_filters", "Use supported finance filters, a page size from 1 to 50, and an unmodified cursor.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "admin list finance", err)
		return
	}
	httputil.JSON(w, http.StatusOK, page)
}

func (s *Server) adminAdjustFinance(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:finance")
	if !ok {
		return
	}
	id, valid := pathUUID(w, r, "userID")
	if !valid {
		return
	}
	var input admin.FinanceAdjustment
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.admin.AdjustFinance(r.Context(), actor.ID, id, input, httputil.RequestID(r.Context()))
	s.writeAdminResult(w, r, item, err)
}

func (s *Server) adminListPayments(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:finance"); !ok {
		return
	}
	limit, ok := adminDirectoryLimit(w, r, "invalid_admin_payment_filters", "payment operations")
	if !ok {
		return
	}
	page, err := s.admin.ListPaymentOperations(r.Context(), admin.PaymentOperationListInput{
		Query: r.URL.Query().Get("q"), Purpose: r.URL.Query().Get("purpose"), Status: r.URL.Query().Get("status"),
		Mode: r.URL.Query().Get("mode"), Attention: r.URL.Query().Get("attention"), Cursor: r.URL.Query().Get("cursor"), Limit: limit,
	})
	if errors.Is(err, admin.ErrInvalidPaymentFilter) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_payment_filters", "Use supported payment filters, a page size from 1 to 50, and an unmodified cursor.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "admin list payments", err)
		return
	}
	httputil.JSON(w, http.StatusOK, page)
}

func (s *Server) adminRecoverPayment(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:finance")
	if !ok {
		return
	}
	id, valid := pathUUID(w, r, "paymentID")
	if !valid {
		return
	}
	var input admin.PaymentRecovery
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.admin.RecoverPayment(r.Context(), actor.ID, id, input, httputil.RequestID(r.Context()))
	s.writeAdminResult(w, r, item, err)
}

func (s *Server) adminReplayPaymentEvent(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:finance")
	if !ok {
		return
	}
	id, valid := pathUUID(w, r, "eventID")
	if !valid {
		return
	}
	var input admin.PaymentEventReplay
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.admin.ReplayPaymentEvent(r.Context(), actor.ID, id, input, httputil.RequestID(r.Context()))
	s.writeAdminResult(w, r, item, err)
}

func (s *Server) adminListPaymentDestinations(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:finance"); !ok {
		return
	}
	limit, ok := adminDirectoryLimit(w, r, "invalid_admin_payment_destination_filters", "payment destinations")
	if !ok {
		return
	}
	page, err := s.admin.ListPaymentDestinations(r.Context(), admin.PaymentDestinationListInput{
		Query: r.URL.Query().Get("q"), Status: r.URL.Query().Get("status"), Cursor: r.URL.Query().Get("cursor"), Limit: limit,
	})
	if errors.Is(err, admin.ErrInvalidDestinationFilter) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_payment_destination_filters", "Use a supported destination status, a page size from 1 to 50, and an unmodified cursor.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "admin list payment destinations", err)
		return
	}
	httputil.JSON(w, http.StatusOK, page)
}

func (s *Server) adminUpdatePaymentDestination(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:finance")
	if !ok {
		return
	}
	id, valid := pathUUID(w, r, "userID")
	if !valid {
		return
	}
	var input admin.PaymentDestinationUpdate
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.admin.UpdatePaymentDestination(r.Context(), actor.ID, id, input, httputil.RequestID(r.Context()))
	s.writeAdminResult(w, r, item, err)
}

func (s *Server) adminListRiskSignals(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:risk"); !ok {
		return
	}
	resourceType := strings.TrimSpace(r.URL.Query().Get("resourceType"))
	resourceIDText := strings.TrimSpace(r.URL.Query().Get("resourceId"))
	var resourceID *uuid.UUID
	if resourceIDText != "" {
		parsed, err := uuid.Parse(resourceIDText)
		if err != nil {
			httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_risk_filters", "Use a supported resource type and a valid resource identifier together.", false)
			return
		}
		resourceID = &parsed
	}
	limit, ok := adminDirectoryLimit(w, r, "invalid_admin_risk_filters", "risk")
	if !ok {
		return
	}
	page, err := s.admin.ListRiskSignals(r.Context(), admin.RiskSignalFilter{
		Query: r.URL.Query().Get("q"), Status: r.URL.Query().Get("status"), Severity: r.URL.Query().Get("severity"),
		ResourceType: resourceType, ResourceID: resourceID, Cursor: r.URL.Query().Get("cursor"), Limit: limit,
	})
	if errors.Is(err, admin.ErrInvalidRiskFilter) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_risk_filters", "Use supported risk filters, paired resource focus, a page size from 1 to 50, and an unmodified cursor.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "admin list risk signals", err)
		return
	}
	httputil.JSON(w, http.StatusOK, page)
}

func (s *Server) adminReviewRiskSignal(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:risk")
	if !ok {
		return
	}
	id, valid := pathUUID(w, r, "signalID")
	if !valid {
		return
	}
	var input admin.RiskReview
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.admin.ReviewRiskSignal(r.Context(), actor.ID, id, input, httputil.RequestID(r.Context()))
	s.writeAdminResult(w, r, item, err)
}

func (s *Server) adminGetRiskRules(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:risk_rules"); !ok {
		return
	}
	limit, ok := adminDirectoryLimit(w, r, "invalid_admin_risk_rule_history_filters", "risk rule history")
	if !ok {
		return
	}
	item, err := s.admin.GetRiskRulePolicy(r.Context(), admin.RevisionHistoryInput{Cursor: r.URL.Query().Get("cursor"), Limit: limit})
	if errors.Is(err, admin.ErrInvalidRiskRuleHistory) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_risk_rule_history_filters", "Use a risk rule history page size from 1 to 50 and an unmodified cursor.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "admin get risk rules", err)
		return
	}
	httputil.JSON(w, http.StatusOK, item)
}

func (s *Server) adminUpdateRiskRules(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:risk_rules")
	if !ok {
		return
	}
	var input admin.RiskRuleUpdate
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.admin.UpdateRiskRulePolicy(r.Context(), actor.ID, input, httputil.RequestID(r.Context()))
	s.writeAdminResult(w, r, item, err)
}

func (s *Server) adminListAudit(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:audit"); !ok {
		return
	}
	limit, ok := adminDirectoryLimit(w, r, "invalid_admin_audit_filters", "audit")
	if !ok {
		return
	}
	page, err := s.admin.ListAudit(r.Context(), admin.AuditListInput{
		Query: r.URL.Query().Get("q"), Action: r.URL.Query().Get("action"), ResourceType: r.URL.Query().Get("resourceType"),
		Cursor: r.URL.Query().Get("cursor"), Limit: limit,
	})
	if errors.Is(err, admin.ErrInvalidAuditFilter) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_audit_filters", "Use supported audit filters, a page size from 1 to 50, and an unmodified cursor.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "admin list audit", err)
		return
	}
	httputil.JSON(w, http.StatusOK, page)
}

func adminDirectoryLimit(w http.ResponseWriter, r *http.Request, code, subject string) (int, bool) {
	value := strings.TrimSpace(r.URL.Query().Get("limit"))
	if value == "" {
		return 0, true
	}
	limit, err := strconv.Atoi(value)
	if err != nil {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, code, "Use supported "+subject+" filters, a page size from 1 to 50, and an unmodified cursor.", false)
		return 0, false
	}
	return limit, true
}

func (s *Server) adminGetOperationalDiagnostics(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:observability"); !ok {
		return
	}
	item, err := s.admin.GetOperationalDiagnostics(r.Context())
	if err != nil {
		s.internalError(w, r, "admin operational diagnostics", err)
		return
	}
	httputil.JSON(w, http.StatusOK, item)
}

func (s *Server) adminGetRankingPolicy(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:ranking"); !ok {
		return
	}
	limit, ok := adminDirectoryLimit(w, r, "invalid_admin_ranking_history_filters", "ranking history")
	if !ok {
		return
	}
	item, err := s.admin.GetRankingPolicy(r.Context(), admin.RevisionHistoryInput{Cursor: r.URL.Query().Get("cursor"), Limit: limit})
	if errors.Is(err, admin.ErrInvalidRankingHistory) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_ranking_history_filters", "Use a ranking history page size from 1 to 50 and an unmodified cursor.", false)
		return
	}
	if err != nil {
		s.writeAdminResult(w, r, item, err)
		return
	}
	httputil.JSON(w, http.StatusOK, item)
}

func (s *Server) adminUpdateRankingPolicy(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:ranking")
	if !ok {
		return
	}
	var input admin.RankingUpdate
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.admin.UpdateRankingPolicy(r.Context(), actor.ID, input, httputil.RequestID(r.Context()))
	s.writeAdminResult(w, r, item, err)
}

func (s *Server) adminCreateRankingCandidate(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:ranking")
	if !ok {
		return
	}
	var input admin.RankingUpdate
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.admin.CreateRankingCandidate(r.Context(), actor.ID, input, httputil.RequestID(r.Context()))
	s.writeAdminResult(w, r, item, err)
}

func (s *Server) adminRunRankingEvaluation(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:ranking")
	if !ok {
		return
	}
	var input admin.ConfirmedReason
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.admin.RunRankingEvaluation(r.Context(), actor.ID, input, httputil.RequestID(r.Context()))
	s.writeAdminResult(w, r, item, err)
}

func (s *Server) adminUpdateRankingRollout(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:ranking")
	if !ok {
		return
	}
	var input admin.RankingRolloutUpdate
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.admin.UpdateRankingRollout(r.Context(), actor.ID, input, httputil.RequestID(r.Context()))
	s.writeAdminResult(w, r, item, err)
}

func (s *Server) adminGetDiscoveryOperations(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:ranking"); !ok {
		return
	}
	limit, ok := adminDirectoryLimit(w, r, "invalid_admin_discovery_history_filters", "Discovery operation history")
	if !ok {
		return
	}
	item, err := s.admin.GetDiscoveryOperations(r.Context(), admin.DiscoveryHistoryInput{
		IndexCursor: r.URL.Query().Get("indexCursor"), EvaluationCursor: r.URL.Query().Get("evaluationCursor"), Limit: limit,
	})
	if errors.Is(err, admin.ErrInvalidDiscoveryHistory) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_discovery_history_filters", "Use a Discovery history page size from 1 to 50 and unmodified cursors.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "get discovery operations", err)
		return
	}
	httputil.JSON(w, http.StatusOK, item)
}

func (s *Server) adminAnalyzeDiscoveryIndex(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:ranking")
	if !ok {
		return
	}
	var input admin.ConfirmedReason
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.admin.RunDiscoveryIndexAnalyze(r.Context(), actor.ID, input, httputil.RequestID(r.Context()))
	s.writeAdminResult(w, r, item, err)
}

func (s *Server) adminListReports(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:governance"); !ok {
		return
	}
	limit := 0
	if value := strings.TrimSpace(r.URL.Query().Get("limit")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_report_filters", "Use supported report filters, a page size from 1 to 50, and an unmodified cursor.", false)
			return
		}
		limit = parsed
	}
	page, err := s.admin.ListReports(r.Context(), admin.GovernanceReportListInput{
		Query: r.URL.Query().Get("q"), ResourceType: r.URL.Query().Get("type"), Category: r.URL.Query().Get("category"),
		Status: r.URL.Query().Get("status"), Cursor: r.URL.Query().Get("cursor"), Limit: limit,
	})
	if errors.Is(err, admin.ErrInvalidReportFilter) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_report_filters", "Use supported report filters, a page size from 1 to 50, and an unmodified cursor.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "admin list governance reports", err)
		return
	}
	httputil.JSON(w, http.StatusOK, page)
}

func (s *Server) adminResolveReport(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:governance")
	if !ok {
		return
	}
	id, valid := pathUUID(w, r, "reportID")
	if !valid {
		return
	}
	var input admin.ReportResolution
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.admin.ResolveReport(r.Context(), actor.ID, id, input, httputil.RequestID(r.Context()))
	s.writeAdminResult(w, r, item, err)
}

func (s *Server) adminListAppeals(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:governance"); !ok {
		return
	}
	limit := 0
	if value := strings.TrimSpace(r.URL.Query().Get("limit")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_appeal_filters", "Use supported appeal filters, a page size from 1 to 50, and an unmodified cursor.", false)
			return
		}
		limit = parsed
	}
	page, err := s.admin.ListAppeals(r.Context(), admin.GovernanceAppealListInput{
		Query: r.URL.Query().Get("q"), ResourceType: r.URL.Query().Get("type"), Status: r.URL.Query().Get("status"),
		Cursor: r.URL.Query().Get("cursor"), Limit: limit,
	})
	if errors.Is(err, admin.ErrInvalidAppealFilter) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_appeal_filters", "Use supported appeal filters, a page size from 1 to 50, and an unmodified cursor.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "admin list governance appeals", err)
		return
	}
	httputil.JSON(w, http.StatusOK, page)
}

func (s *Server) adminResolveAppeal(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:governance")
	if !ok {
		return
	}
	id, valid := pathUUID(w, r, "appealID")
	if !valid {
		return
	}
	var input admin.AppealResolution
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.admin.ResolveAppeal(r.Context(), actor.ID, id, input, httputil.RequestID(r.Context()))
	s.writeAdminResult(w, r, item, err)
}

func (s *Server) requirePermission(w http.ResponseWriter, r *http.Request, permission string) (identity.User, bool) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return identity.User{}, false
	}
	for _, granted := range user.Permissions {
		if granted == permission {
			return user, true
		}
	}
	httputil.WriteError(w, r, http.StatusForbidden, "permission_required", "Your account does not have permission to perform this operation.", false)
	return identity.User{}, false
}

func (s *Server) writeAdminResult(w http.ResponseWriter, r *http.Request, item any, err error) {
	switch {
	case errors.Is(err, admin.ErrNotFound), errors.Is(err, billing.ErrAccountNotFound):
		httputil.WriteError(w, r, http.StatusNotFound, "admin_resource_not_found", "The requested operations resource was not found.", false)
	case errors.Is(err, admin.ErrInvalid):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_command", "Confirm the operation and provide a specific reason of at least 10 characters.", false)
	case errors.Is(err, admin.ErrSelfMutation):
		httputil.WriteError(w, r, http.StatusConflict, "self_access_mutation_forbidden", "Use a different administrator account to change your own role or access state.", false)
	case errors.Is(err, admin.ErrConflict), errors.Is(err, billing.ErrReservationState):
		httputil.WriteError(w, r, http.StatusConflict, "admin_state_conflict", "The resource is not in a state that allows this operation.", false)
	case errors.Is(err, admin.ErrProviderConfig):
		httputil.WriteError(w, r, http.StatusServiceUnavailable, "provider_configuration_required", "This provider cannot be enabled until its external configuration is verified.", false)
	case errors.Is(err, billing.ErrInsufficientFunds):
		httputil.WriteError(w, r, http.StatusConflict, "billing_balance_conflict", "The adjustment would reduce the balance below reserved credits.", false)
	case err != nil:
		s.internalError(w, r, "admin command", err)
	default:
		httputil.JSON(w, http.StatusOK, item)
	}
}
