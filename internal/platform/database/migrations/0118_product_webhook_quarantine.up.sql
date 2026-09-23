-- Signed, normalized product evidence rejected by transaction binding is kept
-- separately from admitted events. It is never a source of purchase rights.
CREATE TABLE product_webhook_quarantines (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 provider text NOT NULL CHECK(provider IN ('stripe','waffo_pancake')),
 provider_event_id text NOT NULL CHECK(length(provider_event_id) BETWEEN 6 AND 255),
 payload_sha256 text NOT NULL CHECK(payload_sha256 ~ '^[a-f0-9]{64}$'),
 live_mode boolean NOT NULL,
 claimed_payment_id uuid,
 candidate_payment_id uuid REFERENCES payment_intents(id),
 event jsonb NOT NULL CHECK(jsonb_typeof(event)='object' AND octet_length(event::text)<=16384),
 rejection_code text NOT NULL CHECK(rejection_code IN ('payment_binding_conflict','event_identity_conflict','original_identity_missing','payment_unknown','provider_routing_unavailable')),
 received_at timestamptz NOT NULL DEFAULT now(),
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','admitted')),
 admitted_event_id uuid REFERENCES payment_provider_events(id),
 checked_at timestamptz,
 last_error_code text CHECK(last_error_code IN ('payment_binding_conflict','event_identity_conflict','original_identity_missing','payment_unknown','provider_routing_unavailable')),
 version integer NOT NULL DEFAULT 1 CHECK(version>0),
 UNIQUE(provider,provider_event_id,payload_sha256),
 CHECK((state='admitted')=(admitted_event_id IS NOT NULL))
);
CREATE INDEX product_webhook_quarantine_queue_idx ON product_webhook_quarantines(state,received_at DESC,id DESC);
CREATE INDEX product_webhook_quarantine_candidate_idx ON product_webhook_quarantines(candidate_payment_id) WHERE state='pending' AND candidate_payment_id IS NOT NULL;
CREATE INDEX product_webhook_quarantine_claim_idx ON product_webhook_quarantines(claimed_payment_id,provider,live_mode) WHERE state='pending';

CREATE FUNCTION protect_product_webhook_quarantine() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'signed rejected evidence cannot be deleted'; END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'pending' OR NEW.version<>1 OR NEW.admitted_event_id IS NOT NULL
   OR NEW.event->>'ProviderEventID' IS DISTINCT FROM NEW.provider_event_id
   OR NEW.event->>'PayloadSHA256' IS DISTINCT FROM NEW.payload_sha256
   OR NEW.event->>'LiveMode' IS DISTINCT FROM NEW.live_mode::text
   OR NEW.event->>'PaymentID' IS DISTINCT FROM NEW.claimed_payment_id::text
   OR NEW.event->>'Supported' IS DISTINCT FROM 'true' THEN
   RAISE EXCEPTION 'quarantine requires a consistent normalized signed receipt';
  END IF;
  RETURN NEW;
 END IF;
 IF (NEW.id,NEW.provider,NEW.provider_event_id,NEW.payload_sha256,NEW.live_mode,NEW.claimed_payment_id,NEW.candidate_payment_id,NEW.event,NEW.rejection_code,NEW.received_at)
 IS DISTINCT FROM (OLD.id,OLD.provider,OLD.provider_event_id,OLD.payload_sha256,OLD.live_mode,OLD.claimed_payment_id,OLD.candidate_payment_id,OLD.event,OLD.rejection_code,OLD.received_at)
 OR OLD.state='admitted' OR NEW.version<>OLD.version+1 OR NEW.checked_at IS NULL THEN
  RAISE EXCEPTION 'quarantine identity and admitted evidence are immutable';
 END IF;
 IF NEW.state='admitted' AND NOT EXISTS(SELECT 1 FROM payment_provider_events e
 WHERE e.id=NEW.admitted_event_id AND e.provider=NEW.provider AND e.provider_event_id=NEW.provider_event_id
 AND e.payload_sha256=NEW.payload_sha256 AND e.live_mode=NEW.live_mode AND e.evidence_source='webhook'
 AND ROW(e.payment_id::text,e.event_type,e.object_id,e.object_type,e.resource_id::text,e.purpose,
 e.amount_cents::text,e.currency,e.payment_status,e.provider_payment_id,e.provider_charge_id,e.refund_operation_id::text)
 IS NOT DISTINCT FROM ROW(NEW.event->>'PaymentID',NEW.event->>'EventType',NEW.event->>'ObjectID',NEW.event->>'ObjectType',
 NEW.event->>'ResourceID',NEW.event->>'Purpose',NEW.event->>'AmountCents',NEW.event->>'Currency',NEW.event->>'PaymentStatus',
 NEW.event->>'ProviderPaymentID',NEW.event->>'ProviderChargeID',NEW.event->>'RefundOperationID')) THEN
  RAISE EXCEPTION 'quarantine admission requires matching signed event evidence';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER product_webhook_quarantine_guard BEFORE INSERT OR UPDATE OR DELETE ON product_webhook_quarantines
 FOR EACH ROW EXECUTE FUNCTION protect_product_webhook_quarantine();

