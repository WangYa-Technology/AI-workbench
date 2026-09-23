package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/hcai-chat/hcai-chat/internal/billing"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/payments"
	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/systemsettings"
)

func (s *Server) listProducts(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if r.URL.Query().Has("limit") {
		parsed, err := strconv.Atoi(r.URL.Query().Get("limit"))
		if err != nil || parsed < 1 || parsed > 100 {
			httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_product_filters", "Use supported filters, a page size from 1 to 100, and an unmodified cursor.", false)
			return
		}
		limit = parsed
	}
	page, err := s.marketplace.ListProducts(r.Context(), s.optionalViewer(r), marketplace.ListFilter{
		Query: r.URL.Query().Get("q"), ProductType: r.URL.Query().Get("type"), Category: r.URL.Query().Get("category"),
		LicenseCode: r.URL.Query().Get("license"), Sort: r.URL.Query().Get("sort"), Limit: limit, Cursor: r.URL.Query().Get("cursor"),
	})
	if errors.Is(err, marketplace.ErrInvalidProductFilter) {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_product_filters", "Use supported filters, at most 120 search characters, and an unmodified cursor.", false)
		return
	}
	if err != nil {
		s.internalError(w, r, "list marketplace products", err)
		return
	}
	httputil.JSON(w, http.StatusOK, page)
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

func (s *Server) checkoutProduct(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	productID, ok := pathUUID(w, r, "productID")
	if !ok {
		return
	}
	var input struct {
		LicenseAccepted bool   `json:"licenseAccepted"`
		OfferVersion    string `json:"offerVersion"`
	}
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	origin := strings.TrimRight(s.config.WebOrigin, "/")
	item, created, err := s.payments.BeginProductCheckout(
		r.Context(), user.ID, productID, idempotencyKey(r), httputil.RequestID(r.Context()),
		origin+"/workspace/orders?payment=success", origin+"/market/assets/"+productID.String()+"?payment=cancelled", input.LicenseAccepted, input.OfferVersion,
	)
	switch {
	case errors.Is(err, payments.ErrDisabled), errors.Is(err, payments.ErrProviderUnavailable), errors.Is(err, payments.ErrProviderConfigMismatch):
		httputil.WriteError(w, r, http.StatusServiceUnavailable, "payment_provider_unavailable", "Payment checkout is not enabled.", false)
	case errors.Is(err, payments.ErrInvalidCheckout):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "payment_checkout_invalid", "Review the product, license acceptance, amount, and request key.", false)
	case errors.Is(err, payments.ErrAlreadyOwned):
		httputil.WriteError(w, r, http.StatusConflict, "product_already_owned", "This account already owns an active license for the product.", false)
	case errors.Is(err, payments.ErrOfferChanged):
		httputil.WriteError(w, r, http.StatusConflict, "product_offer_changed", "The offer changed. Reload the product and review its license before purchasing.", false)
	case errors.Is(err, payments.ErrCheckoutPreparation):
		httputil.WriteError(w, r, http.StatusConflict, "payment_checkout_preparation_failed", "The delivery could not be prepared. Open your orders to inspect or close checkout before trying again.", false)
	case errors.Is(err, payments.ErrCheckoutClosed):
		httputil.WriteError(w, r, http.StatusConflict, "payment_checkout_closed", "This order was closed before checkout started. Review the product before purchasing again.", false)
	case errors.Is(err, payments.ErrCheckoutExpired):
		httputil.WriteError(w, r, http.StatusConflict, "payment_checkout_expired", "The provider confirmed this checkout expired without payment. Review the product before starting a new checkout.", false)
	case errors.Is(err, payments.ErrCheckoutReconciliation):
		httputil.WriteError(w, r, http.StatusConflict, "payment_reconciliation_required", "The original checkout requires verification before it can be retried. Check your orders or contact support.", false)
	case errors.Is(err, payments.ErrCheckoutConflict):
		httputil.WriteError(w, r, http.StatusConflict, "payment_checkout_conflict", "The request key belongs to another checkout or the checkout changed.", false)
	case errors.Is(err, systemsettings.ErrDisabled):
		httputil.WriteError(w, r, http.StatusServiceUnavailable, "feature_disabled", "Marketplace checkout is temporarily unavailable by an audited platform setting.", false)
	case errors.Is(err, media.ErrStageBusy), errors.Is(err, media.ErrStageStorage):
		s.internalError(w, r, "prepare product delivery", err)
	case err != nil:
		var classified interface{ Retryable() bool }
		if errors.As(err, &classified) {
			httputil.WriteError(w, r, http.StatusServiceUnavailable, "payment_checkout_unavailable", "The payment Provider could not create checkout.", classified.Retryable())
			return
		}
		s.internalError(w, r, "create product payment checkout", err)
	default:
		status := http.StatusOK
		if created {
			status = http.StatusCreated
			w.Header().Set("Location", "/api/v1/orders/"+item.OrderID.String())
		}
		httputil.JSON(w, status, item)
	}
}

