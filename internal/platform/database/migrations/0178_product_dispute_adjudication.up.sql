CREATE TABLE product_payment_dispute_adjudications (
  dispute_id uuid PRIMARY KEY REFERENCES product_payment_disputes(id) ON DELETE RESTRICT,
  provider_event_id uuid NOT NULL UNIQUE REFERENCES product_payment_dispute_events(provider_event_id) ON DELETE RESTRICT,
  outcome text NOT NULL CHECK (outcome IN ('won','lost','charge_refunded')),
  financial_effect text NOT NULL CHECK (financial_effect IN ('release_hold','loss_applied','no_change','requires_review')),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  FOREIGN KEY (dispute_id,provider_event_id) REFERENCES product_payment_dispute_events(dispute_id,provider_event_id) ON DELETE RESTRICT
);

CREATE FUNCTION reject_product_payment_dispute_adjudication_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'product payment dispute adjudication evidence is immutable' USING ERRCODE='55000';
END;
$$;
CREATE TRIGGER product_payment_dispute_adjudications_immutable
BEFORE UPDATE OR DELETE ON product_payment_dispute_adjudications
FOR EACH ROW EXECUTE FUNCTION reject_product_payment_dispute_adjudication_mutation();

CREATE FUNCTION validate_product_dispute_adjudication() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM product_payment_disputes d
    JOIN product_payment_dispute_events e ON e.dispute_id=d.id
    JOIN payment_provider_events p ON p.id=e.provider_event_id
    JOIN payment_intents pi ON pi.id=d.payment_id
    WHERE d.id=NEW.dispute_id AND e.provider_event_id=NEW.provider_event_id
      AND e.provider_status=NEW.outcome AND p.dispute_status=NEW.outcome
      AND p.provider='stripe' AND p.object_id=d.provider_dispute_id
      AND p.live_mode=d.live_mode AND pi.live_mode=d.live_mode
      AND p.provider_payment_id=d.provider_payment_id AND pi.provider_payment_id=d.provider_payment_id
      AND p.provider_charge_id=d.provider_charge_id AND pi.provider_charge_id=d.provider_charge_id
      AND p.amount_cents=d.amount_cents AND pi.amount_cents=d.amount_cents
      AND p.currency=d.currency AND pi.currency=d.currency
      AND pi.provider='stripe' AND pi.purpose='product' AND pi.order_id=d.order_id
  ) THEN
    RAISE EXCEPTION 'dispute adjudication requires matching provider evidence' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER product_dispute_adjudication_evidence_guard
BEFORE INSERT ON product_payment_dispute_adjudications
FOR EACH ROW EXECUTE FUNCTION validate_product_dispute_adjudication();

-- Upgrade replay uses existing immutable Provider evidence; no synthetic outcome.
INSERT INTO jobs(kind,payload,max_attempts)
SELECT 'payment.process_event',jsonb_build_object('eventId',d.latest_event_id::text,'disputeAdjudicationBackfill',true),8
FROM product_payment_disputes d
JOIN payment_provider_event_processing p ON p.event_id=d.latest_event_id
WHERE d.payment_id IS NOT NULL AND d.action_status IN ('won','lost','charge_refunded') AND p.status='processed';
