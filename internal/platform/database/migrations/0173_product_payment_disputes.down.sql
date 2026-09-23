DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM product_payment_dispute_events) OR EXISTS (SELECT 1 FROM product_payment_disputes) THEN
    RAISE EXCEPTION 'cannot remove product payment dispute evidence' USING ERRCODE='55000';
  END IF;
END $$;
DROP TRIGGER IF EXISTS product_settlement_dispute_hold_guard ON product_settlements;
DROP FUNCTION IF EXISTS protect_product_settlement_dispute_hold();
ALTER TABLE product_settlements DROP CONSTRAINT IF EXISTS product_settlements_status_check;
ALTER TABLE product_settlements ADD CONSTRAINT product_settlements_status_check CHECK (status IN (
  'pending_hold','available','transfer_pending','transferred','refund_hold','recovery_required','provider_unsupported','cancelled'));
DROP TRIGGER IF EXISTS product_payment_dispute_events_immutable ON product_payment_dispute_events;
DROP FUNCTION IF EXISTS reject_product_payment_dispute_mutation();
DROP TABLE product_payment_dispute_events;
DROP TABLE product_payment_disputes;
ALTER TABLE payment_provider_events DROP COLUMN dispute_status, DROP COLUMN dispute_reason, DROP COLUMN dispute_network_reason_code, DROP COLUMN dispute_due_by;
