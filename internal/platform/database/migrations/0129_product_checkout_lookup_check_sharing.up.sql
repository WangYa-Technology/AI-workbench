-- Replacement locators may both finish authenticating the same session. Their
-- immutable receipts can legitimately share the one active checkout check.
-- Uniqueness remains on lookup job_id and on the active check job itself.
DROP INDEX product_checkout_lookup_check;
CREATE INDEX product_checkout_lookup_check ON product_checkout_lookups(check_job_id)
 WHERE check_job_id IS NOT NULL;
