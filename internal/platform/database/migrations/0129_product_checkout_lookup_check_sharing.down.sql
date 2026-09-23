-- Never remove or rewrite receipts to restore the older one-to-one index.
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM product_checkout_lookups WHERE check_job_id IS NOT NULL
   GROUP BY check_job_id HAVING count(*)>1) THEN
  RAISE EXCEPTION 'cannot discard shared checkout lookup evidence';
 END IF;
END $$;
DROP INDEX product_checkout_lookup_check;
CREATE UNIQUE INDEX product_checkout_lookup_check ON product_checkout_lookups(check_job_id)
 WHERE check_job_id IS NOT NULL;
