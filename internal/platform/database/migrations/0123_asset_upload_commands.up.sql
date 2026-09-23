-- Drain old upload writers and workers before applying the command protocol.
LOCK TABLE jobs IN SHARE ROW EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM jobs WHERE status='running') THEN
  RAISE EXCEPTION 'drain running jobs before enabling upload commands';
 END IF;
END $$;
CREATE TABLE asset_upload_commands (
 id uuid PRIMARY KEY,
 owner_id uuid NOT NULL REFERENCES users(id),
 idempotency_key text NOT NULL CHECK(idempotency_key ~ '^[A-Za-z0-9._:-]{8,128}$'),
 request_hash text NOT NULL CHECK(request_hash ~ '^[0-9a-f]{64}$'),
 result_asset_id uuid UNIQUE REFERENCES assets(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 completed_at timestamptz,
 UNIQUE(owner_id,idempotency_key),
 CHECK((result_asset_id IS NULL)=(completed_at IS NULL))
);
CREATE TABLE asset_upload_attempts (
 command_id uuid NOT NULL REFERENCES asset_upload_commands(id),
 write_id uuid PRIMARY KEY REFERENCES upload_writes(id),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX asset_upload_attempts_command_idx ON asset_upload_attempts(command_id,created_at,write_id);

CREATE FUNCTION require_upload_command_protocol() RETURNS void LANGUAGE plpgsql AS $$ BEGIN
 IF current_setting('app.upload_command_protocol',true) IS DISTINCT FROM 'command-v1' THEN
  RAISE EXCEPTION 'upload-command-aware application required' USING ERRCODE='23514';
 END IF;
END $$;
CREATE FUNCTION check_upload_command_protocol() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 PERFORM require_upload_command_protocol(); RETURN NEW;
END $$;
CREATE TRIGGER upload_command_writer_protocol BEFORE INSERT OR UPDATE ON assets
 FOR EACH ROW WHEN (NEW.source_type='upload') EXECUTE FUNCTION check_upload_command_protocol();
CREATE FUNCTION check_upload_command_worker_protocol() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.status='running' OR (TG_OP='UPDATE' AND OLD.status='running') THEN
  PERFORM require_upload_command_protocol();
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER upload_command_worker_protocol BEFORE INSERT OR UPDATE ON jobs
 FOR EACH ROW EXECUTE FUNCTION check_upload_command_worker_protocol();

CREATE FUNCTION protect_upload_command() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 PERFORM require_upload_command_protocol();
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'upload command evidence cannot be deleted'; END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.result_asset_id IS NOT NULL OR NOT EXISTS(SELECT 1 FROM users WHERE id=NEW.owner_id AND status='active') THEN
   RAISE EXCEPTION 'invalid upload command';
  END IF;
 ELSE
  IF (NEW.id,NEW.owner_id,NEW.idempotency_key,NEW.request_hash,NEW.created_at)
   IS DISTINCT FROM (OLD.id,OLD.owner_id,OLD.idempotency_key,OLD.request_hash,OLD.created_at)
   OR (OLD.result_asset_id IS NOT NULL AND NEW IS DISTINCT FROM OLD) THEN
   RAISE EXCEPTION 'upload command identity and result are immutable';
  END IF;
 END IF;
 IF NEW.result_asset_id IS NOT NULL AND NOT EXISTS(
  SELECT 1 FROM asset_upload_attempts t JOIN upload_writes w ON w.id=t.write_id
  JOIN assets a ON a.id=w.asset_id WHERE t.command_id=NEW.id AND a.id=NEW.result_asset_id
  AND w.status='attached' AND w.owner_id=NEW.owner_id AND a.owner_id=NEW.owner_id
  AND a.source_type='upload' AND a.storage_backend=w.storage_backend AND a.storage_key=w.storage_key AND a.size_bytes=w.size_bytes
 ) THEN RAISE EXCEPTION 'upload command result requires attached ownership evidence'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER upload_command_evidence BEFORE INSERT OR UPDATE OR DELETE ON asset_upload_commands
 FOR EACH ROW EXECUTE FUNCTION protect_upload_command();

CREATE FUNCTION protect_upload_attempt() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 PERFORM require_upload_command_protocol();
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'upload attempt evidence is immutable'; END IF;
 -- Serialize attempts with result completion, including direct SQL writers.
 PERFORM 1 FROM asset_upload_commands WHERE id=NEW.command_id FOR UPDATE;
 IF NOT EXISTS(SELECT 1 FROM asset_upload_commands c JOIN upload_writes w ON w.owner_id=c.owner_id
 WHERE c.id=NEW.command_id AND c.result_asset_id IS NULL AND w.id=NEW.write_id AND w.status='pending') THEN
  RAISE EXCEPTION 'upload attempt requires matching pending ownership';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER upload_attempt_evidence BEFORE INSERT OR UPDATE OR DELETE ON asset_upload_attempts
 FOR EACH ROW EXECUTE FUNCTION protect_upload_attempt();

-- Do not fabricate commands for historical uploads. Every new reserved upload
-- location must finish its command in the same transaction as asset attachment.
CREATE FUNCTION check_upload_command_result() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM asset_upload_commands c JOIN asset_upload_attempts t ON t.command_id=c.id
 JOIN upload_writes w ON w.id=t.write_id WHERE c.result_asset_id=NEW.id AND c.owner_id=NEW.owner_id
 AND w.asset_id=NEW.id AND w.status='attached') THEN
  RAISE EXCEPTION 'journaled upload requires completed command';
 END IF;
 RETURN NEW;
END $$;
CREATE CONSTRAINT TRIGGER upload_command_result_guard AFTER INSERT ON assets
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN(NEW.source_type='upload' AND NEW.storage_key ~ '(^|/)upload-')
 EXECUTE FUNCTION check_upload_command_result();
