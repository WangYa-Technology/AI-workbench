package httpapi

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/authchallenges"
	"github.com/hcai-chat/hcai-chat/internal/emailactions"
	"github.com/hcai-chat/hcai-chat/internal/identity"
	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
	"github.com/hcai-chat/hcai-chat/internal/systemsettings"
)

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var input identity.RegisterInput
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	user, token, err := s.identity.Register(r.Context(), input, requestClientInfo(r, s.config.TrustedProxyCIDRs))
	switch {
	case errors.Is(err, identity.ErrInvalid):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_registration", "Use a valid email, a 3-30 character lowercase handle, a password of at least 10 characters, and a supported locale and IANA timezone.", false)
	case errors.Is(err, identity.ErrConflict):
		httputil.WriteError(w, r, http.StatusConflict, "account_exists", "An account already uses this email or handle.", false)
	case errors.Is(err, systemsettings.ErrDisabled):
		httputil.WriteError(w, r, http.StatusServiceUnavailable, "feature_disabled", "New registrations are temporarily unavailable by an audited platform setting.", false)
	case err != nil:
		s.internalError(w, r, "register account", err)
	default:
		setSessionCookie(w, token, s.config.CookieSecure)
		if _, actionErr := s.emailActions.RequestVerification(r.Context(), user.ID, httputil.RequestID(r.Context())); actionErr != nil && !errors.Is(actionErr, emailactions.ErrAlreadyVerified) {
			s.logger.Error("queue registration email verification", "requestId", httputil.RequestID(r.Context()), "error", actionErr)
		}
		httputil.JSON(w, http.StatusCreated, map[string]any{"user": user, "authentication": "email_password"})
	}
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var input identity.LoginInput
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	user, token, err := s.identity.Login(r.Context(), input, requestClientInfo(r, s.config.TrustedProxyCIDRs))
	switch {
	case errors.Is(err, identity.ErrInvalidLogin):
		httputil.WriteError(w, r, http.StatusUnauthorized, "invalid_credentials", "The email or password is incorrect.", false)
	case errors.Is(err, identity.ErrInactive):
		httputil.WriteError(w, r, http.StatusForbidden, "account_inactive", "This account is not active. Contact support before trying again.", false)
	case err != nil:
		s.internalError(w, r, "login account", err)
	default:
		setSessionCookie(w, token, s.config.CookieSecure)
		httputil.JSON(w, http.StatusOK, map[string]any{"user": user, "authentication": "email_password"})
	}
}

type unifiedAuthStartInput struct {
	Email  string `json:"email"`
	Locale string `json:"locale"`
}

type unifiedAuthStartResponse struct {
	Email     string                    `json:"email"`
	Account   bool                      `json:"accountExists"`
	Next      string                    `json:"next"`
	Challenge *authchallenges.Challenge `json:"challenge,omitempty"`
}

func (s *Server) unifiedAuthStart(w http.ResponseWriter, r *http.Request) {
	var input unifiedAuthStartInput
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(input.Email))
	locale := strings.TrimSpace(input.Locale)
	if locale == "" {
		locale = "en-US"
	}
	exists, err := s.identity.AccountExists(r.Context(), email)
	if errors.Is(err, identity.ErrInvalid) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_email", "Enter a valid email address.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "check unified auth email", err)
		return
	}
	response := unifiedAuthStartResponse{Email: email, Account: exists}
	if exists {
		response.Next = "existing_account"
	} else {
		challenge, challengeErr := s.authChallenges.Start(r.Context(), email, authchallenges.RegistrationCode, locale, httputil.RequestID(r.Context()))
		if errors.Is(challengeErr, authchallenges.ErrInvalid) {
			httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_email", "Enter a valid email address.", false)
			return
		}
		if errors.Is(challengeErr, authchallenges.ErrRateLimited) {
			httputil.WriteError(w, r, http.StatusTooManyRequests, "auth_code_rate_limited", "A code was sent recently. Wait before requesting another.", true)
			return
		}
		if challengeErr != nil {
			s.internalError(w, r, "queue registration code", challengeErr)
			return
		}
		response.Next, response.Challenge = "registration", &challenge
	}
	httputil.JSON(w, http.StatusOK, response)
}

