package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/assets"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
	"github.com/hcai-chat/hcai-chat/internal/systemsettings"
)

func (s *Server) listingUser(w http.ResponseWriter, r *http.Request, review bool) (uuid.UUID, bool) {
	w.Header().Set("Cache-Control", "private, no-store")
	if review {
		u, ok := s.requirePermission(w, r, "admin:content")
		return u.ID, ok
	}
	u, ok := s.requireUser(w, r)
	return u.ID, ok
}
func (s *Server) listSellerProducts(w http.ResponseWriter, r *http.Request) {
	s.listProductListings(w, r, false)
}
func (s *Server) listReviewProducts(w http.ResponseWriter, r *http.Request) {
	s.listProductListings(w, r, true)
}
func (s *Server) listProductListings(w http.ResponseWriter, r *http.Request, review bool) {
	actor, ok := s.listingUser(w, r, review)
	if !ok {
		return
	}
	f := marketplace.ListingFilter{Status: r.URL.Query().Get("status"), Cursor: r.URL.Query().Get("cursor")}
	for key, values := range r.URL.Query() {
		if len(values) != 1 || (key != "status" && key != "cursor" && key != "limit") {
			s.writeListingResult(w, r, nil, marketplace.ErrInvalidListing)
			return
		}
	}
	if values, exists := r.URL.Query()["limit"]; exists {
		v, e := strconv.Atoi(values[0])
		if e != nil || v < 1 || v > 50 {
			s.writeListingResult(w, r, nil, marketplace.ErrInvalidListing)
			return
		}
		f.Limit = v
	}
	page, err := s.marketplace.ListListings(r.Context(), actor, review, f)
	s.writeListingResult(w, r, page, err)
}
func (s *Server) getSellerProduct(w http.ResponseWriter, r *http.Request) {
	s.getProductListing(w, r, false)
}
func (s *Server) getReviewProduct(w http.ResponseWriter, r *http.Request) {
	s.getProductListing(w, r, true)
}
func (s *Server) getProductListing(w http.ResponseWriter, r *http.Request, review bool) {
	actor, ok := s.listingUser(w, r, review)
	if !ok {
		return
	}
	id, ok := pathUUID(w, r, "productID")
	if !ok {
		return
	}
	item, err := s.marketplace.GetListing(r.Context(), actor, id, review)
	s.writeListingResult(w, r, item, err)
}
func (s *Server) listSellerLicenses(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.listingUser(w, r, false); !ok {
		return
	}
	items, err := s.marketplace.ListingLicenses(r.Context())
	s.writeListingResult(w, r, map[string]any{"items": items}, err)
}
func (s *Server) createSellerProduct(w http.ResponseWriter, r *http.Request) {
	s.mutateProductListing(w, r, "create", false)
}
func (s *Server) editSellerProduct(w http.ResponseWriter, r *http.Request) {
	s.mutateProductListing(w, r, "edit", false)
}
func (s *Server) actSellerProduct(w http.ResponseWriter, r *http.Request) {
	action := chi.URLParam(r, "action")
	if action != "submit" && action != "pause" {
		http.NotFound(w, r)
		return
	}
	s.mutateProductListing(w, r, action, false)
}
func (s *Server) actReviewProduct(w http.ResponseWriter, r *http.Request) {
	action := chi.URLParam(r, "action")
	if action != "approve" && action != "reject" && action != "block" && action != "reopen" {
		http.NotFound(w, r)
		return
	}
	s.mutateProductListing(w, r, action, true)
}
func (s *Server) mutateProductListing(w http.ResponseWriter, r *http.Request, action string, review bool) {
	actor, ok := s.listingUser(w, r, review)
	if !ok {
		return
	}
	var id uuid.UUID
	if action != "create" {
		id, ok = pathUUID(w, r, "productID")
		if !ok {
			return
		}
	}
	var input marketplace.ListingMutation
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.marketplace.MutateListing(r.Context(), actor, id, action, idempotencyKey(r), httputil.RequestID(r.Context()), input)
	s.writeListingResult(w, r, item, err)
}
func (s *Server) writeListingResult(w http.ResponseWriter, r *http.Request, item any, err error) {
	switch {
	case errors.Is(err, marketplace.ErrNotFound):
		httputil.WriteError(w, r, 404, "product_not_found", "The product was not found.", false)
	case errors.Is(err, marketplace.ErrListingForbidden):
		httputil.WriteError(w, r, 403, "forbidden", "This product action is not permitted.", false)
	case errors.Is(err, marketplace.ErrInvalidListing):
		httputil.WriteError(w, r, 422, "invalid_product_listing", "Check the listing fields, current version, confirmations and request key.", false)
	case errors.Is(err, marketplace.ErrListingSource):
		httputil.WriteError(w, r, 422, "product_source_unavailable", "Choose a clean independent private original and a separate eligible sample.", false)
	case errors.Is(err, marketplace.ErrListingConflict), errors.Is(err, marketplace.ErrIdempotencyConflict):
		httputil.WriteError(w, r, 409, "product_listing_conflict", "Refresh the product; its content, review or command changed.", false)
	case errors.Is(err, systemsettings.ErrDisabled):
		httputil.WriteError(w, r, 503, "feature_disabled", "Publishing is temporarily unavailable.", false)
	case err != nil:
		s.internalError(w, r, "product publication", err)
	default:
		httputil.JSON(w, http.StatusOK, item)
	}
}

func (s *Server) reviewProductContent(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.listingUser(w, r, true)
	if !ok {
		return
	}
	id, ok := pathUUID(w, r, "productID")
	if !ok {
		return
	}
	for key, values := range r.URL.Query() {
		if len(values) != 1 || (key != "kind" && key != "version" && key != "fileIndex") {
			s.writeListingResult(w, r, nil, marketplace.ErrInvalidListing)
			return
		}
	}
	var index *int
	if values, exists := r.URL.Query()["fileIndex"]; exists {
		v, err := strconv.Atoi(values[0])
		if err != nil || strconv.Itoa(v) != values[0] {
			s.writeListingResult(w, r, nil, marketplace.ErrInvalidListing)
			return
		}
		index = &v
	}
	owner, file, err := s.marketplace.ReviewListingFileAt(r.Context(), actor, id, r.URL.Query().Get("kind"), r.URL.Query().Get("version"), httputil.RequestID(r.Context()), index)
	if err != nil {
		s.writeListingResult(w, r, nil, err)
		return
	}
	content, err := s.assets.Content(r.Context(), owner, file)
	content = content.WithAuthorization(func(ctx context.Context) error {
		err := s.marketplace.RecheckListingFile(ctx, actor, id, owner, file, r.URL.Query().Get("kind"), r.URL.Query().Get("version"), index)
		if errors.Is(err, marketplace.ErrListingForbidden) {
			return assets.ErrForbidden
		}
		return err
	})
	w.Header().Set("Content-Disposition", `attachment; filename="`+file.String()+`"`)
	s.writeAssetContent(w, r, file, content, err)
}
