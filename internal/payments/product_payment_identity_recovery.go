package payments

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5"
)

const ProductIdentityRecoveryJobKind = "payment.verify_product_identity"

const productPaymentBindingSelect = `SELECT id,resource_id,order_id,payer_id,amount_cents,currency,live_mode,
 COALESCE(provider_checkout_id,''),COALESCE(provider_payment_id,''),COALESCE(provider_charge_id,'')
 FROM payment_intents WHERE id=$1 AND purpose='product' AND provider='stripe'`

func readProductPaymentBinding(ctx context.Context, db productIdentityQuery, paymentID uuid.UUID, lock bool) (ProductPaymentBinding, error) {
	return readProductPaymentBindingForProvider(ctx, db, paymentID, "stripe", lock)
}

func readProductPaymentBindingForProvider(ctx context.Context, db productIdentityQuery, paymentID uuid.UUID, provider string, lock bool) (ProductPaymentBinding, error) {
	var b ProductPaymentBinding
	if !oneOf(provider, "stripe", "waffo_pancake") {
		return b, ErrCheckoutReconciliation
	}
	query := `SELECT id,resource_id,order_id,payer_id,amount_cents,currency,live_mode,
 COALESCE(provider_checkout_id,''),COALESCE(provider_payment_id,''),COALESCE(provider_charge_id,'')
 FROM payment_intents WHERE id=$1 AND purpose='product' AND provider=$2`
	if lock {
		query += " FOR UPDATE"
	}
	err := db.QueryRow(ctx, query, paymentID, provider).Scan(&b.PaymentID, &b.ResourceID, &b.OrderID, &b.BuyerID, &b.AmountCents, &b.Currency, &b.LiveMode, &b.ProviderCheckoutID, &b.ProviderPaymentID, &b.ProviderChargeID)
	return b, err
}

func verifyRecoveredProductPaymentIdentity(ctx context.Context, db productIdentityQuery, runtime ProviderRuntime, paymentID uuid.UUID) (ProductCheckoutIdentity, productPaymentCustomer, error) {
	original, binding, err := readRecoveredProductPaymentIdentity(ctx, db, paymentID)
	if err != nil {
		return ProductCheckoutIdentity{}, productPaymentCustomer{}, err
	}
	current, err := checkoutIdentity(ctx, runtime)
	if err != nil {
		return ProductCheckoutIdentity{}, productPaymentCustomer{}, err
	}
	if current.Provider != original.Provider || current.MerchantID != original.MerchantID || current.StoreID != original.StoreID || current.LiveMode != original.LiveMode || current.Endpoint != original.Endpoint {
		return ProductCheckoutIdentity{}, productPaymentCustomer{}, ErrCheckoutReconciliation
	}
	// Stripe refunds address the verified PaymentIntent and require no historical
	// customer email. This is not an outbound CheckoutRequest and cannot replay it.
	return current, productPaymentCustomer{BuyerIdentity: binding.BuyerID.String()}, nil
}

// Validate immutable merchant/binding evidence without a new provider call.
// Remote reads/refunds must additionally authenticate the current runtime above.
func readRecoveredProductPaymentIdentity(ctx context.Context, db productIdentityQuery, paymentID uuid.UUID) (ProductCheckoutIdentity, ProductPaymentBinding, error) {
	var identityJSON, bindingJSON []byte
	if err := db.QueryRow(ctx, `SELECT identity,binding FROM product_payment_identity_recoveries WHERE payment_id=$1`, paymentID).Scan(&identityJSON, &bindingJSON); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProductCheckoutIdentity{}, ProductPaymentBinding{}, ErrCheckoutReconciliation
		}
		return ProductCheckoutIdentity{}, ProductPaymentBinding{}, err
	}
	var original ProductCheckoutIdentity
	var binding ProductPaymentBinding
	if json.Unmarshal(identityJSON, &original) != nil || json.Unmarshal(bindingJSON, &binding) != nil || original.Provider != "stripe" || original.MerchantID == "" || original.Endpoint == "" {
		return ProductCheckoutIdentity{}, ProductPaymentBinding{}, ErrCheckoutReconciliation
	}
	currentBinding, err := readProductPaymentBinding(ctx, db, paymentID, false)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProductCheckoutIdentity{}, ProductPaymentBinding{}, ErrCheckoutReconciliation
		}
		return ProductCheckoutIdentity{}, ProductPaymentBinding{}, err
	}
	// A session may acquire its payment/charge after recovery. Previously known
	// remote IDs remain pinned; new IDs are checked by the subsequent funds read.
	comparable := currentBinding
	if binding.ProviderPaymentID == "" {
		comparable.ProviderPaymentID = ""
	}
	if binding.ProviderChargeID == "" {
		comparable.ProviderChargeID = ""
	}
	if comparable != binding || original.LiveMode != binding.LiveMode {
		return ProductCheckoutIdentity{}, ProductPaymentBinding{}, ErrCheckoutReconciliation
	}
	return original, binding, nil
}

