-- Evidence lookups and counts are scoped by the original operation. The older
-- user-leading indexes do not support this lookup for a large account ledger.
CREATE INDEX legacy_product_billing_operation_idx
 ON billing_entries(operation_id,entry_type,direction,user_id) INCLUDE(amount_cents,currency);

-- Compatibility for recorded, internal-only historical purchases. Absence of
-- a provider intent is not evidence of a local payment. Require the original
-- audit and both balanced accounting records; never derive payee from products.
CREATE VIEW legacy_product_refund_evidence AS
 SELECT o.id AS order_id, credit.user_id AS seller_id
 FROM orders o
 LEFT JOIN billing_entries credit ON credit.operation_id=o.id
   AND credit.entry_type='product_sale' AND credit.direction='credit'
 WHERE NOT EXISTS(SELECT 1 FROM payment_intents pi WHERE pi.order_id=o.id)
 AND EXISTS(SELECT 1 FROM audit_events a WHERE a.resource_type='order' AND a.resource_id=o.id
   AND a.actor_id=o.buyer_id AND a.action='marketplace.test_purchase'
   AND a.metadata->>'realCharge'='false' AND a.metadata->>'paymentMode'='test')
 AND (
  (o.amount_cents>0 AND credit.user_id<>o.buyer_id AND credit.amount_cents=o.amount_cents AND credit.currency=o.currency
   AND (SELECT count(*) FROM billing_entries b WHERE b.operation_id=o.id)=2
   AND EXISTS(SELECT 1 FROM billing_entries debit WHERE debit.operation_id=o.id
     AND debit.user_id=o.buyer_id AND debit.entry_type='product_purchase' AND debit.direction='debit'
     AND debit.amount_cents=o.amount_cents AND debit.currency=o.currency)
   AND (SELECT count(*) FROM ledger_entries l WHERE l.operation_id=o.id)=2
   AND EXISTS(SELECT 1 FROM ledger_entries l WHERE l.operation_id=o.id AND l.account_id=o.buyer_id
     AND l.direction='debit' AND l.amount_cents=o.amount_cents AND l.currency=o.currency
     AND l.reason='test_purchase_debit_no_real_charge')
   AND EXISTS(SELECT 1 FROM ledger_entries l WHERE l.operation_id=o.id AND l.account_id=credit.user_id
     AND l.direction='credit' AND l.amount_cents=o.amount_cents AND l.currency=o.currency
     AND l.reason='test_purchase_credit_no_real_payout'))
  OR (o.amount_cents=0 AND credit.user_id IS NULL
    AND NOT EXISTS(SELECT 1 FROM billing_entries b WHERE b.operation_id=o.id)
    AND NOT EXISTS(SELECT 1 FROM ledger_entries l WHERE l.operation_id=o.id))
 );
