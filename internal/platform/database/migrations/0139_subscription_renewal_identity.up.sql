-- A new webhook delivery ID must not credit the same renewal capture again.
-- Unique ledger writes also abort the entire transaction of pre-upgrade workers.
-- Existing duplicates deliberately stop migration for financial reconciliation.
CREATE UNIQUE INDEX point_entries_waffo_subscription_capture
 ON point_entries ((metadata->>'paymentId'),(metadata->>'providerPaymentId'))
 WHERE entry_type='subscription_credit' AND metadata->>'provider'='waffo_pancake';

CREATE UNIQUE INDEX payment_intent_events_waffo_subscription_capture
 ON payment_intent_events (payment_id,(evidence->>'providerPaymentId'))
 WHERE event_type='subscription.renewed' AND evidence->>'provider'='waffo_pancake';