type unifiedAuthCodeInput struct {
	Email   string `json:"email"`
	Purpose string `json:"purpose"`
	Locale  string `json:"locale"`
}

func (s *Server) unifiedAuthSendCode(w http.ResponseWriter, r *http.Request) {
	var input unifiedAuthCodeInput
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	input.Purpose = strings.TrimSpace(input.Purpose)
	input.Locale = strings.TrimSpace(input.Locale)
	if input.Locale == "" {
		input.Locale = "en-US"
	}
	if input.Purpose != authchallenges.LoginCode && input.Purpose != authchallenges.RegistrationCode {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_auth_code_purpose", "Use a supported authentication code purpose.", false)
		return
	}
	exists, err := s.identity.AccountExists(r.Context(), input.Email)
	if errors.Is(err, identity.ErrInvalid) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_email", "Enter a valid email address.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "check auth code account", err)
		return
	}
	if input.Purpose == authchallenges.LoginCode && !exists {
		httputil.WriteError(w, r, http.StatusNotFound, "account_not_found", "No active account uses this email.", false)
		return
	}
	if input.Purpose == authchallenges.RegistrationCode && exists {
		httputil.WriteError(w, r, http.StatusConflict, "account_exists", "An account already uses this email. Continue with sign-in.", false)
		return
	}
	challenge, err := s.authChallenges.Start(r.Context(), input.Email, input.Purpose, input.Locale, httputil.RequestID(r.Context()))
	if errors.Is(err, authchallenges.ErrInvalid) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_email", "Enter a valid email address.", false)
		return
	}
	if errors.Is(err, authchallenges.ErrRateLimited) {
		httputil.WriteError(w, r, http.StatusTooManyRequests, "auth_code_rate_limited", "A code was sent recently. Wait before requesting another.", true)
		return
	}
	if err != nil {
		s.internalError(w, r, "queue auth code", err)
		return
	}
	httputil.JSON(w, http.StatusAccepted, map[string]any{"challenge": challenge})
}

type unifiedAuthConfirmInput struct {
	ChallengeID string `json:"challengeId"`
	Email       string `json:"email"`
	Code        string `json:"code"`
}

func (s *Server) unifiedAuthLoginCode(w http.ResponseWriter, r *http.Request) {
	var input unifiedAuthConfirmInput
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	id, err := uuid.Parse(strings.TrimSpace(input.ChallengeID))
	if err != nil {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_auth_challenge", "The authentication challenge is invalid.", false)
		return
	}
	user, token, err := s.authChallenges.ConfirmLogin(r.Context(), id, input.Email, input.Code, requestClientInfo(r, s.config.TrustedProxyCIDRs))
	switch {
	case errors.Is(err, authchallenges.ErrInvalid), errors.Is(err, authchallenges.ErrInvalidCode):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_auth_code", "Enter the six-digit code from your email.", false)
	case errors.Is(err, authchallenges.ErrNotFound):
		httputil.WriteError(w, r, http.StatusNotFound, "auth_challenge_not_found", "The authentication code is no longer available.", false)
	case errors.Is(err, authchallenges.ErrExpired):
		httputil.WriteError(w, r, http.StatusGone, "auth_code_expired", "That code has expired. Request a new one.", false)
	case errors.Is(err, authchallenges.ErrRateLimited):
		httputil.WriteError(w, r, http.StatusTooManyRequests, "auth_code_rate_limited", "Too many attempts. Request a new code.", true)
	case errors.Is(err, authchallenges.ErrConflict):
		httputil.WriteError(w, r, http.StatusConflict, "auth_challenge_consumed", "That code can no longer be used.", false)
	case errors.Is(err, identity.ErrInactive):
		httputil.WriteError(w, r, http.StatusForbidden, "account_inactive", "This account is not active. Contact support before trying again.", false)
	case errors.Is(err, identity.ErrInvalidLogin):
		httputil.WriteError(w, r, http.StatusUnauthorized, "invalid_credentials", "The email or code is incorrect.", false)
	case err != nil:
		s.internalError(w, r, "confirm login code", err)
	default:
		setSessionCookie(w, token, s.config.CookieSecure)
		httputil.JSON(w, http.StatusOK, map[string]any{"user": user, "authentication": "email_code"})
	}
}

