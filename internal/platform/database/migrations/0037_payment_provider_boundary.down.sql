DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM payment_provider_events) THEN
    RAISE EXCEPTION '0037 cannot be rolled back after payment provider evidence exists';
  END IF;
END;
$$;

DROP TRIGGER IF EXISTS payment_provider_event_processing_terminal ON payment_provider_event_processing;
DROP FUNCTION IF EXISTS protect_terminal_payment_event_processing();
DROP TRIGGER IF EXISTS payment_provider_events_immutable ON payment_provider_events;
DROP FUNCTION IF EXISTS reject_payment_provider_event_mutation();
DROP TABLE IF EXISTS payment_provider_event_processing;
DROP TABLE IF EXISTS payment_provider_events;
