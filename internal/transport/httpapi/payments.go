package httpapi

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/hcai-chat/hcai-chat/internal/payments"
	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
)

const maxPaymentWebhookBytes int64 = 1024 * 1024

func (s *Server) getPayoutStatus(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	status, err := s.payments.GetPayoutStatus(r.Context(), user.ID)
	if err != nil {
		s.internalError(w, r, "get payout status", err)
		return
	}
	httputil.JSON(w, http.StatusOK, status)
}

func (s *Server) beginPayoutOnboarding(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	origin := strings.TrimRight(s.config.WebOrigin, "/")
	item, err := s.payments.BeginPayoutOnboarding(
		r.Context(), user.ID,
		origin+"/settings?section=payouts&connect=refresh",
		origin+"/settings?section=payouts&connect=return",
		httputil.RequestID(r.Context()),
	)
	switch {
	case errors.Is(err, payments.ErrDisabled), errors.Is(err, payments.ErrProviderUnavailable):
		httputil.WriteError(w, r, http.StatusServiceUnavailable, "payment_provider_unavailable", "Creator payouts are not enabled in this environment.", false)
	case errors.Is(err, payments.ErrPayoutNotFound):
		httputil.WriteError(w, r, http.StatusNotFound, "account_not_found", "The creator account could not be found.", false)
	case errors.Is(err, payments.ErrPayoutConflict):
		httputil.WriteError(w, r, http.StatusConflict, "payout_onboarding_conflict", "The payout account changed. Refresh this page and review its current status.", false)
	case errors.Is(err, payments.ErrPayoutReconciliation):
		httputil.WriteError(w, r, http.StatusConflict, "payment_reconciliation_required", "The original payout account request must be reconciled before onboarding can continue.", false)
	case err != nil:
		var classified interface{ Retryable() bool }
		if errors.As(err, &classified) {
			httputil.WriteError(w, r, http.StatusServiceUnavailable, "payout_onboarding_unavailable", "The payout Provider could not start onboarding.", classified.Retryable())
			return
		}
		s.internalError(w, r, "begin payout onboarding", err)
	default:
		httputil.JSON(w, http.StatusCreated, item)
	}
}

func (s *Server) receiveStripeWebhook(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		httputil.WriteError(w, r, http.StatusUnsupportedMediaType, "payment_webhook_content_type_invalid", "Send a signed JSON payment event.", false)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPaymentWebhookBytes)
	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			httputil.WriteError(w, r, http.StatusRequestEntityTooLarge, "payment_webhook_too_large", "The payment event exceeds the accepted size.", false)
			return
		}
		s.internalError(w, r, "read Stripe webhook", err)
		return
	}
	receipt, err := s.payments.ReceiveStripeWebhook(r.Context(), rawBody, r.Header.Get("Stripe-Signature"))
	switch {
	case errors.Is(err, payments.ErrWebhookAdmissionBusy):
		httputil.WriteError(w, r, http.StatusServiceUnavailable, "payment_event_busy", "The payment event is being processed. Retry this delivery.", true)
	case errors.Is(err, payments.ErrDisabled):
		httputil.WriteError(w, r, http.StatusServiceUnavailable, "payment_provider_unavailable", "Payment processing is not enabled.", false)
	case errors.Is(err, payments.ErrInvalidSignature):
		httputil.WriteError(w, r, http.StatusBadRequest, "payment_webhook_signature_invalid", "The payment event signature is invalid.", false)
	case errors.Is(err, payments.ErrInvalidEvent):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "payment_event_invalid", "The payment event envelope is invalid.", false)
	case errors.Is(err, payments.ErrVersionMismatch):
		httputil.WriteError(w, r, http.StatusConflict, "payment_event_version_mismatch", "The payment event API version does not match the configured contract.", false)
	case errors.Is(err, payments.ErrModeMismatch):
		httputil.WriteError(w, r, http.StatusConflict, "payment_event_mode_mismatch", "The payment event test or live mode does not match this environment.", false)
	case errors.Is(err, payments.ErrEventConflict):
		httputil.WriteError(w, r, http.StatusConflict, "payment_event_conflict", "The payment event identifier conflicts with earlier evidence.", false)
	case err != nil:
		s.internalError(w, r, "receive Stripe webhook", err)
	default:
		status := http.StatusAccepted
		if receipt.Duplicate {
			status = http.StatusOK
		}
		httputil.JSON(w, status, receipt)
	}
}

func (s *Server) receiveWaffoWebhook(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		httputil.WriteError(w, r, http.StatusUnsupportedMediaType, "payment_webhook_content_type_invalid", "Send a signed JSON payment event.", false)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPaymentWebhookBytes)
	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			httputil.WriteError(w, r, http.StatusRequestEntityTooLarge, "payment_webhook_too_large", "The payment event exceeds the accepted size.", false)
			return
		}
		s.internalError(w, r, "read Waffo webhook", err)
		return
	}
	receipt, err := s.payments.ReceiveWaffoWebhook(r.Context(), rawBody, r.Header.Get("x-waffo-signature"))
	switch {
	case errors.Is(err, payments.ErrWebhookAdmissionBusy):
		httputil.WriteError(w, r, http.StatusServiceUnavailable, "payment_event_busy", "The payment event is being processed. Retry this delivery.", true)
	case errors.Is(err, payments.ErrDisabled):
		httputil.WriteError(w, r, http.StatusServiceUnavailable, "payment_provider_unavailable", "Payment processing is not enabled.", false)
	case errors.Is(err, payments.ErrProviderConfigMismatch):
		httputil.WriteError(w, r, http.StatusServiceUnavailable, "payment_provider_unavailable", "Payment Provider configuration is incomplete or does not match the deployment.", false)
	case errors.Is(err, payments.ErrInvalidSignature):
		httputil.WriteError(w, r, http.StatusUnauthorized, "payment_webhook_signature_invalid", "The payment event signature is invalid.", false)
	case errors.Is(err, payments.ErrInvalidEvent):
		httputil.WriteError(w, r, http.StatusUnprocessableEntity, "payment_event_invalid", "The payment event envelope is invalid.", false)
	case errors.Is(err, payments.ErrEventConflict):
		httputil.WriteError(w, r, http.StatusConflict, "payment_event_conflict", "The payment event identifier conflicts with earlier evidence.", false)
	case errors.Is(err, payments.ErrCheckoutReconciliation):
		httputil.WriteError(w, r, http.StatusConflict, "payment_reconciliation_required", "The original transaction identity requires verification.", false)
	case err != nil:
		var retryable interface{ Retryable() bool }
		if errors.As(err, &retryable) && retryable.Retryable() {
			httputil.WriteError(w, r, http.StatusServiceUnavailable, "payment_provider_unavailable", "The payment Provider verification service is unavailable.", true)
			return
		}
		s.internalError(w, r, "receive Waffo webhook", err)
	default:
		status := http.StatusAccepted
		if receipt.Duplicate {
			status = http.StatusOK
		}
		httputil.JSON(w, status, receipt)
	}
}
