package httpapi

import (
	"errors"
	"net/http"

	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
)

var productDisputeQueryParameters = map[string]struct{}{
	"q":            {},
	"actionStatus": {},
	"reviewStatus": {},
	"binding":      {},
	"mode":         {},
	"cursor":       {},
	"limit":        {},
}

func (s *Server) adminListProductPaymentDisputes(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:finance")
	if !ok {
		return
	}
	for name, values := range r.URL.Query() {
		if _, supported := productDisputeQueryParameters[name]; !supported || len(values) != 1 {
			writeInvalidProductDisputeFilters(w, r)
			return
		}
	}
	limit, ok := adminDirectoryLimit(w, r, "invalid_admin_product_dispute_filters", "product dispute")
	if !ok {
		return
	}
	page, err := s.admin.ListProductPaymentDisputes(r.Context(), actor.ID, admin.ProductPaymentDisputeListInput{
		Query:        r.URL.Query().Get("q"),
		ActionStatus: r.URL.Query().Get("actionStatus"),
		ReviewStatus: r.URL.Query().Get("reviewStatus"),
		Binding:      r.URL.Query().Get("binding"),
		Mode:         r.URL.Query().Get("mode"),
		Cursor:       r.URL.Query().Get("cursor"),
		Limit:        limit,
	})
	if errors.Is(err, admin.ErrInvalidProductDisputeFilter) {
		writeInvalidProductDisputeFilters(w, r)
		return
	}
	if err != nil {
		s.writeAdminResult(w, r, page, err)
		return
	}
	httputil.JSON(w, http.StatusOK, page)
}

func (s *Server) adminGetProductPaymentDispute(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:finance")
	if !ok {
		return
	}
	if len(r.URL.Query()) != 0 {
		writeInvalidProductDisputeFilters(w, r)
		return
	}
	disputeID, valid := pathUUID(w, r, "disputeID")
	if !valid {
		return
	}
	item, err := s.admin.GetProductPaymentDispute(r.Context(), actor.ID, disputeID)
	s.writeAdminResult(w, r, item, err)
}

func (s *Server) adminOperateProductPaymentDispute(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:finance")
	if !ok {
		return
	}
	if len(r.URL.Query()) != 0 || len(r.Header.Values("Idempotency-Key")) != 1 {
		writeInvalidProductDisputeOperation(w, r)
		return
	}
	disputeID, valid := pathUUID(w, r, "disputeID")
	if !valid {
		return
	}
	var input admin.ProductPaymentDisputeCommand
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.admin.OperateProductPaymentDispute(
		r.Context(), actor.ID, disputeID, input, idempotencyKey(r), httputil.RequestID(r.Context()),
	)
	if errors.Is(err, admin.ErrInvalid) {
		writeInvalidProductDisputeOperation(w, r)
		return
	}
	s.writeAdminResult(w, r, item, err)
}

func writeInvalidProductDisputeFilters(w http.ResponseWriter, r *http.Request) {
	httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_product_dispute_filters", "Use supported product dispute filters, a page size from 1 to 50, and an unmodified cursor.", false)
}

func writeInvalidProductDisputeOperation(w http.ResponseWriter, r *http.Request) {
	httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_admin_product_dispute_operation", "Provide a supported action, route, current version, confirmation, reason, and one valid Idempotency-Key.", false)
}
