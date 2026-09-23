// Package sellerfunds shares the read-only account projection between payments
// and private data exports. It never authorizes or mutates a funds operation.
package sellerfunds

// AccountsQuery takes the owner UUID as $1 and returns one row per classified
// financial account. Unknown evidence is excluded and blocks withdrawals.
// transaction_timestamp fixes the availability cutoff across a repeatable-read
// export; statement_timestamp would advance as its individual sections stream.
// Callers must choose an explicit public field allowlist from these columns.
const AccountsQuery = `WITH scopes AS MATERIALIZED (
 SELECT * FROM seller_funds_settlement_scopes WHERE seller_id=$1
 ), unscoped AS (SELECT count(*) n FROM seller_funds_unscoped WHERE seller_id=$1),
 totals AS (
 SELECT DISTINCT account_id,provider,live_mode,currency FROM scopes WHERE account_id IS NOT NULL
 ), credits AS (
 SELECT e.account_id,SUM(e.amount_cents) n FROM seller_funds_ledger_scopes e
 JOIN product_settlements s ON s.id=e.settlement_id
 WHERE e.seller_id=$1 AND e.entry_type='settlement_credit' AND e.available_at<=transaction_timestamp() AND s.status='available'
 GROUP BY e.account_id
 ), bank_spent AS (
 SELECT e.account_id,SUM(e.amount_cents) FILTER (WHERE e.entry_type='payout_debit') debited,
 SUM(e.amount_cents) FILTER (WHERE e.entry_type='payout_return') returned
 FROM seller_funds_ledger_scopes e JOIN seller_bank_payout_commands c ON c.payout_request_id=e.payout_request_id
 JOIN product_settlements s ON s.id=c.settlement_id
 WHERE e.seller_id=$1 AND e.entry_type IN ('payout_debit','payout_return') AND s.status='available'
 GROUP BY e.account_id
 ), pending AS (
 SELECT a.account_id,SUM(s.net_amount_cents) n FROM scopes a JOIN product_settlements s ON s.id=a.settlement_id
 WHERE s.status='pending_hold' GROUP BY a.account_id
 ), reserved AS (
 SELECT e.account_id,SUM(e.amount_cents) n FROM seller_funds_ledger_scopes e JOIN seller_payout_requests r ON r.id=e.payout_request_id
 WHERE e.seller_id=$1 AND e.entry_type='payout_reservation' AND r.status IN ('requested','under_review','processing','reconciliation_required') GROUP BY e.account_id
 ), recovery AS (
 SELECT account_id,SUM(remaining_cents) n FROM seller_funds_recovery_scopes WHERE seller_id=$1 AND status IN ('open','reconciliation_required') GROUP BY account_id
 )
 SELECT t.account_id,t.provider,t.live_mode,t.currency,COALESCE(p.n,0) AS pending_cents,
 COALESCE(c.n,0) AS eligible_credit_cents,COALESCE(b.debited,0) AS bank_debited_cents,
 COALESCE(b.returned,0) AS bank_returned_cents,
 GREATEST(0,COALESCE(c.n,0)-COALESCE(b.debited,0)+COALESCE(b.returned,0)) AS available_cents,
 COALESCE(r.n,0) AS reserved_cents,COALESCE(d.n,0) AS recovery_due_cents,
 CASE WHEN (SELECT n FROM unscoped)>0 OR COALESCE(d.n,0)>0 THEN 0
 ELSE GREATEST(0,COALESCE(c.n,0)-COALESCE(b.debited,0)+COALESCE(b.returned,0)-COALESCE(r.n,0)) END AS withdrawable_cents
 FROM totals t LEFT JOIN credits c USING(account_id) LEFT JOIN pending p USING(account_id)
 LEFT JOIN reserved r USING(account_id) LEFT JOIN recovery d USING(account_id) LEFT JOIN bank_spent b USING(account_id)
 ORDER BY t.live_mode DESC,t.provider,t.account_id`
