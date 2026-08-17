package httpapi

import (
	"errors"
	"net/http"

	"github.com/hcai-chat/hcai-chat/internal/developer"
	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
)

type keyRotateRequest struct {
	Scopes          []string `json:"scopes"`
	IPAllowlist     []string `json:"ipAllowlist"`
	TTLDays         int      `json:"ttlDays"`
	ExpectedVersion int      `json:"expectedVersion"`
	Reason          string   `json:"reason"`
	Confirmed       bool     `json:"confirmed"`
}

func (s *Server) getDeveloperAccess(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "developer:credentials")
	if !ok {
		return
	}
	item, err := s.developer.GetAccess(r.Context(), actor.ID)
	s.writeDeveloperResult(w, r, http.StatusOK, item, err)
}

func (s *Server) createDeveloperServiceAccount(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "developer:credentials")
	if !ok {
		return
	}
	var input developer.AccountCreate
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.developer.CreateAccount(r.Context(), actor.ID, input, httputil.RequestID(r.Context()))
	s.writeDeveloperResult(w, r, http.StatusCreated, item, err)
}

func (s *Server) issueDeveloperAPIKey(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "developer:credentials")
	if !ok {
		return
	}
	accountID, ok := pathUUID(w, r, "accountID")
	if !ok {
		return
	}
	var input developer.KeyCreate
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.developer.IssueKey(r.Context(), actor.ID, accountID, input, httputil.RequestID(r.Context()))
	s.writeDeveloperResult(w, r, http.StatusCreated, item, err)
}

func (s *Server) rotateDeveloperAPIKey(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "developer:credentials")
	if !ok {
		return
	}
	accountID, ok := pathUUID(w, r, "accountID")
	if !ok {
		return
	}
	keyID, ok := pathUUID(w, r, "keyID")
	if !ok {
		return
	}
	var input keyRotateRequest
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.developer.RotateKey(r.Context(), actor.ID, accountID, keyID,
		developer.KeyCreate{Scopes: input.Scopes, IPAllowlist: input.IPAllowlist, TTLDays: input.TTLDays},
		developer.Transition{ExpectedVersion: input.ExpectedVersion, Reason: input.Reason, Confirmed: input.Confirmed}, httputil.RequestID(r.Context()))
	s.writeDeveloperResult(w, r, http.StatusCreated, item, err)
}

func (s *Server) revokeDeveloperAPIKey(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "developer:credentials")
	if !ok {
		return
	}
	accountID, ok := pathUUID(w, r, "accountID")
	if !ok {
		return
	}
	keyID, ok := pathUUID(w, r, "keyID")
	if !ok {
		return
	}
	var input developer.Transition
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.developer.RevokeKey(r.Context(), actor.ID, accountID, keyID, input, httputil.RequestID(r.Context()))
	s.writeDeveloperResult(w, r, http.StatusOK, item, err)
}

func (s *Server) revokeDeveloperServiceAccount(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "developer:credentials")
	if !ok {
		return
	}
	accountID, ok := pathUUID(w, r, "accountID")
	if !ok {
		return
	}
	var input developer.Transition
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.developer.RevokeAccount(r.Context(), actor.ID, accountID, input, httputil.RequestID(r.Context()))
	s.writeDeveloperResult(w, r, http.StatusOK, item, err)
}

func (s *Server) adminGetDeveloperAccess(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:developer"); !ok {
		return
	}
	item, err := s.developer.ListAll(r.Context())
	s.writeDeveloperResult(w, r, http.StatusOK, item, err)
}

func (s *Server) adminUpdateDeveloperControl(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:developer")
	if !ok {
		return
	}
	var input developer.ControlUpdate
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.developer.UpdateControl(r.Context(), actor.ID, input, httputil.RequestID(r.Context()))
	s.writeDeveloperResult(w, r, http.StatusOK, item, err)
}

func (s *Server) adminRevokeDeveloperServiceAccount(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:developer")
	if !ok {
		return
	}
	accountID, ok := pathUUID(w, r, "accountID")
	if !ok {
		return
	}
	var input developer.Transition
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.developer.AdminRevokeAccount(r.Context(), actor.ID, accountID, input, httputil.RequestID(r.Context()))
	s.writeDeveloperResult(w, r, http.StatusOK, item, err)
}

