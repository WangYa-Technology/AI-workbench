ALTER TABLE product_payment_disputes
  ADD COLUMN latest_event_id uuid;

UPDATE product_payment_disputes d
SET latest_event_id = (
  SELECT e.provider_event_id
  FROM product_payment_dispute_events e
  WHERE e.dispute_id = d.id
  ORDER BY e.occurred_at DESC, e.provider_event_id DESC
  LIMIT 1
);

DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM product_payment_disputes WHERE latest_event_id IS NULL) THEN
    RAISE EXCEPTION 'cannot order product disputes without event evidence' USING ERRCODE='55000';
  END IF;
END $$;

ALTER TABLE product_payment_disputes
  ALTER COLUMN latest_event_id SET NOT NULL,
  ADD CONSTRAINT product_payment_disputes_latest_event_fk
    FOREIGN KEY (latest_event_id) REFERENCES payment_provider_events(id);