func (s *Server) setProductPreview(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	id, ok := pathUUID(w, r, "productID")
	if !ok {
		return
	}
	var body struct {
		PreviewAssetID json.RawMessage `json:"previewAssetId"`
		OfferVersion   string          `json:"offerVersion"`
	}
	if !httputil.DecodeJSON(w, r, &body) {
		return
	}
	input := marketplace.PreviewUpdate{OfferVersion: body.OfferVersion}
	if len(body.PreviewAssetID) == 0 || json.Unmarshal(body.PreviewAssetID, &input.PreviewAssetID) != nil {
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_product_preview", "Provide a preview asset ID or explicit null to remove the sample.", false)
		return
	}
	item, err := s.marketplace.SetPreview(r.Context(), user.ID, id, input, httputil.RequestID(r.Context()))
	switch {
	case errors.Is(err, marketplace.ErrNotFound):
		httputil.WriteError(w, r, http.StatusNotFound, "product_not_found", "The product was not found.", false)
	case errors.Is(err, marketplace.ErrInvalidPreview):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_product_preview", "Choose your own clean, separate preview asset and a valid offer version.", false)
	case errors.Is(err, marketplace.ErrPreviewConflict):
		httputil.WriteError(w, r, http.StatusConflict, "product_offer_changed", "The product offer has changed. Reload before updating the preview.", false)
	case errors.Is(err, systemsettings.ErrDisabled):
		httputil.WriteError(w, r, http.StatusServiceUnavailable, "feature_disabled", "Publishing is temporarily unavailable by an audited platform setting.", false)
	case err != nil:
		s.internalError(w, r, "update product preview", err)
	default:
		httputil.JSON(w, http.StatusOK, item)
	}
}

func (s *Server) listOrders(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
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
	for i := range page.Items {
		s.projectOrderRefundCapability(&page.Items[i])
	}
	httputil.JSON(w, http.StatusOK, page)
}

func (s *Server) projectOrderRefundCapability(item *marketplace.Order) {
	if item.CanRequestRefund && item.PaymentID != nil && !s.payments.CanRefundProduct(item.PaymentMode) {
		item.CanRequestRefund = false
		item.RefundUnavailableReason = "provider_unavailable"
	}
}

func (s *Server) getOrder(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
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
	current, err := s.marketplace.GetOrder(r.Context(), user.ID, id)
	if err != nil {
		s.writeOrderResult(w, r, current, err)
		return
	}
	// Historical internal purchases require original balanced accounting evidence.
	// Every configured external provider (Stripe, Waffo, and future providers)
	// must go through the provider refund workflow so the entitlement remains
	// active until a signed refund event confirms completion.
	if current.PaymentID != nil {
		_, err = s.payments.BeginProductRefund(r.Context(), user.ID, id, idempotencyKey(r), httputil.RequestID(r.Context()), input.Reason)
		if err != nil {
			s.writeProviderRefundError(w, r, err)
			return
		}
		item, err := s.marketplace.GetOrder(r.Context(), user.ID, id)
		s.writeOrderResult(w, r, item, err)
		return
	}
	item, err := s.marketplace.RefundLegacyOrder(r.Context(), user.ID, id, idempotencyKey(r), httputil.RequestID(r.Context()), input.Reason)
	s.writeOrderResult(w, r, item, err)
}

