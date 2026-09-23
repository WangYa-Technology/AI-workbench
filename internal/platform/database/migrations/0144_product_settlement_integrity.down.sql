-- Old workers cannot safely interpret reservations or retained recovery proof.
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM product_settlements) THEN
  RAISE EXCEPTION 'cannot downgrade settlement integrity while financial records exist';
 END IF;
END; $$;
DROP TRIGGER product_settlement_dispatch_guard ON product_settlement_dispatches;
DROP FUNCTION protect_product_settlement_dispatch();
DROP TRIGGER product_settlement_snapshot_guard ON product_settlements;
DROP FUNCTION protect_product_settlement_snapshot();
ALTER TABLE product_settlement_dispatches DROP COLUMN reserved_at;
ALTER TABLE product_settlement_events DROP CONSTRAINT product_settlement_event_key_unique;
ALTER TABLE product_settlement_events DROP COLUMN event_key;
ALTER TABLE product_settlements DROP CONSTRAINT product_settlement_transfer_evidence;
ALTER TABLE product_settlements DROP CONSTRAINT product_settlement_transfer_pair;
ALTER TABLE product_settlements ADD CONSTRAINT product_settlements_check2
 CHECK ((status='transferred') = (transferred_at IS NOT NULL AND provider_transfer_id IS NOT NULL));
