CREATE TABLE product_refund_checks (
 id uuid PRIMARY KEY,
 payment_id uuid NOT NULL REFERENCES payment_intents(id),
 requested_by uuid NOT NULL REFERENCES users(id),
 job_id uuid NOT NULL REFERENCES jobs(id),
 status text NOT NULL CHECK(status IN ('requested','observed','completed','failed')),
 observations jsonb CHECK(observations IS NULL OR jsonb_typeof(observations)='array'),
 unresolved_count integer NOT NULL DEFAULT 0 CHECK(unresolved_count>=0),
 error_code text,
 observed_at timestamptz,
 completed_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX product_refund_checks_active ON product_refund_checks(payment_id) WHERE status IN ('requested','observed');
CREATE INDEX product_refund_checks_payment ON product_refund_checks(payment_id,created_at DESC,id);
ALTER TABLE payment_provider_events ADD COLUMN evidence_source text NOT NULL DEFAULT 'webhook'
 CHECK(evidence_source IN ('webhook','provider_query'));
ALTER TABLE payment_provider_events ADD COLUMN refund_check_id uuid REFERENCES product_refund_checks(id);
ALTER TABLE payment_provider_events ADD CONSTRAINT payment_refund_query_evidence CHECK(
 (evidence_source='webhook' AND refund_check_id IS NULL) OR
 (evidence_source='provider_query' AND refund_check_id IS NOT NULL AND provider='stripe' AND event_type='refund.observed' AND purpose='product')
);
CREATE FUNCTION protect_refund_check_evidence() RETURNS trigger AS $$
BEGIN
 IF TG_OP='DELETE' OR OLD.status IN ('completed','failed') OR
   ROW(NEW.id,NEW.payment_id,NEW.requested_by,NEW.job_id,NEW.created_at) IS DISTINCT FROM
   ROW(OLD.id,OLD.payment_id,OLD.requested_by,OLD.job_id,OLD.created_at) OR
   (OLD.observed_at IS NOT NULL AND ROW(NEW.observations,NEW.observed_at) IS DISTINCT FROM ROW(OLD.observations,OLD.observed_at)) THEN
   RAISE EXCEPTION 'refund query evidence is immutable';
 END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER product_refund_check_evidence BEFORE UPDATE OR DELETE ON product_refund_checks
 FOR EACH ROW EXECUTE FUNCTION protect_refund_check_evidence();

-- Shared gate for buyer commands, worker dispatch, operator recovery and the
-- order capability projection. Do not send a new refund while checking funds.
CREATE VIEW product_refund_review AS
SELECT pi.id AS payment_id FROM payment_intents pi
WHERE pi.purpose='product' AND (
 EXISTS(SELECT 1 FROM product_refund_attempts a WHERE a.payment_id=pi.id AND a.reconciliation_required)
 OR COALESCE((SELECT c.status IN ('requested','observed') OR c.unresolved_count>0
   OR (c.status='failed' AND c.observed_at IS NOT NULL)
   FROM product_refund_checks c WHERE c.payment_id=pi.id ORDER BY c.created_at DESC,c.id DESC LIMIT 1),false)
);
