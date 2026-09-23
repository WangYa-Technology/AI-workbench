-- Confirm an already-executed original refund; this never creates a refund operation.
CREATE TABLE product_closed_checkout_refund_confirmations (
 payment_id uuid PRIMARY KEY REFERENCES product_closed_checkout_recoveries(payment_id),
 check_id uuid NOT NULL REFERENCES product_refund_checks(id),
 event_id uuid NOT NULL UNIQUE REFERENCES payment_provider_events(id),
 operation_id uuid NOT NULL UNIQUE REFERENCES product_refund_attempts(operation_id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at))
);
CREATE TRIGGER product_closed_checkout_refund_confirmation_immutable BEFORE UPDATE OR DELETE ON product_closed_checkout_refund_confirmations
 FOR EACH ROW EXECUTE FUNCTION reject_payment_provider_event_mutation();

CREATE VIEW product_closed_checkout_refund_candidates AS
 SELECT r.payment_id,c.id AS check_id,e.id AS event_id,a.operation_id,p.order_id,p.provider_payment_id,
 a.provider_refund_id,p.amount_cents,p.currency
 FROM product_closed_checkout_recoveries r JOIN payment_intents p ON p.id=r.payment_id
 JOIN orders o ON o.id=p.order_id
 JOIN LATERAL (SELECT * FROM product_refund_checks q WHERE q.payment_id=p.id ORDER BY q.created_at DESC,q.id DESC LIMIT 1) c ON true
 JOIN product_refund_attempts a ON a.payment_id=p.id AND a.provider='stripe' AND a.status='succeeded'
 JOIN payment_provider_events e ON e.refund_check_id=c.id AND e.refund_operation_id=a.operation_id
 JOIN payment_provider_event_processing ep ON ep.event_id=e.id AND ep.status='processed'
 WHERE p.provider='stripe' AND p.purpose='product' AND p.status IN ('cancelled','payment_failed')
 AND o.status IN ('cancelled','payment_failed','payment_pending') AND o.buyer_id=p.payer_id AND o.product_id=p.resource_id
 AND c.status='completed' AND c.unresolved_count=0 AND c.created_at>=r.created_at AND c.observed_at>=r.created_at
 AND EXISTS(SELECT 1 FROM product_refund_read_executions x WHERE x.check_id=c.id AND x.complete AND x.started_at>=r.created_at)
 AND (o.refund_operation_id IS NULL OR EXISTS(SELECT 1 FROM product_refund_attempts x WHERE x.payment_id=p.id AND x.operation_id=o.refund_operation_id))
 AND (p.provider_refund_id IS NULL OR p.provider_refund_id=a.provider_refund_id OR EXISTS(SELECT 1 FROM product_refund_attempts x
   WHERE x.payment_id=p.id AND x.provider='stripe' AND x.provider_refund_id=p.provider_refund_id AND x.status='failed'
   AND x.provider_payment_id=p.provider_payment_id AND x.amount_cents=p.amount_cents AND x.currency=p.currency))
 AND a.requested_at<=r.created_at AND a.provider_payment_id=p.provider_payment_id AND a.amount_cents=p.amount_cents AND a.currency=p.currency
 AND e.provider='stripe' AND e.purpose='product' AND e.payment_id=p.id AND e.resource_id=p.resource_id
 AND e.event_type='refund.observed' AND e.evidence_source='provider_query' AND e.payment_status='succeeded'
 AND e.object_id=a.provider_refund_id AND e.provider_payment_id=a.provider_payment_id
 AND e.amount_cents=a.amount_cents AND e.currency=a.currency AND e.live_mode=p.live_mode
 AND (SELECT count(*) FROM product_refund_attempts x WHERE x.payment_id=p.id AND x.status='succeeded')=1
 AND NOT EXISTS(SELECT 1 FROM product_refund_attempts x WHERE x.payment_id=p.id AND (x.status IN ('requested','pending') OR x.reconciliation_required))
 AND NOT EXISTS(SELECT 1 FROM jobs j WHERE j.kind='payment.refund_product' AND j.payload->>'paymentId'=p.id::text AND j.status IN ('queued','running'))
 AND NOT EXISTS(SELECT 1 FROM product_payment_identity_gaps g WHERE g.payment_id=p.id)
 AND NOT EXISTS(SELECT 1 FROM product_checkout_evidence_conflicts g WHERE g.payment_id=p.id)
 AND NOT EXISTS(SELECT 1 FROM product_checkout_lookups l WHERE l.payment_id=p.id AND l.outcome IN ('ambiguous','state_changed'))
 AND NOT EXISTS(SELECT 1 FROM product_webhook_quarantine_review g WHERE g.payment_id=p.id)
 AND NOT EXISTS(SELECT 1 FROM product_refund_observation_review g WHERE g.payment_id=p.id)
 AND NOT EXISTS(SELECT 1 FROM product_refund_read_gaps g WHERE g.payment_id=p.id)
 AND NOT EXISTS(SELECT 1 FROM entitlements x WHERE x.order_id=o.id AND (x.user_id<>o.buyer_id OR x.product_id<>o.product_id))
 AND NOT EXISTS(SELECT 1 FROM product_closed_checkout_refund_confirmations x WHERE x.payment_id=p.id);

