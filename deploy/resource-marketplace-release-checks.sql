-- Read-only pre/post-cutover evidence. Run only against the approved target.
-- Counts/sums are diagnostics, NOT provider/bank reconciliation or a GO decision.
\set ON_ERROR_STOP on
BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL statement_timeout = '5s';
SET LOCAL lock_timeout = '1s';
SELECT current_database() AS database, current_schema() AS schema, now() AS snapshot_at;
SELECT count(*) AS migration_count, min(version) AS first, max(version) AS latest FROM schema_migrations;
SELECT version FROM schema_migrations ORDER BY version;
SELECT n.nspname, c.relname FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid
 JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=current_schema() AND NOT i.indisvalid;
SELECT c.relname, t.tgname, t.tgenabled FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid
 JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=current_schema() AND NOT t.tgisinternal AND t.tgenabled='D';
SELECT kind,status,count(*) AS jobs FROM jobs GROUP BY kind,status ORDER BY kind,status;
SELECT count(*) AS expired_running_leases FROM jobs WHERE status='running' AND lease_expires_at<clock_timestamp();
SELECT provider,live_mode,purpose,status,currency,count(*) AS records,sum(amount_cents) AS amount_cents
 FROM payment_intents GROUP BY provider,live_mode,purpose,status,currency ORDER BY 1,2,3,4,5;
SELECT provider,live_mode,status,currency,count(*) AS records,sum(gross_amount_cents) AS gross_cents,
 sum(fee_cents) AS fee_cents,sum(net_amount_cents) AS net_cents
 FROM product_settlements GROUP BY provider,live_mode,status,currency ORDER BY 1,2,3,4;
SELECT entry_type,currency,count(*) AS records,sum(amount_cents) AS amount_cents
 FROM seller_ledger_entries GROUP BY entry_type,currency ORDER BY 1,2;
SELECT status,currency,count(*) AS requests,sum(amount_cents) AS amount_cents
 FROM seller_payout_requests GROUP BY status,currency ORDER BY 1,2;
SELECT provider,live_mode,action_status,count(*) AS disputes FROM product_payment_disputes GROUP BY 1,2,3 ORDER BY 1,2,3;
SELECT count(*) AS immutable_dispute_adjudications FROM product_payment_dispute_adjudications;
SELECT count(*) AS source_dispositions FROM seller_source_reversal_dispositions;
SELECT count(*) AS source_closures FROM seller_source_reversal_closures;
COMMIT;
