CREATE TABLE payment_provider_events (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  provider text NOT NULL CHECK (provider IN ('stripe')),
  provider_event_id text NOT NULL CHECK (char_length(provider_event_id) BETWEEN 8 AND 255 AND provider_event_id ~ '^[A-Za-z0-9_]+$'),
  event_type text NOT NULL CHECK (char_length(event_type) BETWEEN 3 AND 120 AND event_type ~ '^[a-z0-9_.]+$'),
  api_version text NOT NULL CHECK (char_length(api_version) BETWEEN 10 AND 40),
  live_mode boolean NOT NULL,
  occurred_at timestamptz NOT NULL,
  payload_sha256 text NOT NULL CHECK (payload_sha256 ~ '^[a-f0-9]{64}$'),
  object_id text NOT NULL CHECK (char_length(object_id) BETWEEN 6 AND 255 AND object_id ~ '^[A-Za-z0-9_]+$'),
  object_type text NOT NULL CHECK (char_length(object_type) BETWEEN 3 AND 80 AND object_type ~ '^[a-z0-9_.]+$'),
  payment_id uuid,
  resource_id uuid,
  purpose text CHECK (purpose IS NULL OR purpose IN ('product','task')),
  amount_cents bigint CHECK (amount_cents IS NULL OR amount_cents BETWEEN 0 AND 99999999),
  currency text CHECK (currency IS NULL OR currency ~ '^[A-Z]{3}$'),
  payment_status text CHECK (payment_status IS NULL OR (char_length(payment_status) BETWEEN 2 AND 80 AND payment_status ~ '^[a-z0-9_]+$')),
  provider_payment_id text CHECK (provider_payment_id IS NULL OR (char_length(provider_payment_id) BETWEEN 6 AND 255 AND provider_payment_id ~ '^[A-Za-z0-9_]+$')),
  provider_charge_id text CHECK (provider_charge_id IS NULL OR (char_length(provider_charge_id) BETWEEN 6 AND 255 AND provider_charge_id ~ '^[A-Za-z0-9_]+$')),
  provider_transfer_id text CHECK (provider_transfer_id IS NULL OR (char_length(provider_transfer_id) BETWEEN 6 AND 255 AND provider_transfer_id ~ '^[A-Za-z0-9_]+$')),
  destination_id text CHECK (destination_id IS NULL OR (char_length(destination_id) BETWEEN 6 AND 255 AND destination_id ~ '^[A-Za-z0-9_]+$')),
  received_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(provider,provider_event_id)
);
CREATE INDEX payment_provider_events_payment_idx ON payment_provider_events(payment_id,occurred_at,id) WHERE payment_id IS NOT NULL;
CREATE INDEX payment_provider_events_received_idx ON payment_provider_events(received_at DESC,id DESC);

CREATE TABLE payment_provider_event_processing (
  event_id uuid PRIMARY KEY REFERENCES payment_provider_events(id),
  status text NOT NULL CHECK (status IN ('received','processing','retry_scheduled','processed','ignored','failed')),
  attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count BETWEEN 0 AND 20),
  replay_count integer NOT NULL DEFAULT 0 CHECK (replay_count BETWEEN 0 AND 20),
  version integer NOT NULL DEFAULT 1 CHECK (version > 0),
  error_code text CHECK (error_code IS NULL OR (char_length(error_code) BETWEEN 2 AND 80 AND error_code ~ '^[a-z0-9_]+$')),
  last_error_code text CHECK (last_error_code IS NULL OR (char_length(last_error_code) BETWEEN 2 AND 80 AND last_error_code ~ '^[a-z0-9_]+$')),
  next_attempt_at timestamptz,
  processed_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (
    (status IN ('processed','ignored') AND processed_at IS NOT NULL AND next_attempt_at IS NULL AND error_code IS NULL) OR
    (status='retry_scheduled' AND processed_at IS NULL AND next_attempt_at IS NOT NULL AND error_code IS NOT NULL) OR
    (status='failed' AND processed_at IS NOT NULL AND next_attempt_at IS NULL AND error_code IS NOT NULL) OR
    (status IN ('received','processing') AND processed_at IS NULL AND next_attempt_at IS NULL AND error_code IS NULL)
  )
);
CREATE INDEX payment_provider_event_processing_due_idx ON payment_provider_event_processing(next_attempt_at,event_id) WHERE status='retry_scheduled';
CREATE INDEX payment_provider_event_processing_status_idx ON payment_provider_event_processing(status,updated_at DESC,event_id);

CREATE FUNCTION reject_payment_provider_event_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'payment provider event evidence is immutable';
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER payment_provider_events_immutable
BEFORE UPDATE OR DELETE ON payment_provider_events
FOR EACH ROW EXECUTE FUNCTION reject_payment_provider_event_mutation();

CREATE FUNCTION protect_terminal_payment_event_processing() RETURNS trigger AS $$
BEGIN
  IF OLD.status IN ('processed','ignored') AND ROW(NEW.*) IS DISTINCT FROM ROW(OLD.*) THEN
    RAISE EXCEPTION 'terminal payment event processing evidence is immutable';
  END IF;
  IF OLD.status='failed' AND ROW(NEW.*) IS DISTINCT FROM ROW(OLD.*) AND NOT (
    NEW.status='received' AND NEW.processed_at IS NULL AND NEW.next_attempt_at IS NULL AND NEW.error_code IS NULL AND
    NEW.last_error_code=OLD.error_code AND NEW.replay_count=OLD.replay_count+1 AND NEW.version=OLD.version+1 AND
    NEW.attempt_count=OLD.attempt_count AND NEW.event_id=OLD.event_id AND NEW.created_at=OLD.created_at
  ) THEN
    RAISE EXCEPTION 'failed payment event can only enter an audited replay';
  END IF;
  IF OLD.status<>'failed' AND (NEW.replay_count<>OLD.replay_count OR NEW.last_error_code IS DISTINCT FROM OLD.last_error_code) AND NOT (
    NEW.status=OLD.status AND NEW.replay_count=OLD.replay_count+1 AND NEW.last_error_code IS NOT DISTINCT FROM OLD.last_error_code AND
    NEW.version=OLD.version+1 AND NEW.attempt_count=OLD.attempt_count AND NEW.error_code IS NOT DISTINCT FROM OLD.error_code AND
    NEW.next_attempt_at IS NOT DISTINCT FROM OLD.next_attempt_at AND NEW.processed_at IS NOT DISTINCT FROM OLD.processed_at AND
    NEW.event_id=OLD.event_id AND NEW.created_at=OLD.created_at
  ) THEN
    RAISE EXCEPTION 'payment event replay evidence must preserve processing state';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER payment_provider_event_processing_terminal
BEFORE UPDATE ON payment_provider_event_processing
FOR EACH ROW EXECUTE FUNCTION protect_terminal_payment_event_processing();