func (s *Service) HandleProductIdentityRecoveryJob(ctx context.Context, job jobs.Job) error {
	var payload struct {
		PaymentID uuid.UUID `json:"paymentId"`
		ActorID   uuid.UUID `json:"actorId"`
	}
	if job.ID == uuid.Nil || json.Unmarshal(job.Payload, &payload) != nil || payload.PaymentID == uuid.Nil || payload.ActorID == uuid.Nil {
		return newProviderFailure("payment_invalid_request", 0)
	}
	if s == nil || s.pool == nil || !s.config.Enabled {
		return ErrDisabled
	}
	var valid, exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM jobs WHERE id=$1 AND kind=$2
  AND payload->>'paymentId'=$3 AND payload->>'actorId'=$4),
  EXISTS(SELECT 1 FROM product_payment_identity_recoveries WHERE payment_id=$5)
  OR EXISTS(SELECT 1 FROM product_checkout_requests WHERE payment_id=$5)`, job.ID, ProductIdentityRecoveryJobKind, payload.PaymentID.String(), payload.ActorID.String(), payload.PaymentID).Scan(&valid, &exists); err != nil {
		return err
	}
	if !valid {
		return newProviderFailure("payment_invalid_request", 0)
	}
	if exists {
		return nil
	}
	binding, err := readProductPaymentBinding(ctx, s.pool, payload.PaymentID, false)
	if err != nil {
		return err
	}
	runtime, err := s.runtimes.Runtime("stripe")
	if err != nil {
		return err
	}
	reader, ok := runtime.(productPaymentIdentityReader)
	if !ok {
		return newProviderFailure("payment_provider_unsupported", 0)
	}
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	identity, err := checkoutIdentity(readCtx, runtime)
	if err != nil {
		return err
	}
	if identity.Provider != "stripe" || identity.LiveMode != binding.LiveMode {
		return ErrCheckoutReconciliation
	}
	observation, err := reader.ReadProductPaymentIdentity(readCtx, binding)
	if err != nil {
		return SanitizeProviderError(err)
	}
	// Runtime implementations must supply a complete, validated observation.
	if !validIdentityObservation(binding, observation) {
		return newProviderFailure("payment_response_invalid", 0)
	}
	identityJSON, err := json.Marshal(identity)
	if err != nil {
		return newProviderFailure("payment_response_invalid", 0)
	}
	bindingJSON, err := json.Marshal(binding)
	if err != nil {
		return newProviderFailure("payment_response_invalid", 0)
	}
	observationJSON, err := json.Marshal(observation)
	if err != nil {
		return newProviderFailure("payment_response_invalid", 0)
	}
	// Merchant verification has completed. Preserve that exact response while
	// retrying proven local transaction aborts, including during worker shutdown.
	persistCtx, persistCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer persistCancel()
	persist := func(ctx context.Context) error {
		tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "product-checkout:"+binding.BuyerID.String()+":"+binding.ResourceID.String()); err != nil {
			return err
		}
		current, err := readProductPaymentBinding(ctx, tx, payload.PaymentID, true)
		if err != nil {
			return err
		}
		if current != binding {
			return ErrCheckoutReconciliation
		}
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_payment_identity_recoveries WHERE payment_id=$1)
 OR EXISTS(SELECT 1 FROM product_checkout_requests WHERE payment_id=$1)`, payload.PaymentID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return tx.Commit(ctx)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO product_payment_identity_recoveries(payment_id,job_id,requested_by,identity,binding,observation)
 VALUES($1,$2,$3,$4,$5,$6)`, payload.PaymentID, job.ID, payload.ActorID, identityJSON, bindingJSON, observationJSON); err != nil {
			return err
		}
		var status string
		if err := tx.QueryRow(ctx, `UPDATE payment_intents SET version=version+1,updated_at=now() WHERE id=$1 RETURNING status`, payload.PaymentID).Scan(&status); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO payment_intent_events(payment_id,event_type,from_status,to_status,evidence)
 VALUES($1,'identity.verified',$2,$2,jsonb_build_object('jobId',$3::text,'source','provider_query'))`, payload.PaymentID, status, job.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,request_id,metadata)
 VALUES($1,'payment.identity_verified','payment',$2,$3,jsonb_build_object('jobId',$4::text))`, payload.ActorID, payload.PaymentID, "identity-recovery:"+job.ID.String(), job.ID); err != nil {
			return err
		}
		// A recovered merchant does not resolve funds. Schedule the existing read-only
		// workflow in the same transaction; no charge, refund or rights are created.
		if binding.ProviderCheckoutID != "" && (status == "checkout_open" || (oneOf(status, "cancelled", "payment_failed") && observation.Checkout != nil && observation.Checkout.PaymentStatus == "paid")) {
			if _, err := enqueueObservedCheckoutCheckTx(ctx, tx, payload.PaymentID, *observation.Checkout); err != nil {
				return err
			}
		} else if binding.ProviderPaymentID != "" && oneOf(status, "paid", "refund_pending", "refund_failed", "refunded") {
			if err := enqueueRecoveredFundsCheckTx(ctx, tx, payload.ActorID, payload.PaymentID); err != nil {
				return err
			}
		}
		return tx.Commit(ctx)
	}
	_, err = retryPaymentEvidenceWrite(persistCtx, func(writeCtx context.Context) (bool, error) {
		return false, persist(writeCtx)
	})
	return err
}

func validIdentityObservation(b ProductPaymentBinding, o ProductPaymentIdentityObservation) bool {
	if b.ProviderCheckoutID != "" {
		if o.Checkout == nil || o.Payment != nil {
			return false
		}
		c := o.Checkout
		return validCheckoutObservation(CheckoutReadRequest{PaymentID: b.PaymentID, ResourceID: b.ResourceID, ProviderCheckoutID: b.ProviderCheckoutID, AmountCents: b.AmountCents, Currency: b.Currency, LiveMode: b.LiveMode}, *c) && (b.ProviderPaymentID == "" || b.ProviderPaymentID == c.ProviderPaymentID) && (b.ProviderChargeID == "" || b.ProviderChargeID == c.ProviderChargeID)
	}
	p := o.Payment
	return o.Checkout == nil && p != nil && validStripeID(b.ProviderPaymentID, "pi_") && p.ProviderPaymentID == b.ProviderPaymentID && validStripeID(p.ProviderChargeID, "ch_") && (b.ProviderChargeID == "" || b.ProviderChargeID == p.ProviderChargeID) && p.AmountCents == b.AmountCents && p.Currency == b.Currency && p.LiveMode == b.LiveMode && p.Status == "succeeded"
}

func enqueueRecoveredFundsCheckTx(ctx context.Context, tx pgx.Tx, actorID, paymentID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `UPDATE product_refund_checks c SET status='failed',error_code=COALESCE(j.last_error_code,'payment_request_failed'),completed_at=now()
 FROM jobs j WHERE c.payment_id=$1 AND c.job_id=j.id AND j.status IN ('failed','cancelled') AND c.status IN ('requested','observed')`, paymentID); err != nil {
		return err
	}
	var active bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_refund_checks WHERE payment_id=$1 AND status IN ('requested','observed'))`, paymentID).Scan(&active); err != nil {
		return err
	}
	if active {
		return nil
	}
	_, err := insertProductRefundCheckTx(ctx, tx, actorID, paymentID)
	return err
}

// A session can become paid after its merchant was recovered. Ensure that the
// first funds check is durably scheduled when that payment is later accepted.
func enqueueFirstRecoveredFundsCheckTx(ctx context.Context, tx pgx.Tx, paymentID uuid.UUID) error {
	var actorID uuid.UUID
	err := tx.QueryRow(ctx, `SELECT r.requested_by FROM product_payment_identity_recoveries r
 JOIN payment_intents pi ON pi.id=r.payment_id WHERE pi.id=$1 AND pi.provider_payment_id IS NOT NULL
 AND pi.status IN ('paid','refund_pending','refund_failed','refunded')
 AND NOT EXISTS(SELECT 1 FROM product_refund_checks c WHERE c.payment_id=pi.id AND c.created_at>=r.created_at)`, paymentID).Scan(&actorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return enqueueRecoveredFundsCheckTx(ctx, tx, actorID, paymentID)
}
