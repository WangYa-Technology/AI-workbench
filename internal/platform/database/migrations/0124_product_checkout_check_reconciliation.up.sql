-- Restore missing reads, never infer money movement or reset failed work.
CREATE INDEX product_checkout_check_history ON jobs ((payload->>'paymentId'),created_at DESC,id DESC)
 WHERE kind='payment.check_product_checkout';

CREATE TABLE product_checkout_check_dispatches (
 payment_id uuid PRIMARY KEY REFERENCES payment_intents(id),
 job_id uuid NOT NULL UNIQUE REFERENCES jobs(id),
 payment_version integer NOT NULL CHECK(payment_version>0),
 due_at timestamptz NOT NULL CHECK(isfinite(due_at)),
 created_at timestamptz NOT NULL DEFAULT now() CHECK(isfinite(created_at))
);
CREATE TRIGGER product_checkout_check_dispatch_immutable BEFORE UPDATE OR DELETE ON product_checkout_check_dispatches
 FOR EACH ROW EXECUTE FUNCTION reject_payment_provider_event_mutation();

CREATE VIEW product_checkout_check_candidates AS
 SELECT pi.id AS payment_id,pi.order_id,pi.version AS payment_version,
 CASE WHEN terminal.observed_at IS NOT NULL THEN LEAST(pi.checkout_expires_at,terminal.observed_at)
 ELSE pi.checkout_expires_at+interval '5 seconds' END AS due_at
 FROM payment_intents pi JOIN orders o ON o.id=pi.order_id
 LEFT JOIN LATERAL (
  SELECT min(l.created_at) AS observed_at FROM product_checkout_lookups l
  WHERE l.payment_id=pi.id AND l.outcome='found' AND l.result->>'outcome'='found'
  AND l.result->'observation'->>'providerCheckoutId'=pi.provider_checkout_id
  AND (l.result->'observation'->>'paymentStatus'='paid' OR l.result->'observation'->>'status'='expired')
  AND isfinite(l.created_at) AND l.created_at<=now()
 ) terminal ON true
 WHERE pi.purpose='product' AND pi.provider='stripe' AND pi.status='checkout_open'
 AND o.status='payment_pending' AND o.buyer_id=pi.payer_id AND o.product_id=pi.resource_id
 AND pi.provider_checkout_id IS NOT NULL AND isfinite(pi.checkout_expires_at)
 AND NOT EXISTS(SELECT 1 FROM product_payment_identity_gaps g WHERE g.payment_id=pi.id)
 AND NOT EXISTS(SELECT 1 FROM jobs j WHERE j.kind='payment.check_product_checkout' AND j.payload->>'paymentId'=pi.id::text)
 AND NOT EXISTS(SELECT 1 FROM product_checkout_check_dispatches d WHERE d.payment_id=pi.id)
 AND NOT EXISTS(SELECT 1 FROM payment_provider_events e LEFT JOIN payment_provider_event_processing ep ON ep.event_id=e.id
   WHERE e.payment_id=pi.id AND COALESCE(ep.status,'missing')<>'processed');

-- Verify the durable job link in the same transaction as the dispatch.
CREATE FUNCTION check_product_checkout_check_dispatch() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM jobs j JOIN payment_intents p ON p.id=NEW.payment_id
   WHERE j.id=NEW.job_id AND j.kind='payment.check_product_checkout'
   AND j.payload->>'paymentId'=p.id::text AND j.status='queued'
   AND p.purpose='product' AND p.provider='stripe' AND p.status='checkout_open'
   AND p.version=NEW.payment_version+1 AND NEW.due_at<=now()) THEN
  RAISE EXCEPTION 'checkout check dispatch requires matching product query job';
 END IF;
 RETURN NEW;
END $$;
CREATE CONSTRAINT TRIGGER product_checkout_check_dispatch_binding AFTER INSERT ON product_checkout_check_dispatches
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_product_checkout_check_dispatch();

ALTER TABLE maintenance_health DROP CONSTRAINT maintenance_health_kind_check;
ALTER TABLE maintenance_health ADD CONSTRAINT maintenance_health_kind_check CHECK(kind IN (
 'legal_hold_expiry','legal_hold_cleanup','product_cleanup_reconciliation','account_deletion_reconciliation',
 'original_media_cleanup_reconciliation','product_refund_reconciliation','generation_output_cleanup','generation_execution_recovery',
 'asset_scan_execution_recovery','upload_write_cleanup','product_checkout_reconciliation'));
