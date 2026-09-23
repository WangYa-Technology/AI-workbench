-- Durable, bounded follow-up after a legal hold ends. This migration does not
-- dispatch jobs or infer that any historical physical deletion completed.
CREATE TABLE legal_hold_cleanup_checks (
 hold_id uuid PRIMARY KEY REFERENCES data_rights_legal_holds(id),
 user_id uuid NOT NULL REFERENCES users(id),
 after_order_id uuid,
 orders_completed boolean NOT NULL DEFAULT false,
 after_user_id uuid,
 completed_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE legal_hold_cleanup_dispatches (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 hold_id uuid NOT NULL REFERENCES legal_hold_cleanup_checks(hold_id),
 kind text NOT NULL CHECK(kind IN ('account','product')),
 user_id uuid REFERENCES users(id),
 order_id uuid REFERENCES orders(id),
 job_id uuid NOT NULL REFERENCES jobs(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 CHECK ((kind='account' AND user_id IS NOT NULL AND order_id IS NULL) OR
        (kind='product' AND order_id IS NOT NULL AND user_id IS NULL))
);
CREATE UNIQUE INDEX legal_hold_cleanup_dispatch_subject ON legal_hold_cleanup_dispatches(hold_id,kind,COALESCE(user_id,order_id));
CREATE INDEX legal_hold_cleanup_dispatch_job ON legal_hold_cleanup_dispatches(job_id);
CREATE INDEX legal_hold_cleanup_pending ON legal_hold_cleanup_checks(created_at,hold_id) WHERE completed_at IS NULL;
CREATE INDEX legal_hold_closed_scan ON data_rights_legal_holds(created_at,id) WHERE status IN ('expired','released');

CREATE FUNCTION guard_legal_hold_cleanup_check() RETURNS trigger AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'legal hold cleanup evidence is immutable'; END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.after_order_id IS NOT NULL OR NEW.orders_completed OR NEW.after_user_id IS NOT NULL OR NEW.completed_at IS NOT NULL OR NOT EXISTS(
   SELECT 1 FROM data_rights_legal_holds h WHERE h.id=NEW.hold_id AND h.user_id=NEW.user_id
   AND (h.status='released' OR (h.status='expired' AND h.expires_at<=now()))
  ) THEN RAISE EXCEPTION 'legal hold cleanup requires an ended hold'; END IF;
 ELSE
  IF (to_jsonb(NEW)-'after_order_id'-'orders_completed'-'after_user_id'-'completed_at') IS DISTINCT FROM (to_jsonb(OLD)-'after_order_id'-'orders_completed'-'after_user_id'-'completed_at')
   OR OLD.completed_at IS NOT NULL
   OR (OLD.orders_completed AND NOT NEW.orders_completed)
   OR (OLD.orders_completed AND NEW.after_order_id IS DISTINCT FROM OLD.after_order_id)
   OR (NOT NEW.orders_completed AND NEW.after_user_id IS NOT NULL)
   OR (NEW.completed_at IS NOT NULL AND NOT NEW.orders_completed)
   OR (OLD.after_user_id IS NOT NULL AND (NEW.after_user_id IS NULL OR NEW.after_user_id<OLD.after_user_id))
   OR (OLD.after_order_id IS NOT NULL AND (NEW.after_order_id IS NULL OR NEW.after_order_id<OLD.after_order_id))
  THEN RAISE EXCEPTION 'legal hold cleanup progress cannot be rewritten'; END IF;
 END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER legal_hold_cleanup_check_guard BEFORE INSERT OR UPDATE OR DELETE ON legal_hold_cleanup_checks
 FOR EACH ROW EXECUTE FUNCTION guard_legal_hold_cleanup_check();
CREATE TRIGGER legal_hold_cleanup_dispatch_immutable BEFORE UPDATE OR DELETE ON legal_hold_cleanup_dispatches
 FOR EACH ROW EXECUTE FUNCTION reject_media_cleanup_recovery_mutation();
