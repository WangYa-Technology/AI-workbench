DO $$ BEGIN
  IF EXISTS(SELECT 1 FROM product_delivery_snapshots)
    OR EXISTS(SELECT 1 FROM orders WHERE delivery_snapshot_required) THEN
    RAISE EXCEPTION 'cannot discard immutable product delivery evidence';
  END IF;
END $$;
DROP TRIGGER orders_delivery_requirement ON orders;
DROP FUNCTION protect_order_delivery_requirement();
CREATE OR REPLACE VIEW product_delivery_roots AS
 SELECT COALESCE(a.origin_asset_id,a.id) AS asset_id FROM products p JOIN assets a ON a.id=p.asset_id
 UNION SELECT root_asset_id FROM product_order_contracts
 UNION SELECT a.id FROM assets a JOIN product_order_contracts c
 ON a.storage_backend=c.contract->'asset'->>'storageBackend' AND a.storage_key=c.contract->'asset'->>'storageKey';
DROP TABLE product_delivery_snapshots;
DROP FUNCTION protect_product_delivery_snapshot();
ALTER TABLE orders DROP COLUMN delivery_snapshot_required;
