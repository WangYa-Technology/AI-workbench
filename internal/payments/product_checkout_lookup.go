package payments

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5"
)

const ProductCheckoutLookupJobKind = "payment.locate_product_checkout"

func (s *Service) HandleProductCheckoutLookupJob(ctx context.Context, job jobs.Job) error {
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
	var valid bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM jobs WHERE id=$1 AND kind=$2 AND payload->>'paymentId'=$3 AND payload->>'actorId'=$4)`, job.ID, ProductCheckoutLookupJobKind, payload.PaymentID.String(), payload.ActorID.String()).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return newProviderFailure("payment_invalid_request", 0)
	}
	if outcome, exists, err := checkoutLookupOutcome(ctx, s.pool, job.ID); err != nil {
		return err
	} else if exists {
		return lookupOutcomeError(outcome)
	}
	var status, provider string
	var createdAt, now time.Time
	if err := s.pool.QueryRow(ctx, `SELECT pi.status,r.created_at,clock_timestamp(),pi.provider FROM payment_intents pi
	 JOIN product_checkout_requests r ON r.payment_id=pi.id WHERE pi.id=$1 AND pi.purpose='product'`, payload.PaymentID).Scan(&status, &createdAt, &now, &provider); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrCheckoutReconciliation
		}
		return err
	}
	if status != "checkout_pending" {
		return nil
	}
	if createdAt.After(now) {
		return ErrCheckoutReconciliation
	}
	// Include both clock margins in the connector's maximum search window.
	// A truncated negative search cannot prove the original order is absent.
	if provider == "waffo_pancake" && now.Sub(createdAt)+10*time.Minute > 24*time.Hour {
		return ErrCheckoutReconciliation
	}
	binding, err := readProductPaymentBindingForProvider(ctx, s.pool, payload.PaymentID, provider, false)
	if err != nil {
		return err
	}
	if binding.ProviderCheckoutID != "" {
		return nil
	}
	runtime, err := s.runtimes.Runtime(provider)
	if err != nil {
		return err
	}
	reader, ok := runtime.(CheckoutLookupReader)
	if !ok {
		return newProviderFailure("payment_provider_unsupported", 0)
	}
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	identity, customer, err := verifyProductPaymentIdentity(readCtx, s.pool, runtime, payload.PaymentID)
	if err != nil {
		return err
	}
	// A five-minute clock margin bounds the list window without asserting that
	// a negative result proves nonexistence. Every candidate still needs exact
	// HCAI metadata, amount, currency, mode and payment verification.
	request := CheckoutLookupRequest{CheckoutReadRequest: CheckoutReadRequest{PaymentID: binding.PaymentID, ResourceID: binding.ResourceID, AmountCents: binding.AmountCents, Currency: binding.Currency, LiveMode: binding.LiveMode}, OrderExternalID: binding.OrderID.String(), BuyerIdentity: customer.BuyerIdentity, StoreID: identity.StoreID, CreatedAfter: createdAt.Add(-5 * time.Minute), CreatedBefore: now.Add(5 * time.Minute)}
	result, err := reader.LookupProductCheckout(readCtx, request)
	if err != nil {
		return SanitizeProviderError(err)
	}
	if !validCheckoutLookupResult(request, result) {
		return newProviderFailure("payment_response_invalid", 0)
	}
	if result.Outcome == "found" {
		observedRequest := request.CheckoutReadRequest
		observedRequest.ProviderCheckoutID = result.Observation.ProviderCheckoutID
		if !validCheckoutObservationForProvider(observedRequest, *result.Observation, provider) {
			return newProviderFailure("payment_response_invalid", 0)
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return newProviderFailure("payment_response_invalid", 0)
	}
	// Retain the verified result through worker cancellation and retry only
	// local transactions that PostgreSQL proves were rolled back.
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
		current, err := readProductPaymentBindingForProvider(ctx, tx, payload.PaymentID, provider, true)
		if err != nil {
			return err
		}
		if outcome, exists, err := checkoutLookupOutcome(ctx, tx, job.ID); err != nil {
			return err
		} else if exists {
			return lookupOutcomeError(outcome)
		}
		var orderStatus string
		if err := tx.QueryRow(ctx, `SELECT pi.status,o.status FROM payment_intents pi JOIN orders o ON o.id=pi.order_id WHERE pi.id=$1 FOR UPDATE OF o`, payload.PaymentID).Scan(&status, &orderStatus); err != nil {
			return err
		}
		outcome := result.Outcome
		var checkJobID *uuid.UUID
		// Changes in business identity are not accepted, even if a network lookup
		// happened to return a matching remote session from the old request.
		comparable := current
		comparable.ProviderCheckoutID = binding.ProviderCheckoutID
		comparable.ProviderPaymentID = binding.ProviderPaymentID
		comparable.ProviderChargeID = binding.ProviderChargeID
		if comparable != binding {
			outcome = "state_changed"
		}
		if outcome == "found" {
			o := result.Observation
			if (current.ProviderCheckoutID != "" && current.ProviderCheckoutID != o.ProviderCheckoutID) ||
				(current.ProviderPaymentID != "" && current.ProviderPaymentID != o.ProviderPaymentID) ||
				(current.ProviderChargeID != "" && current.ProviderChargeID != o.ProviderChargeID) {
				outcome = "state_changed"
			}
			if outcome == "found" {
				switch {
				case status == "checkout_pending" && orderStatus == "payment_pending":
					recoveredURL := ""
					if o.Status == "open" {
						recoveredURL = o.CheckoutURL
					}
					if _, err := tx.Exec(ctx, `UPDATE payment_intents SET status='checkout_open',provider_checkout_id=$2,checkout_url=NULLIF($3,''),checkout_expires_at=$4 WHERE id=$1`, payload.PaymentID, o.ProviderCheckoutID, recoveredURL, o.ExpiresAt); err != nil {
						return err
					}
					checkJobID, err = enqueueObservedCheckoutCheckTx(ctx, tx, payload.PaymentID, *o)
				case status == "checkout_open" && orderStatus == "payment_pending" && current.ProviderCheckoutID == o.ProviderCheckoutID:
					checkJobID, err = enqueueObservedCheckoutCheckTx(ctx, tx, payload.PaymentID, *o)
				case oneOf(status, "paid", "refund_pending", "refund_failed", "refunded") && current.ProviderPaymentID != "" && current.ProviderPaymentID == o.ProviderPaymentID:
					// The signed callback won. Only attach the proven session ID; never
					// reopen the order, replace payment IDs or regrant/refund its rights.
					if _, err = tx.Exec(ctx, `UPDATE payment_intents SET provider_checkout_id=COALESCE(provider_checkout_id,$2),checkout_expires_at=COALESCE(checkout_expires_at,$3) WHERE id=$1`, payload.PaymentID, o.ProviderCheckoutID, o.ExpiresAt); err != nil {
						return err
					}
				default:
					outcome = "state_changed"
				}
				if err != nil {
					return err
				}
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO product_checkout_lookups(job_id,payment_id,requested_by,outcome,searched_after,searched_before,result,check_job_id)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, job.ID, payload.PaymentID, payload.ActorID, outcome, request.CreatedAfter, request.CreatedBefore, encoded, checkJobID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE payment_intents SET version=version+1,updated_at=now() WHERE id=$1`, payload.PaymentID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO payment_intent_events(payment_id,event_type,from_status,to_status,evidence)
 SELECT id,'checkout.lookup_completed',$2,status,jsonb_build_object('jobId',$3::text,'outcome',$4::text,'source','provider_query') FROM payment_intents WHERE id=$1`, payload.PaymentID, status, job.ID, outcome); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,resource_type,resource_id,request_id,metadata)
 VALUES($1,'payment.checkout_lookup_completed','payment',$2,$3,jsonb_build_object('jobId',$4::text,'outcome',$5::text))`, payload.ActorID, payload.PaymentID, "checkout-lookup:"+job.ID.String(), job.ID, outcome); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return lookupOutcomeError(outcome)
	}
	_, err = retryPaymentEvidenceWrite(persistCtx, func(writeCtx context.Context) (bool, error) {
		return false, persist(writeCtx)
	})
	return err
}

