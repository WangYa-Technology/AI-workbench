-- Drain old API/worker instances first: in-flight external writes cannot be fenced by SQL.
LOCK TABLE jobs IN SHARE ROW EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM jobs WHERE status='running') THEN
  RAISE EXCEPTION 'drain running jobs before enabling generation output journal';
 END IF;
END $$;

CREATE TABLE generation_output_writes (
 id uuid PRIMARY KEY,
 generation_id uuid NOT NULL REFERENCES generations(id),
 owner_id uuid NOT NULL REFERENCES users(id),
 asset_id uuid NOT NULL,
 storage_backend text NOT NULL CHECK(storage_backend IN ('local_file','s3')),
 storage_key text NOT NULL CHECK(length(storage_key) BETWEEN 1 AND 1024),
 checksum_sha256 text NOT NULL CHECK(checksum_sha256 ~ '^[0-9a-f]{64}$'),
 size_bytes bigint NOT NULL CHECK(size_bytes BETWEEN 1 AND 104857600),
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','attached','cleaned')),
 created_at timestamptz NOT NULL DEFAULT now(),
 attached_at timestamptz,
 verified_absent_at timestamptz,
 next_check_at timestamptz NOT NULL DEFAULT now()+interval '15 minutes' CHECK(isfinite(next_check_at)),
 cleanup_checks bigint NOT NULL DEFAULT 0 CHECK(cleanup_checks>=0),
 last_error_code text CHECK(last_error_code IN ('generation_output_cleanup_failed','generation_output_retained','generation_output_conflict')),
 UNIQUE(storage_backend,storage_key),
 CHECK((status='attached')=(attached_at IS NOT NULL)),
 CHECK(status<>'cleaned' OR verified_absent_at IS NOT NULL)
);
CREATE INDEX generation_output_cleanup_due_idx ON generation_output_writes(next_check_at,id) WHERE status<>'attached';
CREATE INDEX generation_output_owner_idx ON generation_output_writes(owner_id,id);
CREATE INDEX generation_output_generation_idx ON generation_output_writes(generation_id,id);

CREATE FUNCTION require_generation_output_protocol() RETURNS void LANGUAGE plpgsql AS $$ BEGIN
 IF current_setting('app.generation_output_protocol',true) IS DISTINCT FROM 'journal-v1' THEN
  RAISE EXCEPTION 'generation-output-aware application required' USING ERRCODE='23514';
 END IF;
END $$;
CREATE FUNCTION check_generation_output_protocol() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 PERFORM require_generation_output_protocol(); RETURN NEW;
END $$;
CREATE TRIGGER generation_output_writer_protocol BEFORE INSERT OR UPDATE ON generations
 FOR EACH ROW EXECUTE FUNCTION check_generation_output_protocol();
-- Includes deletion and cleanup claimers: an older worker must not skip the journal.
CREATE FUNCTION check_generation_output_worker_protocol() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.status='running' OR (TG_OP='UPDATE' AND OLD.status='running') THEN
  PERFORM require_generation_output_protocol();
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER generation_output_worker_protocol BEFORE INSERT OR UPDATE ON jobs
 FOR EACH ROW EXECUTE FUNCTION check_generation_output_worker_protocol();

CREATE FUNCTION protect_generation_output_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 PERFORM require_generation_output_protocol();
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'generation output evidence cannot be deleted'; END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.status<>'pending' OR NOT EXISTS(SELECT 1 FROM generations g JOIN users u ON u.id=g.owner_id
    WHERE g.id=NEW.generation_id AND g.owner_id=NEW.owner_id AND g.status IN ('queued','running') AND u.status='active')
    THEN RAISE EXCEPTION 'invalid generation output intent'; END IF;
 ELSE
  IF (NEW.id,NEW.generation_id,NEW.owner_id,NEW.asset_id,NEW.storage_backend,NEW.storage_key,NEW.checksum_sha256,NEW.size_bytes,NEW.created_at)
    IS DISTINCT FROM (OLD.id,OLD.generation_id,OLD.owner_id,OLD.asset_id,OLD.storage_backend,OLD.storage_key,OLD.checksum_sha256,OLD.size_bytes,OLD.created_at)
    OR (OLD.status='attached' AND NEW IS DISTINCT FROM OLD)
    OR (OLD.status='cleaned' AND NEW.status<>'cleaned') THEN
   RAISE EXCEPTION 'generation output identity and terminal state are immutable';
  END IF;
 END IF;
 IF NEW.status='attached' AND NOT EXISTS(SELECT 1 FROM generations g JOIN assets a ON a.id=g.output_asset_id
   WHERE g.id=NEW.generation_id AND g.status='succeeded' AND a.id=NEW.asset_id AND a.owner_id=NEW.owner_id
   AND a.source_type='generation' AND a.source_id=g.id AND a.storage_backend=NEW.storage_backend AND a.storage_key=NEW.storage_key)
   THEN RAISE EXCEPTION 'generation output attachment lacks committed result binding'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER generation_output_evidence BEFORE INSERT OR UPDATE OR DELETE ON generation_output_writes
 FOR EACH ROW EXECUTE FUNCTION protect_generation_output_write();

-- Prevent a concurrent asset writer from adopting a location which cleanup has retired.
CREATE FUNCTION protect_generation_output_asset() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE w generation_output_writes%ROWTYPE;
BEGIN
 SELECT * INTO w FROM generation_output_writes WHERE storage_backend=NEW.storage_backend AND storage_key=NEW.storage_key FOR UPDATE;
 IF FOUND AND (w.status='cleaned' OR NEW.id<>w.asset_id OR NEW.owner_id<>w.owner_id
   OR NEW.source_type<>'generation' OR NEW.source_id IS DISTINCT FROM w.generation_id) THEN
  RAISE EXCEPTION 'reserved generation output location cannot be reused';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER generation_output_asset_guard BEFORE INSERT OR UPDATE OF storage_backend,storage_key,owner_id,source_id,source_type ON assets
 FOR EACH ROW EXECUTE FUNCTION protect_generation_output_asset();

-- A success update cannot commit without the same-transaction journal attachment.
CREATE FUNCTION check_generation_output_result() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM generation_output_writes w JOIN generations g ON g.id=w.generation_id
 WHERE g.id=NEW.id AND w.status='attached' AND w.asset_id=g.output_asset_id AND w.owner_id=g.owner_id) THEN
  RAISE EXCEPTION 'generation media success requires attached output evidence';
 END IF;
 RETURN NEW;
END $$;
CREATE CONSTRAINT TRIGGER generation_output_result_guard AFTER UPDATE OF status ON generations
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN(OLD.status IS DISTINCT FROM NEW.status AND NEW.status='succeeded' AND NEW.mode<>'chat')
 EXECUTE FUNCTION check_generation_output_result();

ALTER TABLE maintenance_health DROP CONSTRAINT maintenance_health_kind_check;
ALTER TABLE maintenance_health ADD CONSTRAINT maintenance_health_kind_check CHECK(kind IN (
 'legal_hold_expiry','legal_hold_cleanup','product_cleanup_reconciliation','account_deletion_reconciliation',
 'original_media_cleanup_reconciliation','product_refund_reconciliation','generation_output_cleanup'));