type unifiedAuthRegisterInput struct {
	ChallengeID string `json:"challengeId"`
	Code        string `json:"code"`
	identity.RegisterInput
}

func (s *Server) unifiedAuthRegister(w http.ResponseWriter, r *http.Request) {
	var input unifiedAuthRegisterInput
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	// The unified screen intentionally asks only for a handle and password;
	// keep the legacy registration fields compatible by filling sensible values.
	if strings.TrimSpace(input.DisplayName) == "" {
		input.DisplayName = input.Handle
	}
	if strings.TrimSpace(input.Locale) == "" {
		input.Locale = "en-US"
	}
	if strings.TrimSpace(input.Timezone) == "" {
		input.Timezone = "UTC"
	}
	id, err := uuid.Parse(strings.TrimSpace(input.ChallengeID))
	if err != nil {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_auth_challenge", "The authentication challenge is invalid.", false)
		return
	}
	user, token, err := s.authChallenges.CompleteRegistration(r.Context(), id, input.RegisterInput, input.Code, requestClientInfo(r, s.config.TrustedProxyCIDRs))
	switch {
	case errors.Is(err, authchallenges.ErrInvalid), errors.Is(err, identity.ErrInvalid):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_registration", "Use a valid code, a 3-30 character lowercase handle, a password of at least 10 characters, and a supported locale and IANA timezone.", false)
	case errors.Is(err, authchallenges.ErrInvalidCode):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_auth_code", "Enter the six-digit code from your email.", false)
	case errors.Is(err, authchallenges.ErrNotFound):
		httputil.WriteError(w, r, http.StatusNotFound, "auth_challenge_not_found", "The registration code is no longer available.", false)
	case errors.Is(err, authchallenges.ErrExpired):
		httputil.WriteError(w, r, http.StatusGone, "auth_code_expired", "That code has expired. Request a new one.", false)
	case errors.Is(err, authchallenges.ErrRateLimited):
		httputil.WriteError(w, r, http.StatusTooManyRequests, "auth_code_rate_limited", "Too many attempts. Request a new code.", true)
	case errors.Is(err, authchallenges.ErrConflict), errors.Is(err, identity.ErrConflict):
		httputil.WriteError(w, r, http.StatusConflict, "account_exists", "An account already uses this email or handle.", false)
	case errors.Is(err, systemsettings.ErrDisabled):
		httputil.WriteError(w, r, http.StatusServiceUnavailable, "feature_disabled", "New registrations are temporarily unavailable by an audited platform setting.", false)
	case err != nil:
		s.internalError(w, r, "complete unified registration", err)
	default:
		setSessionCookie(w, token, s.config.CookieSecure)
		httputil.JSON(w, http.StatusCreated, map[string]any{"user": user, "authentication": "email_code"})
	}
}

func (s *Server) updateProfile(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var input identity.ProfileInput
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	updated, err := s.identity.UpdateProfile(r.Context(), user.ID, input, httputil.RequestID(r.Context()))
	switch {
	case errors.Is(err, identity.ErrInvalid):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_profile", "Enter a display name, supported locale, and valid IANA timezone.", false)
	case errors.Is(err, identity.ErrNotFound):
		httputil.WriteError(w, r, http.StatusNotFound, "account_not_found", "The account could not be found.", false)
	case err != nil:
		s.internalError(w, r, "update profile", err)
	default:
		httputil.JSON(w, http.StatusOK, map[string]any{"user": updated})
	}
}

func (s *Server) listAccountSessions(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_session_filters", "Use a page size from 1 to 50 and an unmodified session cursor.", false)
			return
		}
		limit = parsed
	}
	page, err := s.identity.ListSessions(r.Context(), user.ID, sessionToken(r), identity.SessionListInput{Cursor: r.URL.Query().Get("cursor"), Limit: limit})
	if err != nil {
		if errors.Is(err, identity.ErrInvalidSessionFilter) {
			httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_session_filters", "Use a page size from 1 to 50 and an unmodified session cursor.", false)
			return
		}
		s.internalError(w, r, "list account sessions", err)
		return
	}
	httputil.JSON(w, http.StatusOK, page)
}