CREATE FUNCTION validate_product_closed_checkout_refund_confirmation() RETURNS trigger AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM product_closed_checkout_refund_candidates c WHERE c.payment_id=NEW.payment_id
 AND c.check_id=NEW.check_id AND c.event_id=NEW.event_id AND c.operation_id=NEW.operation_id)
 THEN RAISE EXCEPTION 'closed checkout refund confirmation requires complete original refund evidence'; END IF;
 RETURN NEW;
END; $$ LANGUAGE plpgsql;
CREATE TRIGGER product_closed_checkout_refund_confirmation_binding BEFORE INSERT ON product_closed_checkout_refund_confirmations
 FOR EACH ROW EXECUTE FUNCTION validate_product_closed_checkout_refund_confirmation();

CREATE OR REPLACE VIEW product_closed_checkout_funds_ready AS
 SELECT r.payment_id,r.event_id FROM product_closed_checkout_recoveries r
 WHERE COALESCE((SELECT c.status='completed' AND c.unresolved_count=0 AND c.observed_at>=r.created_at AND c.created_at>=r.created_at
 AND EXISTS(SELECT 1 FROM product_refund_read_executions x WHERE x.check_id=c.id AND x.complete AND x.started_at>=r.created_at)
 FROM product_refund_checks c WHERE c.payment_id=r.payment_id ORDER BY c.created_at DESC,c.id DESC LIMIT 1),false)
 AND NOT EXISTS(SELECT 1 FROM product_payment_identity_gaps g WHERE g.payment_id=r.payment_id)
 AND NOT EXISTS(SELECT 1 FROM product_checkout_evidence_conflicts g WHERE g.payment_id=r.payment_id)
 AND NOT EXISTS(SELECT 1 FROM product_checkout_lookups l WHERE l.payment_id=r.payment_id AND l.outcome IN ('ambiguous','state_changed'))
 AND NOT EXISTS(SELECT 1 FROM product_webhook_quarantine_review g WHERE g.payment_id=r.payment_id)
 AND NOT EXISTS(SELECT 1 FROM product_refund_observation_review g WHERE g.payment_id=r.payment_id)
 AND NOT EXISTS(SELECT 1 FROM product_refund_read_gaps g WHERE g.payment_id=r.payment_id)
 AND NOT EXISTS(SELECT 1 FROM product_refund_attempts a WHERE a.payment_id=r.payment_id AND a.reconciliation_required)
 -- Existing refund obligations and historical rights need their own disposition.
 -- A clean remote read cannot authorize a second compensation for them.
 AND NOT EXISTS(SELECT 1 FROM product_refund_attempts a WHERE a.payment_id=r.payment_id AND a.requested_at<=r.created_at)
 AND NOT EXISTS(SELECT 1 FROM payment_intents p JOIN entitlements e ON e.order_id=p.order_id WHERE p.id=r.payment_id)
 UNION ALL
 SELECT f.payment_id,r.event_id FROM product_closed_checkout_refund_confirmations f
 JOIN product_closed_checkout_recoveries r ON r.payment_id=f.payment_id
 JOIN payment_intents p ON p.id=f.payment_id AND p.status='refunded'
 JOIN orders o ON o.id=p.order_id AND o.status='refunded'
 JOIN product_refund_attempts a ON a.operation_id=f.operation_id AND a.payment_id=p.id AND a.status='succeeded'
 WHERE p.provider_refund_id=a.provider_refund_id AND p.provider_payment_id=a.provider_payment_id
 AND p.amount_cents=a.amount_cents AND p.currency=a.currency;