func (s *Server) writeProviderRefundError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, payments.ErrDisabled), errors.Is(err, payments.ErrProviderUnavailable), errors.Is(err, payments.ErrProviderConfigMismatch):
		httputil.WriteError(w, r, http.StatusServiceUnavailable, "payment_provider_unavailable", "Payment refunds are not enabled.", false)
	case errors.Is(err, payments.ErrInvalidRefund):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_refund", "Provide a refund reason between 10 and 500 characters and a valid request key.", false)
	case errors.Is(err, payments.ErrRefundExpired):
		httputil.WriteError(w, r, http.StatusConflict, "refund_window_expired", "The displayed refund window has ended.", false)
	case errors.Is(err, payments.ErrRefundConflict):
		httputil.WriteError(w, r, http.StatusConflict, "refund_unavailable", "This order is not eligible for a refund in its current state.", false)
	default:
		var classified interface{ Retryable() bool }
		if errors.As(err, &classified) {
			httputil.WriteError(w, r, http.StatusServiceUnavailable, "payment_refund_unavailable", "The payment Provider could not start the refund.", classified.Retryable())
			return
		}
		s.internalError(w, r, "create product payment refund", err)
	}
}

func (s *Server) writeOrderResult(w http.ResponseWriter, r *http.Request, item marketplace.Order, err error) {
	switch {
	case errors.Is(err, marketplace.ErrOrderNotFound):
		httputil.WriteError(w, r, http.StatusNotFound, "order_not_found", "The requested order was not found.", false)
	case errors.Is(err, marketplace.ErrLegacyRefundEvidence):
		httputil.WriteError(w, r, http.StatusConflict, "legacy_refund_reconciliation_required", "Original internal payment evidence must be reconciled before reversal.", false)
	case errors.Is(err, marketplace.ErrIdempotencyConflict):
		httputil.WriteError(w, r, http.StatusConflict, "idempotency_conflict", "This command key is already bound to a different request.", false)
	case errors.Is(err, marketplace.ErrInvalidRefund):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_refund", "Provide a refund reason between 10 and 500 characters and a valid request key.", false)
	case errors.Is(err, marketplace.ErrRefundWindowExpired):
		httputil.WriteError(w, r, http.StatusConflict, "refund_window_expired", "The displayed refund window has ended.", false)
	case errors.Is(err, marketplace.ErrRefundConflict):
		httputil.WriteError(w, r, http.StatusConflict, "refund_unavailable", "This order is not eligible for a refund in its current state.", false)
	case errors.Is(err, billing.ErrInsufficientFunds):
		httputil.WriteError(w, r, http.StatusConflict, "refund_balance_conflict", "The historical internal reversal cannot be settled against the original payee balance.", false)
	case err != nil:
		s.internalError(w, r, "marketplace order", err)
	default:
		s.projectOrderRefundCapability(&item)
		httputil.JSON(w, http.StatusOK, item)
	}
}

func (s *Server) closeProductCheckout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	id, ok := pathUUID(w, r, "orderID")
	if !ok {
		return
	}
	var input payments.CloseProductCheckoutInput
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	err := s.payments.CloseProductCheckout(r.Context(), user.ID, id, idempotencyKey(r), httputil.RequestID(r.Context()), input)
	switch {
	case errors.Is(err, payments.ErrCheckoutOrderNotFound):
		httputil.WriteError(w, r, http.StatusNotFound, "order_not_found", "The requested order was not found.", false)
	case errors.Is(err, payments.ErrCheckoutCloseInvalid):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_checkout_closure", "Confirm closing this checkout and provide its current version and request key.", false)
	case errors.Is(err, payments.ErrCheckoutCloseConflict):
		httputil.WriteError(w, r, http.StatusConflict, "checkout_closure_unavailable", "The checkout changed or may have reached payment. Refresh your order before continuing.", false)
	case err != nil:
		s.internalError(w, r, "close product checkout", err)
	default:
		item, err := s.marketplace.GetOrder(r.Context(), user.ID, id)
		s.writeOrderResult(w, r, item, err)
	}
}