func (s *Server) revokeAccountSession(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	sessionID, err := uuid.Parse(chi.URLParam(r, "sessionID"))
	if err != nil {
		httputil.WriteError(w, r, http.StatusBadRequest, "invalid_session_id", "The session identifier is invalid.", false)
		return
	}
	current, err := s.identity.RevokeSession(r.Context(), user.ID, sessionID, sessionToken(r), httputil.RequestID(r.Context()))
	switch {
	case errors.Is(err, identity.ErrNotFound):
		httputil.WriteError(w, r, http.StatusNotFound, "session_not_found", "The session could not be found.", false)
	case err != nil:
		s.internalError(w, r, "revoke account session", err)
	default:
		if current {
			clearSessionCookie(w, s.config.CookieSecure)
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) revokeOtherAccountSessions(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	count, err := s.identity.RevokeOtherSessions(r.Context(), user.ID, sessionToken(r), httputil.RequestID(r.Context()))
	if err != nil {
		s.internalError(w, r, "revoke other account sessions", err)
		return
	}
	httputil.JSON(w, http.StatusOK, map[string]any{"revokedCount": count})
}

func (s *Server) listOAuthProviders(w http.ResponseWriter, r *http.Request) {
	items, err := s.identity.ListOAuthProviders(r.Context())
	if err != nil {
		s.internalError(w, r, "list oauth providers", err)
		return
	}
	httputil.JSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) startOAuth(w http.ResponseWriter, r *http.Request) {
	provider := chi.URLParam(r, "provider")
	if provider != "google" && provider != "github" {
		httputil.WriteError(w, r, http.StatusNotFound, "oauth_provider_not_found", "The OAuth provider is not supported.", false)
		return
	}
	httputil.WriteError(w, r, http.StatusServiceUnavailable, "oauth_provider_unavailable", "This sign-in provider is unavailable until its external credentials and staging verification are complete.", false)
}

func requestClientInfo(r *http.Request, trustedProxyCIDRs []netip.Prefix) identity.ClientInfo {
	return identity.ClientInfo{
		Label:       clientLabel(r.UserAgent()),
		NetworkHash: identity.HashNetwork(clientAddress(r, trustedProxyCIDRs)),
		RequestID:   httputil.RequestID(r.Context()),
	}
}

func clientLabel(userAgent string) string {
	platform := ""
	switch {
	case strings.Contains(userAgent, "Android"):
		platform = "Android"
	case strings.Contains(userAgent, "iPhone"), strings.Contains(userAgent, "iPad"):
		platform = "iOS"
	case strings.Contains(userAgent, "Macintosh"):
		platform = "macOS"
	case strings.Contains(userAgent, "Windows"):
		platform = "Windows"
	case strings.Contains(userAgent, "Linux"):
		platform = "Linux"
	}
	browser := "Browser"
	switch {
	case strings.Contains(userAgent, "Edg/"):
		browser = "Edge"
	case strings.Contains(userAgent, "Firefox/"):
		browser = "Firefox"
	case strings.Contains(userAgent, "Chrome/"):
		browser = "Chrome"
	case strings.Contains(userAgent, "Safari/"):
		browser = "Safari"
	case strings.Contains(strings.ToLower(userAgent), "curl/"):
		browser = "CLI"
	}
	if platform == "" {
		return browser
	}
	return browser + " on " + platform
}

func clientAddress(r *http.Request, trustedProxyCIDRs []netip.Prefix) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	remote, parseErr := netip.ParseAddr(host)
	if parseErr != nil || !isTrustedProxy(remote, trustedProxyCIDRs) {
		return host
	}
	for _, candidate := range strings.Split(r.Header.Get("X-Forwarded-For"), ",") {
		candidate = strings.TrimSpace(candidate)
		if parsed, err := netip.ParseAddr(candidate); err == nil {
			return parsed.String()
		}
	}
	return host
}

func isTrustedProxy(address netip.Addr, trusted []netip.Prefix) bool {
	for _, prefix := range trusted {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func setSessionCookie(w http.ResponseWriter, token string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: identity.SessionCookie, Value: token, Path: "/", MaxAge: 30 * 24 * 60 * 60,
		Expires: time.Now().Add(30 * 24 * time.Hour), HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: identity.SessionCookie, Value: "", Path: "/", MaxAge: -1,
		Expires: time.Unix(1, 0), HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
}