func (s *Server) adminRevokeDeveloperAPIKey(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:developer")
	if !ok {
		return
	}
	keyID, ok := pathUUID(w, r, "keyID")
	if !ok {
		return
	}
	var input developer.Transition
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.developer.AdminRevokeKey(r.Context(), actor.ID, keyID, input, httputil.RequestID(r.Context()))
	s.writeDeveloperResult(w, r, http.StatusOK, item, err)
}

func (s *Server) writeDeveloperResult(w http.ResponseWriter, r *http.Request, status int, item any, err error) {
	switch {
	case errors.Is(err, developer.ErrDisabled):
		httputil.WriteError(w, r, http.StatusServiceUnavailable, "developer_access_disabled", "Developer Access is disabled by an administrator.", false)
	case errors.Is(err, developer.ErrInvalid):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_developer_command", "Review the credential settings, confirmation, and reason.", false)
	case errors.Is(err, developer.ErrNotFound):
		httputil.WriteError(w, r, http.StatusNotFound, "developer_resource_not_found", "The Developer Access resource was not found.", false)
	case errors.Is(err, developer.ErrConflict):
		httputil.WriteError(w, r, http.StatusConflict, "developer_state_conflict", "The credential changed or an account limit was reached. Refresh and try again.", false)
	case err != nil:
		s.internalError(w, r, "developer access command", err)
	default:
		httputil.JSON(w, status, item)
	}
}

func (s *Server) developerAPIContract(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAPIPrincipal(w, r, developer.IdentityReadScope()); !ok {
		return
	}
	s.writeAPIv1(w, r, developer.APIContract())
}

func (s *Server) developerAPIErrors(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAPIPrincipal(w, r, developer.IdentityReadScope()); !ok {
		return
	}
	s.writeAPIv1(w, r, map[string]any{"items": developer.APIErrorRegistry()})
}

func (s *Server) developerAPIPrincipal(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAPIPrincipal(w, r, developer.IdentityReadScope())
	if !ok {
		return
	}
	s.writeAPIv1(w, r, principal)
}

func (s *Server) requireAPIPrincipal(w http.ResponseWriter, r *http.Request, scope string) (developer.Principal, bool) {
	token := developer.BearerToken(r.Header.Get("Authorization"))
	principal, err := s.developer.Authenticate(r.Context(), token, r.RemoteAddr, scope)
	if err == nil {
		return principal, true
	}
	code, message := "AUTHENTICATION_REQUIRED", "Provide an active API key."
	status := http.StatusUnauthorized
	if errors.Is(err, developer.ErrScope) {
		code, message, status = "SCOPE_REQUIRED", "The API key does not grant the required scope.", http.StatusForbidden
	}
	if errors.Is(err, developer.ErrIP) {
		code, message, status = "SOURCE_IP_DENIED", "The request source is outside this key's allowlist.", http.StatusForbidden
	}
	if !errors.Is(err, developer.ErrUnauthenticated) && !errors.Is(err, developer.ErrScope) && !errors.Is(err, developer.ErrIP) {
		s.logger.Error("authenticate developer api", "request_id", httputil.RequestID(r.Context()), "error", err)
		code, message, status = "INTERNAL_ERROR", "The request could not be completed.", http.StatusInternalServerError
	}
	w.Header().Set("X-API-Version", "v1")
	requestID := httputil.RequestID(r.Context())
	httputil.JSON(w, status, map[string]any{"data": nil, "error": map[string]any{"code": code, "message": message}, "meta": map[string]any{"apiVersion": "v1", "requestId": requestID}})
	return developer.Principal{}, false
}

func (s *Server) writeAPIv1(w http.ResponseWriter, r *http.Request, data any) {
	w.Header().Set("X-API-Version", "v1")
	httputil.JSON(w, http.StatusOK, map[string]any{"data": data, "error": nil, "meta": map[string]any{"apiVersion": "v1", "requestId": httputil.RequestID(r.Context())}})
}