CREATE TABLE product_webhook_quarantine_checks (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 quarantine_id uuid NOT NULL REFERENCES product_webhook_quarantines(id),
 actor_id uuid NOT NULL REFERENCES users(id),
 expected_version integer NOT NULL CHECK(expected_version>0),
 outcome text NOT NULL CHECK(outcome IN ('pending','admitted')),
 reason text NOT NULL CHECK(char_length(reason) BETWEEN 10 AND 1000),
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(quarantine_id,expected_version)
);
CREATE TRIGGER product_webhook_quarantine_checks_immutable BEFORE UPDATE OR DELETE ON product_webhook_quarantine_checks
 FOR EACH ROW EXECUTE FUNCTION reject_payment_provider_event_mutation();

-- Only an existing remote checkout/payment/charge match can place a payment
-- on hold; a claimed local UUID alone must not block an unrelated customer's funds.
CREATE VIEW product_webhook_quarantine_matches AS
 SELECT q.id AS quarantine_id,p.id AS payment_id FROM product_webhook_quarantines q JOIN payment_intents p
 ON p.id=q.claimed_payment_id AND p.provider=q.provider AND p.live_mode=q.live_mode AND p.purpose='product'
 WHERE q.state='pending' AND (
 (p.provider_checkout_id IS NOT NULL AND p.provider_checkout_id=q.event->>'ObjectID') OR
 (p.provider_payment_id IS NOT NULL AND p.provider_payment_id=q.event->>'ProviderPaymentID') OR
 (p.provider_charge_id IS NOT NULL AND p.provider_charge_id=q.event->>'ProviderChargeID'));
CREATE VIEW product_webhook_quarantine_review AS
 SELECT DISTINCT payment_id FROM product_webhook_quarantine_matches;
CREATE OR REPLACE VIEW product_refund_review AS
 SELECT payment_id FROM product_refund_funds_review
 UNION SELECT payment_id FROM product_refund_dispatch_review
 UNION SELECT payment_id FROM product_webhook_quarantine_review;

-- Ordinary refund cleanup and account deletion use this policy too, not only
-- the reconciliation scheduler. A late signed conflict must retain the copy.
CREATE OR REPLACE VIEW product_delivery_cleanup_policy AS
 SELECT d.order_id,d.state,
 (EXISTS(SELECT 1 FROM payment_intents p WHERE p.order_id=d.order_id AND p.status IN ('checkout_pending','checkout_open')) OR
  EXISTS(SELECT 1 FROM entitlements e JOIN users u ON u.id=e.user_id WHERE e.order_id=d.order_id AND e.status='active' AND u.status<>'deleted') OR
  EXISTS(SELECT 1 FROM payment_intents p JOIN product_webhook_quarantine_review q ON q.payment_id=p.id WHERE p.order_id=d.order_id)) AS needed,
 EXISTS(SELECT 1 FROM data_rights_legal_holds h JOIN product_delivery_cleanup_subjects s ON s.user_id=h.user_id
  WHERE s.order_id=d.order_id AND h.status='active' AND h.expires_at>now()) AS held
 FROM product_delivery_snapshots d;
