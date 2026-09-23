DO $$ BEGIN
  IF EXISTS(SELECT 1 FROM product_refund_attempts) THEN
    RAISE EXCEPTION 'cannot discard product refund operation evidence; reconcile and retain it before rollback';
  END IF;
END $$;
DROP TABLE product_refund_attempts;
DROP FUNCTION guard_product_refund_attempt();
