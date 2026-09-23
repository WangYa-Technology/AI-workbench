DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM billing_waffo_webhook_bindings)
 OR EXISTS(SELECT 1 FROM payment_provider_events e
   JOIN payment_provider_event_processing p ON p.event_id=e.id
   LEFT JOIN payment_intents pi ON pi.id=e.payment_id
   WHERE e.provider='waffo_pancake'
   AND (e.purpose IN ('wallet_topup','subscription') OR pi.purpose IN ('wallet_topup','subscription'))
   AND p.status NOT IN ('processed','ignored')) THEN
  RAISE EXCEPTION 'cannot discard verified billing Waffo webhook bindings';
 END IF;
END $$;
DROP TABLE billing_waffo_webhook_bindings;
