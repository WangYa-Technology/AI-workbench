DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM billing_stripe_checkout_bindings)
 OR EXISTS(SELECT 1 FROM billing_stripe_webhook_bindings)
 OR EXISTS(SELECT 1 FROM payment_provider_events e
  JOIN payment_provider_event_processing s ON s.event_id=e.id
  LEFT JOIN payment_intents p ON p.id=e.payment_id
  WHERE e.provider='stripe' AND (e.purpose IN ('wallet_topup','subscription') OR p.purpose IN ('wallet_topup','subscription'))
  AND s.status NOT IN ('processed','ignored')) THEN
  RAISE EXCEPTION 'cannot discard verified billing Stripe webhook bindings';
 END IF;
END $$;
DROP TABLE billing_stripe_webhook_bindings;
DROP TABLE billing_stripe_checkout_bindings;
