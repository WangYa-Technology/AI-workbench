package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/hcai-chat/hcai-chat/internal/billing"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
	"github.com/hcai-chat/hcai-chat/internal/systemsettings"
)

func (s *Server) listProducts(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := s.marketplace.ListProducts(r.Context(), s.optionalViewer(r), marketplace.ListFilter{
		Query: r.URL.Query().Get("q"), ProductType: r.URL.Query().Get("type"),
		LicenseCode: r.URL.Query().Get("license"), Sort: r.URL.Query().Get("sort"), Limit: limit,
	})
	if err != nil {
		s.internalError(w, r, "list marketplace products", err)
		return
	}
	httputil.JSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getProduct(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "productID")
	if !ok {
		return
	}
	item, err := s.marketplace.GetProduct(r.Context(), s.optionalViewer(r), id)
	if errors.Is(err, marketplace.ErrNotFound) {
		httputil.WriteError(w, r, http.StatusNotFound, "product_not_found", "The requested product was not found.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "get marketplace product", err)
		return
	}
	httputil.JSON(w, http.StatusOK, item)
}

func (s *Server) purchaseProduct(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	id, ok := pathUUID(w, r, "productID")
	if !ok {
		return
	}
	var input struct {
		LicenseAccepted bool `json:"licenseAccepted"`
	}
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, created, err := s.marketplace.Purchase(r.Context(), user.ID, id, idempotencyKey(r), httputil.RequestID(r.Context()), input.LicenseAccepted)
	switch {
	case errors.Is(err, marketplace.ErrNotFound):
		httputil.WriteError(w, r, http.StatusNotFound, "product_not_found", "The requested product is not available.", false)
	case errors.Is(err, marketplace.ErrInvalidPurchase):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "license_acceptance_required", "Review and accept the displayed license before continuing.", false)
	case errors.Is(err, marketplace.ErrSellerPurchase):
		httputil.WriteError(w, r, http.StatusConflict, "seller_purchase_forbidden", "The seller account cannot purchase its own product.", false)
	case errors.Is(err, marketplace.ErrIdempotencyConflict):
		httputil.WriteError(w, r, http.StatusConflict, "idempotency_conflict", "This request key was already used for another purchase.", false)
	case errors.Is(err, billing.ErrInsufficientFunds):
		httputil.WriteError(w, r, http.StatusPaymentRequired, "insufficient_credits", "This Local Test account does not have enough available credits.", false)
	case errors.Is(err, systemsettings.ErrDisabled):
		httputil.WriteError(w, r, http.StatusServiceUnavailable, "feature_disabled", "Marketplace checkout is temporarily unavailable by an audited platform setting.", false)
	case err != nil:
		s.internalError(w, r, "purchase marketplace product", err)
	default:
		status := http.StatusOK
		if created {
			status = http.StatusCreated
			w.Header().Set("Location", "/api/v1/orders/"+item.OrderID.String())
		}
		httputil.JSON(w, status, item)
	}
}

func (s *Server) listOrders(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_order_filters", "Use a page size from 1 to 50 and an unmodified order cursor.", false)
			return
		}
		limit = parsed
	}
	page, err := s.marketplace.ListOrders(r.Context(), user.ID, marketplace.OrderListInput{Cursor: r.URL.Query().Get("cursor"), Limit: limit})
	if err != nil {
		if errors.Is(err, marketplace.ErrInvalidOrderFilter) {
			httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_order_filters", "Use a page size from 1 to 50 and an unmodified order cursor.", false)
			return
		}
		s.internalError(w, r, "list marketplace orders", err)
		return
	}
	httputil.JSON(w, http.StatusOK, page)
}

func (s *Server) getOrder(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	id, ok := pathUUID(w, r, "orderID")
	if !ok {
		return
	}
	item, err := s.marketplace.GetOrder(r.Context(), user.ID, id)
	s.writeOrderResult(w, r, item, err)
}

func (s *Server) refundOrder(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	id, ok := pathUUID(w, r, "orderID")
	if !ok {
		return
	}
	var input struct {
		Reason string `json:"reason"`
	}
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.marketplace.RequestRefund(r.Context(), user.ID, id, idempotencyKey(r), httputil.RequestID(r.Context()), input.Reason)
	s.writeOrderResult(w, r, item, err)
}

func (s *Server) writeOrderResult(w http.ResponseWriter, r *http.Request, item marketplace.Order, err error) {
	switch {
	case errors.Is(err, marketplace.ErrOrderNotFound):
		httputil.WriteError(w, r, http.StatusNotFound, "order_not_found", "The requested order was not found.", false)
	case errors.Is(err, marketplace.ErrInvalidRefund):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_refund", "Provide a refund reason between 10 and 500 characters and a valid request key.", false)
	case errors.Is(err, marketplace.ErrRefundWindowExpired):
		httputil.WriteError(w, r, http.StatusConflict, "refund_window_expired", "The displayed refund window has ended.", false)
	case errors.Is(err, marketplace.ErrRefundConflict):
		httputil.WriteError(w, r, http.StatusConflict, "refund_unavailable", "This order is not eligible for a refund in its current state.", false)
	case errors.Is(err, billing.ErrInsufficientFunds):
		httputil.WriteError(w, r, http.StatusConflict, "refund_balance_conflict", "The Local Test refund cannot be settled against the current account balance.", false)
	case err != nil:
		s.internalError(w, r, "marketplace order", err)
	default:
		httputil.JSON(w, http.StatusOK, item)
	}
}
