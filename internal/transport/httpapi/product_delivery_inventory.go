package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
)

func (s *Server) listProductDeliveryEvidenceGaps(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	actor, ok := s.requirePermission(w, r, "admin:media")
	if !ok {
		return
	}
	f := productdelivery.EvidenceGapFilter{}
	invalid := func() {
		httputil.WriteError(w, r, 422, "invalid_delivery_evidence_filter", "Use a valid evidence filter and its matching cursor.", false)
	}
	for key, values := range r.URL.Query() {
		if len(values) != 1 {
			invalid()
			return
		}
		switch key {
		case "gap":
			f.Gap = values[0]
		case "environment":
			f.Environment = values[0]
		case "scope":
			f.Scope = values[0]
		case "cursor":
			f.Cursor = values[0]
		case "limit":
			n, err := strconv.Atoi(values[0])
			if err != nil || n < 1 || n > 50 {
				invalid()
				return
			}
			f.Limit = n
		default:
			invalid()
			return
		}
	}
	page, err := s.deliveryRepairs.ListEvidenceGaps(r.Context(), actor.ID, f)
	switch {
	case errors.Is(err, productdelivery.ErrInventoryFilter):
		invalid()
	case errors.Is(err, productdelivery.ErrRepairForbidden):
		httputil.WriteError(w, r, 403, "forbidden", "Media operations permission is required.", false)
	case err != nil:
		s.internalError(w, r, "list delivery evidence gaps", err)
	default:
		httputil.JSON(w, http.StatusOK, page)
	}
}
