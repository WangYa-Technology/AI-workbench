-- Only derived projections/indexes are removed. Recovery and delivery evidence stays.
DROP INDEX product_delivery_cleanup_jobs_order;
DROP VIEW product_delivery_cleanup_policy;
DROP VIEW product_delivery_cleanup_subjects;
