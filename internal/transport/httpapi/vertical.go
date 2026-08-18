package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/billing"
	"github.com/hcai-chat/hcai-chat/internal/community"
	"github.com/hcai-chat/hcai-chat/internal/creation"
	"github.com/hcai-chat/hcai-chat/internal/discovery"
	"github.com/hcai-chat/hcai-chat/internal/identity"
	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/systemsettings"
)

func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	httputil.JSON(w, http.StatusOK, map[string]any{"user": user, "authentication": "session_cookie"})
}

func (s *Server) demoLogin(w http.ResponseWriter, r *http.Request) {
	if s.config.Environment == "production" || !s.config.LocalProviderEnabled {
		httputil.WriteError(w, r, http.StatusNotFound, "not_found", "This endpoint is not available.", false)
		return
	}
	var input struct {
		Actor string `json:"actor"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil && !errors.Is(err, io.EOF) {
		httputil.WriteError(w, r, http.StatusBadRequest, "invalid_json", "The request body must be valid JSON.", false)
		return
	}
	if input.Actor != "" && input.Actor != "creator" && input.Actor != "publisher" && input.Actor != "admin" {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_demo_actor", "Choose creator, publisher, or admin for the local demo session.", false)
		return
	}
	user, token, err := s.identity.StartDemoSession(r.Context(), input.Actor, requestClientInfo(r, s.config.TrustedProxyCIDRs))
	if err != nil {
		s.internalError(w, r, "start demo session", err)
		return
	}
	setSessionCookie(w, token, s.config.CookieSecure)
	httputil.JSON(w, http.StatusOK, map[string]any{"user": user, "authentication": "local_demo"})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	token := sessionToken(r)
	if err := s.identity.Revoke(r.Context(), token); err != nil {
		s.internalError(w, r, "revoke session", err)
		return
	}
	clearSessionCookie(w, s.config.CookieSecure)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listWorks(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	var before *time.Time
	if cursor := strings.TrimSpace(r.URL.Query().Get("cursor")); cursor != "" {
		parsed, err := time.Parse(time.RFC3339Nano, cursor)
		if err != nil {
			httputil.WriteError(w, r, http.StatusBadRequest, "invalid_cursor", "The pagination cursor is invalid.", false)
			return
		}
		before = &parsed
	}
	page, err := s.discovery.List(r.Context(), limit, before)
	if err != nil {
		s.internalError(w, r, "list works", err)
		return
	}
	httputil.JSON(w, http.StatusOK, page)
}

func (s *Server) getWork(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "workID")
	if !ok {
		return
	}
	work, err := s.discovery.Get(r.Context(), id)
	if errors.Is(err, discovery.ErrNotFound) {
		httputil.WriteError(w, r, http.StatusNotFound, "work_not_found", "The requested work was not found.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "get work", err)
		return
	}
	httputil.JSON(w, http.StatusOK, work)
}

func (s *Server) searchDiscovery(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	types := make([]string, 0)
	for _, kind := range strings.Split(r.URL.Query().Get("types"), ",") {
		if kind = strings.TrimSpace(kind); kind != "" {
			types = append(types, kind)
		}
	}
	result, err := s.discovery.Search(r.Context(), discovery.SearchFilter{
		Query: r.URL.Query().Get("q"), Types: types, Page: page, Limit: limit,
	})
	if errors.Is(err, discovery.ErrInvalidQuery) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_search_query", "Enter 2 to 120 characters and choose only supported result types.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "search discovery", err)
		return
	}
	httputil.JSON(w, http.StatusOK, result)
}

func (s *Server) getCreator(w http.ResponseWriter, r *http.Request) {
	var viewerID uuid.UUID
	if token := sessionToken(r); token != "" {
		if user, err := s.identity.Authenticate(r.Context(), token); err == nil {
			viewerID = user.ID
		}
	}
	profile, err := s.discovery.Creator(r.Context(), chi.URLParam(r, "handle"), viewerID)
	if errors.Is(err, discovery.ErrNotFound) {
		httputil.WriteError(w, r, http.StatusNotFound, "creator_not_found", "The requested creator was not found.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "get creator", err)
		return
	}
	httputil.JSON(w, http.StatusOK, profile)
}

func (s *Server) submitGeneration(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var input creation.SubmitInput
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	generation, err := s.creation.SubmitCommand(r.Context(), user.ID, input, r.Header.Get("Idempotency-Key"), httputil.RequestID(r.Context()))
	switch {
	case errors.Is(err, creation.ErrInvalid), errors.Is(err, creation.ErrIdempotency):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_generation", "Choose an available creation mode, enter a prompt between 3 and 2,000 characters, and use output settings supported by that mode.", false)
		return
	case errors.Is(err, creation.ErrIdempotencyConflict):
		httputil.WriteError(w, r, http.StatusConflict, "idempotency_conflict", "This request key was already used for a different generation command.", false)
		return
	case errors.Is(err, billing.ErrInsufficientFunds):
		httputil.WriteError(w, r, http.StatusPaymentRequired, "insufficient_credits", "Add Local Test credits before starting this generation.", false)
		return
	case errors.Is(err, creation.ErrProviderOff):
		httputil.WriteError(w, r, http.StatusServiceUnavailable, "provider_unavailable", "No provider is available for this creation mode in the current environment.", true)
		return
	case errors.Is(err, systemsettings.ErrDisabled):
		httputil.WriteError(w, r, http.StatusServiceUnavailable, "feature_disabled", "Generation submission is temporarily unavailable by an audited platform setting.", false)
		return
	case err != nil:
		s.internalError(w, r, "submit generation", err)
		return
	}
	w.Header().Set("Location", "/api/v1/generations/"+generation.ID.String())
	httputil.JSON(w, http.StatusAccepted, generation)
}

func (s *Server) creationCapabilities(w http.ResponseWriter, r *http.Request) {
	capabilities, err := s.creation.Capabilities(r.Context())
	if err != nil {
		s.internalError(w, r, "load creation capabilities", err)
		return
	}
	httputil.JSON(w, http.StatusOK, capabilities)
}

func (s *Server) cancelGeneration(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	id, valid := pathUUID(w, r, "generationID")
	if !valid {
		return
	}
	var input struct {
		Reason string `json:"reason"`
	}
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.creation.Cancel(r.Context(), user.ID, id, r.Header.Get("Idempotency-Key"), httputil.RequestID(r.Context()), input.Reason)
	writeGenerationCommandResult(w, r, s, item, err)
}

func (s *Server) retryGeneration(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	id, valid := pathUUID(w, r, "generationID")
	if !valid {
		return
	}
	item, err := s.creation.Retry(r.Context(), user.ID, id, r.Header.Get("Idempotency-Key"), httputil.RequestID(r.Context()))
	if err == nil {
		w.Header().Set("Location", "/api/v1/generations/"+item.ID.String())
		httputil.JSON(w, http.StatusAccepted, item)
		return
	}
	writeGenerationCommandResult(w, r, s, item, err)
}

func (s *Server) favoriteGeneration(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	id, valid := pathUUID(w, r, "generationID")
	if !valid {
		return
	}
	var input struct {
		Active bool `json:"active"`
	}
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.creation.SetFavorite(r.Context(), user.ID, id, input.Active, httputil.RequestID(r.Context()))
	if errors.Is(err, creation.ErrNotFound) {
		httputil.WriteError(w, r, http.StatusNotFound, "generation_not_found", "The generation was not found.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "favorite generation", err)
		return
	}
	httputil.JSON(w, http.StatusOK, item)
}

func (s *Server) batchGenerations(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var input creation.GenerationBatchInput
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	result, err := s.creation.Batch(r.Context(), user.ID, input, r.Header.Get("Idempotency-Key"), httputil.RequestID(r.Context()))
	if errors.Is(err, creation.ErrInvalid) || errors.Is(err, creation.ErrIdempotency) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_generation_batch", "Select 1 to 50 unique generations and choose a supported batch action.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "batch generations", err)
		return
	}
	httputil.JSON(w, http.StatusOK, result)
}

func writeGenerationCommandResult(w http.ResponseWriter, r *http.Request, s *Server, item creation.Generation, err error) {
	switch {
	case errors.Is(err, creation.ErrNotFound):
		httputil.WriteError(w, r, http.StatusNotFound, "generation_not_found", "The generation was not found.", false)
	case errors.Is(err, creation.ErrForbidden):
		httputil.WriteError(w, r, http.StatusForbidden, "forbidden", "You cannot change this generation.", false)
	case errors.Is(err, creation.ErrConflict):
		httputil.WriteError(w, r, http.StatusConflict, "generation_state_conflict", "The generation is not in a state that allows this action.", false)
	case errors.Is(err, creation.ErrIdempotencyConflict):
		httputil.WriteError(w, r, http.StatusConflict, "idempotency_conflict", "This request key was already used for a different generation command.", false)
	case errors.Is(err, creation.ErrInvalid), errors.Is(err, creation.ErrIdempotency):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_generation_command", "Provide a valid command reason and idempotency key.", false)
	case errors.Is(err, creation.ErrProviderOff):
		httputil.WriteError(w, r, http.StatusServiceUnavailable, "provider_unavailable", "No provider is available for this creation mode in the current environment.", true)
	case errors.Is(err, billing.ErrInsufficientFunds):
		httputil.WriteError(w, r, http.StatusPaymentRequired, "insufficient_credits", "Add Local Test credits before retrying this generation.", false)
	case err != nil:
		s.internalError(w, r, "change generation", err)
	default:
		httputil.JSON(w, http.StatusOK, item)
	}
}

func (s *Server) billingStatement(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	input := billing.StatementInput{Direction: query.Get("direction"), EntryType: query.Get("entryType"), Cursor: query.Get("cursor")}
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_billing_filters", "Use a limit from 1 to 50.", false)
			return
		}
		input.Limit = limit
	}
	for raw, target := range map[string]**time.Time{"dateFrom": &input.DateFrom, "dateTo": &input.DateTo} {
		value := strings.TrimSpace(query.Get(raw))
		if value == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_billing_filters", "Use valid ISO 8601 dates and keep the start before the end.", false)
			return
		}
		*target = &parsed
	}
	statement, err := s.billing.Statement(r.Context(), user.ID, input)
	if errors.Is(err, billing.ErrAccountNotFound) {
		httputil.WriteError(w, r, http.StatusNotFound, "billing_account_not_found", "The billing account was not found.", false)
		return
	}
	if errors.Is(err, billing.ErrInvalidStatement) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_billing_filters", "Choose a supported direction and entry type, valid ISO 8601 dates, a limit from 1 to 50, and an unmodified cursor.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "get billing statement", err)
		return
	}
	httputil.JSON(w, http.StatusOK, statement)
}

func (s *Server) listGenerations(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	input := creation.GenerationListInput{
		Mode: query.Get("mode"), Status: query.Get("status"), Cursor: query.Get("cursor"),
	}
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_generation_filters", "Use a limit from 1 to 50.", false)
			return
		}
		input.Limit = limit
	}
	for raw, target := range map[string]**time.Time{"dateFrom": &input.DateFrom, "dateTo": &input.DateTo} {
		value := strings.TrimSpace(query.Get(raw))
		if value == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_generation_filters", "Use valid ISO 8601 dates and keep the start before the end.", false)
			return
		}
		*target = &parsed
	}
	page, err := s.creation.List(r.Context(), user.ID, input)
	if errors.Is(err, creation.ErrInvalid) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_generation_filters", "Choose a supported mode and status, valid ISO 8601 dates, a limit from 1 to 50, and an unmodified cursor.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "list generations", err)
		return
	}
	httputil.JSON(w, http.StatusOK, page)
}

func (s *Server) getGeneration(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	id, valid := pathUUID(w, r, "generationID")
	if !valid {
		return
	}
	generation, err := s.creation.Get(r.Context(), user.ID, id)
	switch {
	case errors.Is(err, creation.ErrNotFound):
		httputil.WriteError(w, r, http.StatusNotFound, "generation_not_found", "The generation was not found.", false)
	case errors.Is(err, creation.ErrForbidden):
		httputil.WriteError(w, r, http.StatusForbidden, "forbidden", "You cannot access this generation.", false)
	case err != nil:
		s.internalError(w, r, "get generation", err)
	default:
		httputil.JSON(w, http.StatusOK, generation)
	}
}

func (s *Server) listAssets(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	input, ok := parseAssetListInput(w, r, "invalid_asset_filters")
	if !ok {
		return
	}
	page, err := s.assets.List(r.Context(), user.ID, input)
	if errors.Is(err, assets.ErrInvalidList) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_asset_filters", "Use a page size from 1 to 50 and an unmodified Asset cursor.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "list assets", err)
		return
	}
	httputil.JSON(w, http.StatusOK, page)
}

func (s *Server) listSavedWorks(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	input, ok := parseAssetListInput(w, r, "invalid_saved_work_filters")
	if !ok {
		return
	}
	page, err := s.assets.ListSavedWorks(r.Context(), user.ID, input)
	if errors.Is(err, assets.ErrInvalidList) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_saved_work_filters", "Use a page size from 1 to 50 and an unmodified saved-work cursor.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "list saved works", err)
		return
	}
	httputil.JSON(w, http.StatusOK, page)
}

func (s *Server) listAssetUsages(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	assetID, ok := pathUUID(w, r, "assetID")
	if !ok {
		return
	}
	input, ok := parseAssetListInput(w, r, "invalid_asset_usage_filters")
	if !ok {
		return
	}
	page, err := s.assets.ListUsages(r.Context(), user.ID, assetID, input)
	switch {
	case errors.Is(err, assets.ErrInvalidList):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_asset_usage_filters", "Use a page size from 1 to 50 and an unmodified Asset usage cursor.", false)
	case errors.Is(err, assets.ErrNotFound):
		httputil.WriteError(w, r, http.StatusNotFound, "asset_not_found", "The asset was not found.", false)
	case errors.Is(err, assets.ErrForbidden):
		httputil.WriteError(w, r, http.StatusForbidden, "forbidden", "You cannot access this asset.", false)
	case err != nil:
		s.internalError(w, r, "list asset usages", err)
	default:
		httputil.JSON(w, http.StatusOK, page)
	}
}

func parseAssetListInput(w http.ResponseWriter, r *http.Request, code string) (assets.ListInput, bool) {
	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			httputil.WriteError(w, r, http.StatusUnprocessableEntity, code, "Use a page size from 1 to 50 and an unmodified cursor.", false)
			return assets.ListInput{}, false
		}
		limit = parsed
	}
	return assets.ListInput{Cursor: r.URL.Query().Get("cursor"), Limit: limit}, true
}

func (s *Server) uploadAsset(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requirePermission(w, r, "assets:upload")
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, assets.MaxUploadSize+(256<<10))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			httputil.WriteError(w, r, http.StatusRequestEntityTooLarge, "upload_too_large", "Upload one supported file no larger than 10 MiB.", false)
			return
		}
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_asset_upload", "Send a valid multipart upload with one title and one supported file.", false)
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "upload_file_required", "Choose one supported file to upload.", false)
		return
	}
	defer file.Close()
	item, err := s.assets.Upload(r.Context(), user.ID, assets.UploadInput{
		Title: r.FormValue("title"), Filename: header.Filename, Reader: file, RequestID: httputil.RequestID(r.Context()),
	})
	switch {
	case errors.Is(err, assets.ErrTooLarge):
		httputil.WriteError(w, r, http.StatusRequestEntityTooLarge, "upload_too_large", "Upload one supported file no larger than 10 MiB.", false)
	case errors.Is(err, assets.ErrInvalid):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_asset_upload", "Use a title of 3–120 characters and a supported JPEG, PNG, MP4, WAV, or plain-text file.", false)
	case err != nil:
		s.internalError(w, r, "upload asset", err)
	default:
		w.Header().Set("Location", "/api/v1/assets/"+item.ID.String())
		httputil.JSON(w, http.StatusCreated, item)
	}
}

func (s *Server) getAsset(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	id, ok := pathUUID(w, r, "assetID")
	if !ok {
		return
	}
	item, err := s.assets.GetOwned(r.Context(), user.ID, id)
	switch {
	case errors.Is(err, assets.ErrNotFound):
		httputil.WriteError(w, r, http.StatusNotFound, "asset_not_found", "The asset was not found.", false)
	case errors.Is(err, assets.ErrForbidden):
		httputil.WriteError(w, r, http.StatusForbidden, "forbidden", "You cannot access this asset.", false)
	case err != nil:
		s.internalError(w, r, "get asset", err)
	default:
		httputil.JSON(w, http.StatusOK, item)
	}
}

func (s *Server) uploadAssetVersion(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requirePermission(w, r, "assets:upload")
	if !ok {
		return
	}
	baseAssetID, ok := pathUUID(w, r, "assetID")
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, assets.MaxUploadSize+(256<<10))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			httputil.WriteError(w, r, http.StatusRequestEntityTooLarge, "upload_too_large", "Upload one supported file no larger than 10 MiB.", false)
			return
		}
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_asset_version", "Send a title, version note, and one supported replacement file.", false)
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "upload_file_required", "Choose one supported replacement file.", false)
		return
	}
	defer file.Close()
	item, err := s.assets.UploadVersion(r.Context(), user.ID, baseAssetID, assets.VersionInput{
		UploadInput: assets.UploadInput{Title: r.FormValue("title"), Filename: header.Filename, Reader: file, RequestID: httputil.RequestID(r.Context())},
		Note:        r.FormValue("note"),
	})
	switch {
	case errors.Is(err, assets.ErrNotFound):
		httputil.WriteError(w, r, http.StatusNotFound, "asset_not_found", "The base Asset was not found.", false)
	case errors.Is(err, assets.ErrForbidden):
		httputil.WriteError(w, r, http.StatusForbidden, "asset_version_forbidden", "You cannot create a version for this Asset.", false)
	case errors.Is(err, assets.ErrConflict):
		httputil.WriteError(w, r, http.StatusConflict, "asset_version_conflict", "Another Asset version was created first. Reload the version history.", true)
	case errors.Is(err, assets.ErrTooLarge):
		httputil.WriteError(w, r, http.StatusRequestEntityTooLarge, "upload_too_large", "Upload one supported file no larger than 10 MiB.", false)
	case errors.Is(err, assets.ErrInvalid):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_asset_version", "Use the same media kind, a title of 3–120 characters, and a version note of 3–500 characters.", false)
	case err != nil:
		s.internalError(w, r, "upload asset version", err)
	default:
		w.Header().Set("Location", "/api/v1/assets/"+item.ID.String())
		httputil.JSON(w, http.StatusCreated, item)
	}
}

func (s *Server) assetContent(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "assetID")
	if !ok {
		return
	}
	var viewerID uuid.UUID
	if token := sessionToken(r); token != "" {
		if user, err := s.identity.Authenticate(r.Context(), token); err == nil {
			viewerID = user.ID
		}
	}
	content, err := s.assets.Content(r.Context(), viewerID, id)
	switch {
	case errors.Is(err, assets.ErrNotFound):
		httputil.WriteError(w, r, http.StatusNotFound, "asset_not_found", "The asset content was not found.", false)
		return
	case errors.Is(err, assets.ErrForbidden):
		httputil.WriteError(w, r, http.StatusForbidden, "forbidden", "You cannot access this asset.", false)
		return
	case err != nil:
		s.internalError(w, r, "get asset content", err)
		return
	}
	info, err := content.Stat(r.Context())
	if errors.Is(err, media.ErrNotFound) {
		httputil.WriteError(w, r, http.StatusNotFound, "asset_content_missing", "The asset file is missing.", true)
		return
	}
	if err != nil {
		s.internalError(w, r, "stat asset content", err)
		return
	}
	requestedRange, err := parseSingleByteRange(r.Header.Get("Range"), info.Size)
	if err != nil {
		w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(info.Size, 10))
		httputil.WriteError(w, r, http.StatusRequestedRangeNotSatisfiable, "invalid_asset_range", "Request one valid byte range for this asset.", false)
		return
	}
	object, err := content.Open(r.Context(), requestedRange)
	if errors.Is(err, media.ErrNotFound) {
		httputil.WriteError(w, r, http.StatusNotFound, "asset_content_missing", "The asset file is missing.", true)
		return
	}
	if err != nil {
		s.internalError(w, r, "open asset content", err)
		return
	}
	defer object.Body.Close()
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Type", content.MimeType)
	w.Header().Set("Cache-Control", "private, max-age=60")
	if info.ETag != "" {
		w.Header().Set("ETag", info.ETag)
	}
	if !info.LastModified.IsZero() {
		w.Header().Set("Last-Modified", info.LastModified.UTC().Format(http.TimeFormat))
	}
	contentLength := info.Size
	status := http.StatusOK
	if requestedRange != nil {
		contentLength = requestedRange.End - requestedRange.Start + 1
		w.Header().Set("Content-Range", "bytes "+strconv.FormatInt(requestedRange.Start, 10)+"-"+strconv.FormatInt(requestedRange.End, 10)+"/"+strconv.FormatInt(info.Size, 10))
		status = http.StatusPartialContent
	}
	w.Header().Set("Content-Length", strconv.FormatInt(contentLength, 10))
	w.WriteHeader(status)
	if _, err := io.Copy(w, object.Body); err != nil {
		s.logger.Error("stream asset content", "assetId", id, "error", err)
	}
}

func parseSingleByteRange(value string, size int64) (*media.ByteRange, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if size <= 0 || !strings.HasPrefix(value, "bytes=") || strings.Contains(value, ",") {
		return nil, errors.New("invalid byte range")
	}
	parts := strings.Split(strings.TrimSpace(strings.TrimPrefix(value, "bytes=")), "-")
	if len(parts) != 2 || (parts[0] == "" && parts[1] == "") {
		return nil, errors.New("invalid byte range")
	}
	if parts[0] == "" {
		suffix, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || suffix <= 0 {
			return nil, errors.New("invalid byte range")
		}
		if suffix > size {
			suffix = size
		}
		return &media.ByteRange{Start: size - suffix, End: size - 1}, nil
	}
	start, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || start < 0 || start >= size {
		return nil, errors.New("invalid byte range")
	}
	end := size - 1
	if parts[1] != "" {
		end, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil || end < start {
			return nil, errors.New("invalid byte range")
		}
		if end >= size {
			end = size - 1
		}
	}
	return &media.ByteRange{Start: start, End: end}, nil
}

func (s *Server) publish(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var input community.PublishInput
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	publication, err := s.community.Publish(r.Context(), user.ID, input)
	switch {
	case errors.Is(err, community.ErrInvalid):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_publication", "Review the title, disclosure, prompt visibility, and text lengths.", false)
	case errors.Is(err, community.ErrForbidden):
		httputil.WriteError(w, r, http.StatusForbidden, "asset_not_publishable", "This asset cannot be published by the current account.", false)
	case errors.Is(err, community.ErrConflict):
		httputil.WriteError(w, r, http.StatusConflict, "asset_already_published", "This asset is already published.", false)
	case errors.Is(err, systemsettings.ErrDisabled):
		httputil.WriteError(w, r, http.StatusServiceUnavailable, "feature_disabled", "Publishing is temporarily unavailable by an audited platform setting.", false)
	case err != nil:
		s.internalError(w, r, "publish work", err)
	default:
		w.Header().Set("Location", "/works/"+publication.WorkID.String())
		httputil.JSON(w, http.StatusCreated, publication)
	}
}

func (s *Server) listContentDrafts(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_content_draft_filters", "Use a page size from 1 to 50 and an unmodified content-draft cursor.", false)
			return
		}
		limit = parsed
	}
	page, err := s.community.ListDrafts(r.Context(), user.ID, community.DraftListInput{Cursor: r.URL.Query().Get("cursor"), Limit: limit})
	if err != nil {
		if errors.Is(err, community.ErrInvalidDraftFilter) {
			httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_content_draft_filters", "Use a page size from 1 to 50 and an unmodified content-draft cursor.", false)
			return
		}
		s.internalError(w, r, "list content drafts", err)
		return
	}
	httputil.JSON(w, http.StatusOK, page)
}

func (s *Server) getContentDraft(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	draftID, ok := pathUUID(w, r, "draftID")
	if !ok {
		return
	}
	item, err := s.community.GetDraft(r.Context(), user.ID, draftID)
	writeContentDraft(w, r, s, item, err, http.StatusOK)
}

func (s *Server) createContentDraft(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var input community.DraftInput
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.community.SaveDraft(r.Context(), user.ID, nil, input, httputil.RequestID(r.Context()))
	writeContentDraft(w, r, s, item, err, http.StatusCreated)
}

func (s *Server) updateContentDraft(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	draftID, ok := pathUUID(w, r, "draftID")
	if !ok {
		return
	}
	var input community.DraftInput
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.community.SaveDraft(r.Context(), user.ID, &draftID, input, httputil.RequestID(r.Context()))
	writeContentDraft(w, r, s, item, err, http.StatusOK)
}

func (s *Server) publishContentDraft(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	draftID, ok := pathUUID(w, r, "draftID")
	if !ok {
		return
	}
	var input struct {
		ExpectedVersion int `json:"expectedVersion"`
	}
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	publication, err := s.community.PublishDraft(r.Context(), user.ID, draftID, input.ExpectedVersion, httputil.RequestID(r.Context()))
	switch {
	case errors.Is(err, community.ErrNotFound):
		httputil.WriteError(w, r, http.StatusNotFound, "content_draft_not_found", "The content draft was not found.", false)
	case errors.Is(err, community.ErrInvalid):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "content_draft_incomplete", "Complete the title and AI disclosure before publishing.", false)
	case errors.Is(err, community.ErrForbidden):
		httputil.WriteError(w, r, http.StatusForbidden, "asset_not_publishable", "The selected Asset must be owned, scan-clean, and publishable.", false)
	case errors.Is(err, community.ErrConflict):
		httputil.WriteError(w, r, http.StatusConflict, "content_draft_conflict", "The draft or selected Asset changed. Reload before publishing.", true)
	case errors.Is(err, systemsettings.ErrDisabled):
		httputil.WriteError(w, r, http.StatusServiceUnavailable, "feature_disabled", "Publishing is temporarily unavailable by an audited platform setting.", false)
	case err != nil:
		s.internalError(w, r, "publish content draft", err)
	default:
		w.Header().Set("Location", "/works/"+publication.WorkID.String())
		httputil.JSON(w, http.StatusCreated, publication)
	}
}

func (s *Server) discardContentDraft(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	draftID, ok := pathUUID(w, r, "draftID")
	if !ok {
		return
	}
	var input struct {
		ExpectedVersion int `json:"expectedVersion"`
	}
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	err := s.community.DiscardDraft(r.Context(), user.ID, draftID, input.ExpectedVersion, httputil.RequestID(r.Context()))
	switch {
	case errors.Is(err, community.ErrNotFound):
		httputil.WriteError(w, r, http.StatusNotFound, "content_draft_not_found", "The content draft was not found.", false)
	case errors.Is(err, community.ErrInvalid):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_content_draft", "Provide the current draft version.", false)
	case errors.Is(err, community.ErrConflict):
		httputil.WriteError(w, r, http.StatusConflict, "content_draft_conflict", "The draft changed. Reload before discarding it.", true)
	case err != nil:
		s.internalError(w, r, "discard content draft", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func writeContentDraft(w http.ResponseWriter, r *http.Request, s *Server, item community.Draft, err error, successStatus int) {
	switch {
	case errors.Is(err, community.ErrNotFound):
		httputil.WriteError(w, r, http.StatusNotFound, "content_draft_not_found", "The content draft was not found.", false)
	case errors.Is(err, community.ErrInvalid):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_content_draft", "Choose an owned Asset and review the draft field lengths.", false)
	case errors.Is(err, community.ErrForbidden):
		httputil.WriteError(w, r, http.StatusForbidden, "asset_not_draftable", "The selected Asset cannot be used in this private draft.", false)
	case errors.Is(err, community.ErrConflict):
		httputil.WriteError(w, r, http.StatusConflict, "content_draft_conflict", "This Asset already has a draft or the draft changed. Reload before saving.", true)
	case err != nil:
		s.internalError(w, r, "save content draft", err)
	default:
		if successStatus == http.StatusCreated {
			w.Header().Set("Location", "/api/v1/content-drafts/"+item.ID.String())
		}
		httputil.JSON(w, successStatus, item)
	}
}

func (s *Server) listPosts(w http.ResponseWriter, r *http.Request) {
	input := community.PostListInput{Cursor: r.URL.Query().Get("cursor")}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_post_filters", "Use a page size from 1 to 50 and an unmodified Community feed cursor.", false)
			return
		}
		input.Limit = limit
	}
	page, err := s.community.ListPageForViewer(r.Context(), s.optionalViewer(r), input)
	if errors.Is(err, community.ErrInvalidPostFilter) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_post_filters", "Use a page size from 1 to 50 and an unmodified Community feed cursor.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "list community posts", err)
		return
	}
	httputil.JSON(w, http.StatusOK, page)
}

func (s *Server) requireUser(w http.ResponseWriter, r *http.Request) (identity.User, bool) {
	user, err := s.identity.Authenticate(r.Context(), sessionToken(r))
	if errors.Is(err, identity.ErrUnauthenticated) {
		httputil.WriteError(w, r, http.StatusUnauthorized, "authentication_required", "Sign in to continue.", false)
		return identity.User{}, false
	}
	if err != nil {
		s.internalError(w, r, "authenticate request", err)
		return identity.User{}, false
	}
	return user, true
}

func sessionToken(r *http.Request) string {
	cookie, err := r.Cookie(identity.SessionCookie)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func pathUUID(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		httputil.WriteError(w, r, http.StatusBadRequest, "invalid_id", "The resource identifier is invalid.", false)
		return uuid.Nil, false
	}
	return id, true
}

func (s *Server) internalError(w http.ResponseWriter, r *http.Request, operation string, err error) {
	s.logger.Error(operation, "request_id", httputil.RequestID(r.Context()), "error", err)
	httputil.WriteError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.", true)
}
