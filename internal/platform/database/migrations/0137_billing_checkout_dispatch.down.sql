LOCK TABLE billing_checkout_requests, billing_checkout_dispatches IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM billing_checkout_requests) THEN
  RAISE EXCEPTION 'cannot discard original billing checkout requests or dispatch evidence';
 END IF;
END $$;
DROP TABLE billing_checkout_dispatches;
DROP TABLE billing_checkout_requests;
