LOCK TABLE point_entries, payment_intent_events IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM point_entries WHERE entry_type='subscription_credit'
  AND metadata->>'provider'='waffo_pancake' AND metadata->>'providerPaymentId' IS NOT NULL)
 OR EXISTS (SELECT 1 FROM payment_intent_events WHERE event_type='subscription.renewed'
  AND evidence->>'provider'='waffo_pancake') THEN
  RAISE EXCEPTION 'recorded subscription renewals prevent downgrade';
 END IF;
END $$;
DROP INDEX payment_intent_events_waffo_subscription_capture;
DROP INDEX point_entries_waffo_subscription_capture;
