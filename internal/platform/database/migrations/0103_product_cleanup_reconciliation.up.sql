-- Discover independently stored copies whose normal cleanup dispatch was lost,
-- or whose last successful cleanup retained an obligation that has since ended.
-- No jobs are created and no delivery evidence is changed by this migration.
CREATE VIEW product_cleanup_resolved_orders AS
 SELECT o.id AS order_id FROM orders o JOIN users buyer ON buyer.id=o.buyer_id
 JOIN payment_intents p ON p.order_id=o.id AND p.purpose='product' AND p.payer_id=o.buyer_id
 WHERE ((o.status='cancelled' AND p.status='cancelled') OR
        (o.status='refunded' AND p.status='refunded') OR
        (o.status='fulfilled' AND p.status='paid' AND buyer.status='deleted'))
 AND NOT EXISTS(SELECT 1 FROM product_refund_review review WHERE review.payment_id=p.id);

CREATE VIEW product_cleanup_reconciliation_candidates AS
 SELECT d.order_id,o.buyer_id,d.created_at,last_job.id AS previous_job_id
 FROM product_delivery_snapshots d
 JOIN product_delivery_cleanup_policy policy ON policy.order_id=d.order_id
 JOIN orders o ON o.id=d.order_id
 JOIN product_cleanup_resolved_orders resolved ON resolved.order_id=o.id
 LEFT JOIN LATERAL (
  SELECT id,status FROM jobs WHERE kind='product.delivery_cleanup' AND payload->>'orderId'=o.id::text
  ORDER BY created_at DESC,id DESC LIMIT 1
 ) last_job ON true
 WHERE d.state<>'removed' AND NOT policy.needed AND NOT policy.held
 AND (last_job.id IS NULL OR last_job.status='succeeded')
 AND NOT EXISTS(SELECT 1 FROM jobs active WHERE active.kind='product.delivery_cleanup'
  AND active.payload->>'orderId'=o.id::text AND active.status IN ('queued','running'));

CREATE TABLE product_cleanup_reconciliations (
 job_id uuid PRIMARY KEY REFERENCES jobs(id),
 order_id uuid NOT NULL REFERENCES product_delivery_snapshots(order_id),
 previous_job_id uuid REFERENCES jobs(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 CHECK (previous_job_id IS NULL OR previous_job_id<>job_id)
);
CREATE UNIQUE INDEX product_cleanup_reconciliation_predecessor
 ON product_cleanup_reconciliations(order_id,COALESCE(previous_job_id,'00000000-0000-0000-0000-000000000000'::uuid));
CREATE INDEX product_cleanup_reconciliation_order ON product_cleanup_reconciliations(order_id,created_at,job_id);
CREATE INDEX product_cleanup_reconciliation_scan ON product_delivery_snapshots(created_at,order_id) WHERE state<>'removed';
CREATE INDEX product_cleanup_reconciliation_last_job ON jobs ((payload->>'orderId'),created_at DESC,id DESC) WHERE kind='product.delivery_cleanup';

CREATE FUNCTION guard_product_cleanup_reconciliation() RETURNS trigger AS $$
BEGIN
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'product cleanup reconciliation evidence is immutable'; END IF;
 IF NOT EXISTS(SELECT 1 FROM jobs WHERE id=NEW.job_id AND kind='product.delivery_cleanup'
  AND payload->>'orderId'=NEW.order_id::text AND payload->>'retentionCheck'='resolved_order' AND status='queued') OR
 (NEW.previous_job_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM jobs WHERE id=NEW.previous_job_id
  AND kind='product.delivery_cleanup' AND payload->>'orderId'=NEW.order_id::text AND status='succeeded')) THEN
  RAISE EXCEPTION 'product cleanup reconciliation must link matching jobs';
 END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER product_cleanup_reconciliation_guard BEFORE INSERT OR UPDATE OR DELETE ON product_cleanup_reconciliations
 FOR EACH ROW EXECUTE FUNCTION guard_product_cleanup_reconciliation();
