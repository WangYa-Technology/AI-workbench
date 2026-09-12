CREATE EXTENSION IF NOT EXISTS pgcrypto WITH SCHEMA public;

ALTER TABLE audit_events
  ADD COLUMN sequence bigint,
  ADD COLUMN previous_hash text,
  ADD COLUMN event_hash text;

CREATE FUNCTION audit_event_hash(
  event_sequence bigint,
  previous_event_hash text,
  event_id uuid,
  event_actor_id uuid,
  event_action text,
  event_resource_type text,
  event_resource_id uuid,
  event_reason text,
  event_request_id text,
  event_metadata jsonb,
  event_created_at timestamptz
) RETURNS text AS $$
  SELECT encode(public.digest(concat_ws('|',
    event_sequence::text,
    COALESCE(previous_event_hash,''),
    event_id::text,
    COALESCE(event_actor_id::text,''),
    event_action,
    event_resource_type,
    COALESCE(event_resource_id::text,''),
    COALESCE(event_reason,''),
    event_request_id,
    event_metadata::text,
    to_char(event_created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"')
  ), 'sha256'), 'hex')
$$ LANGUAGE sql IMMUTABLE;

DO $$
DECLARE
  item audit_events%ROWTYPE;
  next_sequence bigint := 0;
  previous_event_hash text := '';
  next_hash text;
BEGIN
  FOR item IN SELECT * FROM audit_events ORDER BY created_at,id LOOP
    next_sequence := next_sequence + 1;
    next_hash := audit_event_hash(next_sequence,NULLIF(previous_event_hash,''),item.id,item.actor_id,item.action,item.resource_type,item.resource_id,item.reason,item.request_id,item.metadata,item.created_at);
    UPDATE audit_events SET sequence=next_sequence,previous_hash=NULLIF(previous_event_hash,''),event_hash=next_hash WHERE id=item.id;
    previous_event_hash := next_hash;
  END LOOP;
END $$;

ALTER TABLE audit_events
  ALTER COLUMN sequence SET NOT NULL,
  ALTER COLUMN event_hash SET NOT NULL,
  ADD CONSTRAINT audit_events_sequence_unique UNIQUE(sequence),
  ADD CONSTRAINT audit_events_hash_unique UNIQUE(event_hash),
  ADD CONSTRAINT audit_events_hash_format CHECK (event_hash ~ '^[0-9a-f]{64}$'),
  ADD CONSTRAINT audit_events_previous_hash_format CHECK (previous_hash IS NULL OR previous_hash ~ '^[0-9a-f]{64}$');

CREATE TABLE audit_chain_state (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  head_sequence bigint NOT NULL CHECK (head_sequence >= 0),
  head_hash text NOT NULL CHECK (head_hash = '' OR head_hash ~ '^[0-9a-f]{64}$'),
  updated_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO audit_chain_state(singleton,head_sequence,head_hash)
SELECT true,COALESCE(max(sequence),0),COALESCE((array_agg(event_hash ORDER BY sequence DESC))[1],'') FROM audit_events;

CREATE FUNCTION append_audit_event_chain() RETURNS trigger AS $$
DECLARE
  current_sequence bigint;
  current_hash text;
BEGIN
  SELECT head_sequence,head_hash INTO current_sequence,current_hash FROM audit_chain_state WHERE singleton=true FOR UPDATE;
  NEW.sequence := current_sequence + 1;
  NEW.previous_hash := NULLIF(current_hash,'');
  NEW.event_hash := audit_event_hash(NEW.sequence,NEW.previous_hash,NEW.id,NEW.actor_id,NEW.action,NEW.resource_type,NEW.resource_id,NEW.reason,NEW.request_id,NEW.metadata,NEW.created_at);
  UPDATE audit_chain_state SET head_sequence=NEW.sequence,head_hash=NEW.event_hash,updated_at=now() WHERE singleton=true;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE FUNCTION reject_audit_event_mutation() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'audit events are immutable'; END; $$ LANGUAGE plpgsql;

CREATE TRIGGER audit_events_chain BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION append_audit_event_chain();
CREATE TRIGGER audit_events_immutable BEFORE UPDATE OR DELETE ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_audit_event_mutation();

CREATE TABLE request_observations (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  request_id text NOT NULL CHECK (char_length(request_id) BETWEEN 1 AND 128),
  method text NOT NULL CHECK (method IN ('GET','POST','PUT','PATCH','DELETE','OPTIONS','HEAD')),
  route text NOT NULL CHECK (char_length(route) BETWEEN 1 AND 240),
  status integer NOT NULL CHECK (status BETWEEN 100 AND 599),
  duration_ms bigint NOT NULL CHECK (duration_ms >= 0),
  response_bytes bigint NOT NULL CHECK (response_bytes >= 0),
  occurred_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX request_observations_window_idx ON request_observations(occurred_at DESC);
CREATE INDEX request_observations_route_status_idx ON request_observations(route,status,occurred_at DESC);

INSERT INTO permissions(id,module,description,risk_level,resource_authorization) VALUES
  ('admin:observability','admin','Read operational diagnostics and audit-chain integrity','medium',false)
ON CONFLICT (id) DO NOTHING;
INSERT INTO role_permissions(role,permission_id) VALUES ('admin','admin:observability') ON CONFLICT DO NOTHING;
