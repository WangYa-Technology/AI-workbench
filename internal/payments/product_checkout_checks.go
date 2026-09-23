package payments

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/datarights"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5"
)

const ProductCheckoutCheckJobKind = "payment.check_product_checkout"

var ErrCheckoutExpired = errors.New("checkout was verified expired without payment")

// The checkout query and the monetary evidence are committed separately from
// fulfillment, which has its own durable job and audited replay mechanism.
func (s *Service) HandleProductCheckoutCheckJob(ctx context.Context, job jobs.Job) error {
	var payload struct {
		PaymentID uuid.UUID `json:"paymentId"`
	}
	if job.ID == uuid.Nil || json.Unmarshal(job.Payload, &payload) != nil || payload.PaymentID == uuid.Nil {
		return newProviderFailure("payment_invalid_request", 0)
	}
	if s == nil || s.pool == nil || !s.config.Enabled {
		return ErrDisabled
	}
	var request CheckoutReadRequest
	var provider, status string
	var buyerID, orderID uuid.UUID
	var expiresAt time.Time
	err := s.pool.QueryRow(ctx, `SELECT pi.id,pi.resource_id,pi.provider_checkout_id,pi.amount_cents,pi.currency,pi.live_mode,pi.provider,pi.status,pi.payer_id,pi.order_id,pi.checkout_expires_at
 FROM payment_intents pi JOIN jobs j ON j.id=$2 AND j.kind=$3 AND j.payload->>'paymentId'=pi.id::text
 WHERE pi.id=$1 AND pi.purpose='product'`, payload.PaymentID, job.ID, ProductCheckoutCheckJobKind).Scan(&request.PaymentID, &request.ResourceID, &request.ProviderCheckoutID, &request.AmountCents, &request.Currency, &request.LiveMode, &provider, &status, &buyerID, &orderID, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return newProviderFailure("payment_invalid_request", 0)
	}
	if err != nil {
		return err
	}
	if !oneOf(status, "checkout_open", "cancelled", "payment_failed") {
		return nil
	}
	closedAtStart := status != "checkout_open"
	if !oneOf(provider, "stripe", "waffo_pancake") {
		return newProviderFailure("payment_provider_unsupported", 0)
	}
	// Waffo checkout observations are authenticated through the connector
	// boundary. Keep its runtime gate ahead of persisted-evidence consumption so
	// a changed provider cannot reuse a receipt created under another merchant
	// or protocol. Stripe's immutable receipts remain consumable after a worker
	// restart because their signature verification is complete at ingestion.
	if provider == "waffo_pancake" {
		if _, err := s.runtimes.Runtime(provider); err != nil {
			return newProviderFailure("payment_provider_unsupported", 0)
		}
	}
	// A previous invocation may have committed authenticated paid evidence and
	// queued fulfillment before it crashed. Do not replace that evidence.
	var observed bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_provider_events WHERE checkout_job_id=$1)`, job.ID).Scan(&observed); err != nil {
		return err
	}
	if observed {
		return nil
	}
	observation, evidenceSource, err := paidCheckoutEvidence(ctx, s.pool, request)
	if err != nil {
		return err
	}
	if status != "checkout_open" && !evidenceSource.present() {
		return nil
	}
	if delay := time.Until(expiresAt); !evidenceSource.present() && delay > 0 {
		// A recovered session may already be paid/expired before its planned
		// expiry. Only immutable authenticated recovery evidence for this session can bypass the
		// normal schedule; a caller-provided job flag cannot do so.
		var terminalRecovery bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_checkout_session_evidence
 WHERE payment_id=$1 AND provider_checkout_id=$2 AND (payment_status='paid' OR status='expired'))`, request.PaymentID, request.ProviderCheckoutID).Scan(&terminalRecovery); err != nil {
			return err
		}
		if !terminalRecovery {
			return newProviderFailure("payment_request_failed", min(delay, 15*time.Minute))
		}
	}
	if !evidenceSource.present() {
		runtime, err := s.runtimes.Runtime(provider)
		if err != nil {
			return err
		}
		reader, ok := runtime.(CheckoutReader)
		if !ok {
			return newProviderFailure("payment_provider_unsupported", 0)
		}
		if _, _, err := verifyProductPaymentIdentity(ctx, s.pool, runtime, request.PaymentID); err != nil {
			return err
		}
		readCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		observation, err = reader.ReadProductCheckout(readCtx, request)
		cancel()
		if err != nil {
			return SanitizeProviderError(err)
		}
	}
	if !validCheckoutObservationForProvider(request, observation, provider) {
		return newProviderFailure("payment_response_invalid", 0)
	}
	body, err := json.Marshal(observation)
	if err != nil {
		return err
	}
	// The authenticated response is already in memory. Worker shutdown must not
	// discard it before the evidence and its fulfillment job commit together.
	// Only this local transaction gets an independent, bounded lifetime; remote
	// reads above still honor the worker context and their original deadline.
	persistCtx, persistCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer persistCancel()
	persist := func(ctx context.Context) error {
		// A retry starts from the original read; newly saved paid evidence may
		// replace it only after acquiring the payment lock and checking agreement.
		observation, body, evidenceSource := observation, body, evidenceSource
		tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		// Match the checkout/fulfillment lock order. Reading the provider never holds
		// these locks; a signed success that wins the race must never be cancelled.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "product-checkout:"+buyerID.String()+":"+request.ResourceID.String()); err != nil {
			return err
		}
		var current CheckoutReadRequest
		var orderStatus, currentProvider string
		var currentBuyer, currentOrder uuid.UUID
		var knownPayment, knownCharge *string
		if err := tx.QueryRow(ctx, `SELECT pi.id,pi.resource_id,pi.provider_checkout_id,pi.amount_cents,pi.currency,pi.live_mode,pi.provider,pi.status,pi.payer_id,pi.order_id,o.status,pi.provider_payment_id,pi.provider_charge_id
 FROM payment_intents pi JOIN orders o ON o.id=pi.order_id WHERE pi.id=$1 AND pi.purpose='product' FOR UPDATE OF pi,o`, request.PaymentID).Scan(&current.PaymentID, &current.ResourceID, &current.ProviderCheckoutID, &current.AmountCents, &current.Currency, &current.LiveMode, &currentProvider, &status, &currentBuyer, &currentOrder, &orderStatus, &knownPayment, &knownCharge); err != nil {
			return err
		}
		if current != request || currentProvider != provider || currentBuyer != buyerID || currentOrder != orderID {
			return newProviderFailure("payment_response_invalid", 0)
		}
		// A fresh query racing closure still follows the existing late-payment
		// path; historical recovery requires saved evidence from the initial read.
		closedRecovery := closedAtStart && oneOf(status, "cancelled", "payment_failed")
		if closedRecovery {
			var preserved bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_closed_checkout_recoveries WHERE payment_id=$1)`, request.PaymentID).Scan(&preserved); err != nil {
				return err
			}
			if preserved {
				return tx.Commit(ctx)
			}
			var eligible bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_closed_checkout_candidates WHERE payment_id=$1)`, request.PaymentID).Scan(&eligible); err != nil {
				return err
			}
			if !eligible {
				return ErrCheckoutReconciliation
			}
		}
		if evidenceSource.present() {
			// Additional consistent receipts must not strand this shared check.
			// Revalidate the complete set under the payment lock and preserve its
			// current provenance; contradictory monetary observations still fail.
			paid, currentSource, err := paidCheckoutEvidence(ctx, tx, current)
			if err != nil {
				return err
			}
			if !currentSource.present() || !sameCheckoutPaymentObservation(observation, paid) {
				return ErrCheckoutReconciliation
			}
			observation, evidenceSource = paid, currentSource
			body, err = json.Marshal(observation)
			if err != nil {
				return err
			}
		}
		if err := recordCheckoutQueryTx(ctx, tx, request.PaymentID, status, job.ID, body, evidenceSource); err != nil {
			return err
		}
		if !evidenceSource.present() {
			// Recovery can reuse this in-flight check as its follow-up. Reconcile
			// every remote response, including paid, with newly committed receipts.
			// Retain the original query even when another receipt disagrees.
			paid, source, err := paidCheckoutEvidence(ctx, tx, current)
			if err != nil {
				// The current authenticated remote result is part of the conflict.
				// Persist it so a retry cannot forget it and choose another receipt.
				if errors.Is(err, ErrCheckoutReconciliation) {
					if commitErr := tx.Commit(ctx); commitErr != nil {
						return commitErr
					}
				}
				return err
			}
			if source.present() {
				if observation.PaymentStatus == "paid" && !sameCheckoutPaymentObservation(observation, paid) {
					if err := tx.Commit(ctx); err != nil {
						return err
					}
					return ErrCheckoutReconciliation
				}
				observation, evidenceSource = paid, source
				body, err = json.Marshal(observation)
				if err != nil {
					return err
				}
				if err := recordCheckoutQueryTx(ctx, tx, request.PaymentID, status, job.ID, body, evidenceSource); err != nil {
					return err
				}
			}
		}
		if status != "checkout_open" && observation.PaymentStatus != "paid" {
			return tx.Commit(ctx)
		}
		if (status == "checkout_open" && orderStatus != "payment_pending") || (knownPayment != nil && *knownPayment != observation.ProviderPaymentID) || (knownCharge != nil && *knownCharge != observation.ProviderChargeID) {
			if err := tx.Commit(ctx); err != nil {
				return err
			}
			return newProviderFailure("payment_reconciliation_required", 0)
		}
		if observation.safelyExpired() {
			// The query ran without locks. A replacement query or signed callback may
			// have saved payment while fulfillment is still queued, so the intent is
			// still checkout_open. Recheck immutable paid evidence under the payment
			// lock before releasing its active slot. Retain the late query above, but
			// leave the existing payment event to its normal fulfillment/recovery job.
			var paidEvidence bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_provider_events e
 WHERE e.payment_id=$1 AND e.provider=$2 AND e.purpose='product'
 AND e.resource_id=$3 AND e.amount_cents=$4 AND e.currency=$5 AND e.live_mode=$6
 AND ((e.event_type IN ('checkout.session.completed','checkout.session.async_payment_succeeded','checkout.observed') AND e.payment_status='paid')
 OR (e.event_type='payment_intent.succeeded' AND e.payment_status='succeeded')))`,
				request.PaymentID, provider, request.ResourceID, request.AmountCents, request.Currency, request.LiveMode).Scan(&paidEvidence); err != nil {
				return err
			}
			if paidEvidence {
				return tx.Commit(ctx)
			}
			if err := validateProductCheckoutReconciliation(ctx, tx, request.PaymentID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE payment_intents SET status='cancelled',version=version+1,updated_at=now() WHERE id=$1`, request.PaymentID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE orders SET status='cancelled',updated_at=now() WHERE id=$1`, orderID); err != nil {
				return err
			}
			if err := datarights.EnqueueProductMediaCleanupTx(ctx, tx, orderID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO order_events(order_id,from_status,to_status,reason,sequence)
 SELECT $1,'payment_pending','cancelled','Authenticated provider query confirmed expired checkout with no collected payment.',COALESCE(max(sequence),0)+1 FROM order_events WHERE order_id=$1`, orderID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO payment_intent_events(payment_id,event_type,from_status,to_status,evidence)
 VALUES($1,'checkout.expired_verified','checkout_open','cancelled',jsonb_build_object('source','provider_query','jobId',$2::text,'observation',$3::jsonb))`, request.PaymentID, job.ID, body); err != nil {
				return err
			}
			return tx.Commit(ctx)
		}
		if observation.PaymentStatus == "paid" {
			// A saved recovery may have arrived while an initially open checkout
			// was being queried and then closed. Its historical funds need the same
			// preflight; a fresh query with no recovery source retains the existing
			// late-payment path.
			if !closedRecovery && oneOf(status, "cancelled", "payment_failed") && evidenceSource.present() {
				var eligible bool
				if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_closed_checkout_candidates WHERE payment_id=$1)`, request.PaymentID).Scan(&eligible); err != nil {
					return err
				}
				if !eligible {
					if err := tx.Commit(ctx); err != nil {
						return err
					}
					return ErrCheckoutReconciliation
				}
				closedRecovery = true
			}
			digest := sha256.Sum256(body)
			apiVersion, objectType := s.config.APIVersion, "checkout.session"
			if provider == "waffo_pancake" {
				apiVersion, objectType = waffoCheckoutLookupContractVersion, "checkout.order"
			}
			var eventID uuid.UUID
			if err := tx.QueryRow(ctx, `INSERT INTO payment_provider_events(provider,provider_event_id,event_type,api_version,live_mode,occurred_at,payload_sha256,object_id,object_type,payment_id,resource_id,purpose,amount_cents,currency,payment_status,provider_payment_id,provider_charge_id,evidence_source,checkout_job_id)
 VALUES($13,$1,'checkout.observed',$2,$3,now(),$4,$5,$14,$6,$7,'product',$8,$9,'paid',$10,NULLIF($11,''),'provider_query',$12)
 ON CONFLICT(provider,provider_event_id) DO NOTHING RETURNING id`, "checkoutquery_"+strings.ReplaceAll(job.ID.String(), "-", ""), apiVersion, request.LiveMode, hex.EncodeToString(digest[:]), request.ProviderCheckoutID, request.PaymentID, request.ResourceID, request.AmountCents, request.Currency, observation.ProviderPaymentID, observation.ProviderChargeID, job.ID, provider, objectType).Scan(&eventID); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return tx.Commit(ctx)
				}
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO payment_provider_event_processing(event_id,status) VALUES($1,'received')`, eventID); err != nil {
				return err
			}
			if closedRecovery {
				if err := preserveClosedCheckoutPaymentTx(ctx, tx, request.PaymentID, eventID, job.ID, status, orderStatus, observation); err != nil {
					return err
				}
				return tx.Commit(ctx)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO jobs(kind,payload,max_attempts) VALUES($1,jsonb_build_object('eventId',$2::text),20)`, PaymentEventJobKind, eventID); err != nil {
				return err
			}
			return tx.Commit(ctx)
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		// Open sessions and asynchronous payments retain their active slot. A bounded
		// retry eventually parks the job for the existing finance recovery workflow.
		return newProviderFailure("payment_request_failed", 15*time.Minute)
	}
	_, err = retryPaymentEvidenceWrite(persistCtx, func(writeCtx context.Context) (bool, error) {
		return false, persist(writeCtx)
	})
	return err
}

func recordCheckoutQueryTx(ctx context.Context, tx pgx.Tx, paymentID uuid.UUID, status string, jobID uuid.UUID, body []byte, evidenceSource checkoutEvidenceSource) error {
	var lookupID, recoveryID *uuid.UUID
	if evidenceSource.LookupJobID != uuid.Nil {
		lookupID = &evidenceSource.LookupJobID
	}
	if evidenceSource.IdentityRecoveryJobID != uuid.Nil {
		recoveryID = &evidenceSource.IdentityRecoveryJobID
	}
	_, err := tx.Exec(ctx, `INSERT INTO payment_intent_events(payment_id,event_type,from_status,to_status,evidence)
 VALUES($1,'checkout.queried',$2,$2,jsonb_strip_nulls(jsonb_build_object('source','provider_query','jobId',$3::text,'observation',$4::jsonb,'lookupJobId',$5::text,'identityRecoveryJobId',$6::text)))`, paymentID, status, jobID, body, lookupID, recoveryID)
	return err
}