func checkoutLookupOutcome(ctx context.Context, db productIdentityQuery, jobID uuid.UUID) (string, bool, error) {
	var outcome string
	err := db.QueryRow(ctx, `SELECT outcome FROM product_checkout_lookups WHERE job_id=$1`, jobID).Scan(&outcome)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return outcome, err == nil, err
}
func lookupOutcomeError(outcome string) error {
	if outcome == "found" {
		return nil
	}
	return ErrCheckoutReconciliation
}
func validCheckoutLookupResult(input CheckoutLookupRequest, r CheckoutLookupResult) bool {
	if r.Pages < 1 || r.Pages > 10 || r.Scanned < 0 || r.Scanned > 1000 || r.Scanned > r.Pages*100 || len(r.Matches) > 2 || len(r.Matches) > r.Scanned {
		return false
	}
	seen := map[string]bool{}
	for _, id := range r.Matches {
		if len(id) < 3 || len(id) > 255 || seen[id] {
			return false
		}
		seen[id] = true
	}
	switch r.Outcome {
	case "found":
		if len(r.Matches) != 1 || r.Observation == nil {
			return false
		}
		o := r.Observation
		if o.ProviderCheckoutID != r.Matches[0] || o.AmountCents != input.AmountCents || o.Currency != input.Currency || o.LiveMode != input.LiveMode || o.ExpiresAt.IsZero() || !oneOf(o.Status, "open", "complete", "expired") || !oneOf(o.PaymentStatus, "paid", "unpaid", "pending") {
			return false
		}
		if o.Status == "open" {
			parsed, err := url.Parse(o.CheckoutURL)
			if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Path == "" {
				return false
			}
		}
		return true
	case "not_found":
		return len(r.Matches) == 0 && r.Observation == nil
	case "ambiguous":
		return len(r.Matches) == 2 && r.Observation == nil
	case "incomplete":
		return r.Observation == nil
	default:
		return false
	}
}
func enqueueObservedCheckoutCheckTx(ctx context.Context, tx pgx.Tx, paymentID uuid.UUID, o CheckoutObservation) (*uuid.UUID, error) {
	available := o.ExpiresAt.Add(5 * time.Second)
	terminal := o.PaymentStatus == "paid" || o.safelyExpired()
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM jobs WHERE kind=$1 AND payload->>'paymentId'=$2 AND status IN ('queued','running')`, ProductCheckoutCheckJobKind, paymentID.String()).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `INSERT INTO jobs(kind,payload,max_attempts,available_at)
  VALUES($1,jsonb_build_object('paymentId',$2::text),20,CASE WHEN $3 THEN now() ELSE GREATEST(now(),$4) END) RETURNING id`, ProductCheckoutCheckJobKind, paymentID, terminal, available).Scan(&id)
	} else if err == nil && terminal {
		_, err = tx.Exec(ctx, `UPDATE jobs SET available_at=LEAST(available_at,now()) WHERE id=$1 AND status='queued'`, id)
	}
	if err != nil {
		return nil, err
	}
	return &id, nil
}
