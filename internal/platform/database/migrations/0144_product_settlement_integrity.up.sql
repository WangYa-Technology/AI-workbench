-- Keep external transfer evidence when a later refund creates a recovery debt.
ALTER TABLE product_settlements DROP CONSTRAINT product_settlements_check2;
ALTER TABLE product_settlements
 ADD CONSTRAINT product_settlement_transfer_pair CHECK (
   (transferred_at IS NULL) = (provider_transfer_id IS NULL)),
 ADD CONSTRAINT product_settlement_transfer_evidence CHECK (
   (status <> 'transferred' OR provider_transfer_id IS NOT NULL)
   AND (provider_transfer_id IS NULL OR status IN ('transferred','recovery_required')));

ALTER TABLE product_settlement_dispatches ADD COLUMN reserved_at timestamptz;
-- A legacy batch may already have moved money. Never grant it a fresh permit.
UPDATE product_settlement_dispatches d SET reserved_at=ps.updated_at
 FROM product_settlements ps WHERE ps.id=d.settlement_id AND ps.payout_batch_id IS NOT NULL;

CREATE FUNCTION protect_product_settlement_snapshot() RETURNS trigger AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'product settlement snapshots are immutable'; END IF;
 IF ROW(NEW.id,NEW.order_id,NEW.payment_id,NEW.seller_id,NEW.provider,NEW.live_mode,
   NEW.gross_amount_cents,NEW.fee_bps,NEW.fee_cents,NEW.net_amount_cents,NEW.currency,
   NEW.available_at,NEW.transfer_idempotency_key,NEW.created_at) IS DISTINCT FROM
   ROW(OLD.id,OLD.order_id,OLD.payment_id,OLD.seller_id,OLD.provider,OLD.live_mode,
   OLD.gross_amount_cents,OLD.fee_bps,OLD.fee_cents,OLD.net_amount_cents,OLD.currency,
   OLD.available_at,OLD.transfer_idempotency_key,OLD.created_at)
 OR (OLD.payout_batch_id IS NOT NULL AND NEW.payout_batch_id IS DISTINCT FROM OLD.payout_batch_id)
 OR (OLD.destination_id IS NOT NULL AND NEW.destination_id IS DISTINCT FROM OLD.destination_id)
 OR (OLD.provider_transfer_id IS NOT NULL AND
   ROW(NEW.provider_transfer_id,NEW.transferred_at) IS DISTINCT FROM ROW(OLD.provider_transfer_id,OLD.transferred_at))
 THEN RAISE EXCEPTION 'product settlement economic and transfer evidence is immutable'; END IF;
 RETURN NEW;
END; $$ LANGUAGE plpgsql;
CREATE TRIGGER product_settlement_snapshot_guard BEFORE UPDATE OR DELETE ON product_settlements
 FOR EACH ROW EXECUTE FUNCTION protect_product_settlement_snapshot();

CREATE FUNCTION protect_product_settlement_dispatch() RETURNS trigger AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'product settlement dispatches are immutable'; END IF;
 IF ROW(NEW.settlement_id,NEW.job_id,NEW.created_at) IS DISTINCT FROM ROW(OLD.settlement_id,OLD.job_id,OLD.created_at)
 OR OLD.reserved_at IS NOT NULL OR NEW.reserved_at IS NULL
 THEN RAISE EXCEPTION 'product settlement dispatch reservation is immutable'; END IF;
 RETURN NEW;
END; $$ LANGUAGE plpgsql;
CREATE TRIGGER product_settlement_dispatch_guard BEFORE UPDATE OR DELETE ON product_settlement_dispatches
 FOR EACH ROW EXECUTE FUNCTION protect_product_settlement_dispatch();

ALTER TABLE product_settlement_events ADD COLUMN event_key text;
ALTER TABLE product_settlement_events DISABLE TRIGGER product_settlement_events_immutable;
UPDATE product_settlement_events SET event_key='legacy:'||id::text;
ALTER TABLE product_settlement_events ENABLE TRIGGER product_settlement_events_immutable;
ALTER TABLE product_settlement_events ALTER COLUMN event_key SET NOT NULL;
ALTER TABLE product_settlement_events ADD CONSTRAINT product_settlement_event_key_unique UNIQUE(settlement_id,event_key);

-- Existing unreserved work should first run when its frozen hold actually ends.
UPDATE jobs j SET available_at=GREATEST(j.available_at,ps.available_at)
 FROM product_settlement_dispatches d JOIN product_settlements ps ON ps.id=d.settlement_id
 WHERE j.id=d.job_id AND j.status='queued' AND d.reserved_at IS NULL;
