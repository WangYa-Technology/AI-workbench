-- Existing orders retain their historical evidence. Never invent a checksum
-- or acceptance timestamp for a purchase made before immutable delivery.
ALTER TABLE orders ADD COLUMN delivery_snapshot_required boolean NOT NULL DEFAULT false;

CREATE TABLE product_delivery_snapshots (
  order_id uuid PRIMARY KEY REFERENCES product_order_contracts(order_id),
  source_backend text NOT NULL CHECK (source_backend IN ('local_file','s3')),
  source_key text NOT NULL CHECK (length(source_key) BETWEEN 1 AND 1024),
  storage_backend text NOT NULL CHECK (storage_backend IN ('local_file','s3')),
  storage_key text NOT NULL CHECK (length(storage_key) BETWEEN 1 AND 1024),
  sha256 text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
  size_bytes bigint NOT NULL CHECK (size_bytes BETWEEN 1 AND 104857600),
  mime_type text NOT NULL CHECK (length(mime_type) BETWEEN 1 AND 200),
  created_at timestamptz NOT NULL DEFAULT now(),
  state text NOT NULL DEFAULT 'prepared' CHECK (state IN ('prepared','ready','removed')),
  ready_at timestamptz,
  removed_at timestamptz,
  UNIQUE(storage_backend,storage_key),
  CHECK ((storage_backend,storage_key) <> (source_backend,source_key)),
  CHECK ((state='prepared' AND ready_at IS NULL AND removed_at IS NULL)
    OR (state='ready' AND ready_at IS NOT NULL AND removed_at IS NULL)
    OR (state='removed' AND removed_at IS NOT NULL))
);

CREATE FUNCTION protect_product_delivery_snapshot() RETURNS trigger AS $$
BEGIN
  IF TG_OP='DELETE' THEN RAISE EXCEPTION 'product delivery evidence is immutable'; END IF;
  IF (to_jsonb(NEW)-'state'-'ready_at'-'removed_at') IS DISTINCT FROM
     (to_jsonb(OLD)-'state'-'ready_at'-'removed_at') THEN
    RAISE EXCEPTION 'product delivery evidence is immutable';
  END IF;
  IF NOT ((OLD.state='prepared' AND NEW.state='ready')
    OR (OLD.state='prepared' AND NEW.state='removed' AND NEW.ready_at IS NULL)
    OR (OLD.state='ready' AND NEW.state='removed' AND NEW.ready_at=OLD.ready_at)) THEN
    RAISE EXCEPTION 'invalid product delivery state transition';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER product_delivery_snapshots_protected BEFORE UPDATE OR DELETE ON product_delivery_snapshots
  FOR EACH ROW EXECUTE FUNCTION protect_product_delivery_snapshot();

CREATE FUNCTION protect_order_delivery_requirement() RETURNS trigger AS $$
BEGIN
  IF OLD.delivery_snapshot_required AND NOT NEW.delivery_snapshot_required THEN
    RAISE EXCEPTION 'required product delivery cannot fall back to mutable source';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER orders_delivery_requirement BEFORE UPDATE ON orders
  FOR EACH ROW EXECUTE FUNCTION protect_order_delivery_requirement();

-- Registering a reserved copy under another asset ID must not make it public.
CREATE OR REPLACE VIEW product_delivery_roots AS
 SELECT COALESCE(a.origin_asset_id,a.id) AS asset_id FROM products p JOIN assets a ON a.id=p.asset_id
 UNION SELECT root_asset_id FROM product_order_contracts
 UNION SELECT a.id FROM assets a JOIN product_order_contracts c
 ON a.storage_backend=c.contract->'asset'->>'storageBackend' AND a.storage_key=c.contract->'asset'->>'storageKey'
 UNION SELECT a.id FROM assets a JOIN product_delivery_snapshots d
 ON a.storage_backend=d.storage_backend AND a.storage_key=d.storage_key;
