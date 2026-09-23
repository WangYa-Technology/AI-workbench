-- Drain old API/worker instances first: in-flight external writes cannot be fenced by SQL.
LOCK TABLE jobs IN SHARE ROW EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM jobs WHERE status='running') THEN
  RAISE EXCEPTION 'drain running jobs before enabling upload write journal';
 END IF;
END $$;

CREATE TABLE upload_writes (
 id uuid PRIMARY KEY,
 owner_id uuid NOT NULL REFERENCES users(id),
 asset_id uuid NOT NULL UNIQUE,
 storage_backend text NOT NULL CHECK(storage_backend IN ('local_file','s3')),
 storage_key text NOT NULL CHECK(length(storage_key) BETWEEN 1 AND 1024),
 checksum_sha256 text NOT NULL CHECK(checksum_sha256 ~ '^[0-9a-f]{64}$'),
 size_bytes bigint NOT NULL CHECK(size_bytes BETWEEN 1 AND 10485760),
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','attached','cleaned')),
 created_at timestamptz NOT NULL DEFAULT now(),
 attached_at timestamptz,
 verified_absent_at timestamptz,
 next_check_at timestamptz NOT NULL DEFAULT now()+interval '15 minutes' CHECK(isfinite(next_check_at)),
 cleanup_checks bigint NOT NULL DEFAULT 0 CHECK(cleanup_checks>=0),
 last_error_code text CHECK(last_error_code IN ('upload_write_cleanup_failed','upload_write_retained','upload_write_conflict')),
 UNIQUE(storage_backend,storage_key),
 CHECK((status='attached')=(attached_at IS NOT NULL)),
 CHECK(status<>'cleaned' OR verified_absent_at IS NOT NULL)
);
CREATE INDEX upload_write_cleanup_due_idx ON upload_writes(next_check_at,id) WHERE status<>'attached';
CREATE INDEX upload_write_owner_idx ON upload_writes(owner_id,id);

CREATE FUNCTION require_upload_write_protocol() RETURNS void LANGUAGE plpgsql AS $$ BEGIN
 IF current_setting('app.upload_write_protocol',true) IS DISTINCT FROM 'journal-v1' THEN
  RAISE EXCEPTION 'upload-write-aware application required' USING ERRCODE='23514';
 END IF;
END $$;
CREATE FUNCTION check_upload_write_protocol() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 PERFORM require_upload_write_protocol(); RETURN NEW;
END $$;
CREATE TRIGGER upload_write_writer_protocol BEFORE INSERT OR UPDATE ON assets
 FOR EACH ROW WHEN (NEW.source_type='upload') EXECUTE FUNCTION check_upload_write_protocol();
-- Includes deletion and cleanup claimers: an older worker must not skip the journal.
CREATE FUNCTION check_upload_write_worker_protocol() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.status='running' OR (TG_OP='UPDATE' AND OLD.status='running') THEN
  PERFORM require_upload_write_protocol();
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER upload_write_worker_protocol BEFORE INSERT OR UPDATE ON jobs
 FOR EACH ROW EXECUTE FUNCTION check_upload_write_worker_protocol();

CREATE FUNCTION protect_upload_write_evidence() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 PERFORM require_upload_write_protocol();
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'upload write evidence cannot be deleted'; END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.status<>'pending' OR NEW.storage_key !~ ('(^|/)upload-'||NEW.id::text||'\.[a-z0-9]+$')
    OR NOT EXISTS(SELECT 1 FROM users u WHERE u.id=NEW.owner_id AND u.status='active')
    OR EXISTS(SELECT 1 FROM assets a WHERE a.id=NEW.asset_id OR (a.storage_backend=NEW.storage_backend AND a.storage_key=NEW.storage_key))
    THEN RAISE EXCEPTION 'invalid upload write intent'; END IF;
 ELSE
  IF (NEW.id,NEW.owner_id,NEW.asset_id,NEW.storage_backend,NEW.storage_key,NEW.checksum_sha256,NEW.size_bytes,NEW.created_at)
    IS DISTINCT FROM (OLD.id,OLD.owner_id,OLD.asset_id,OLD.storage_backend,OLD.storage_key,OLD.checksum_sha256,OLD.size_bytes,OLD.created_at)
    OR (OLD.status='attached' AND NEW IS DISTINCT FROM OLD)
    OR (OLD.status='cleaned' AND NEW.status<>'cleaned') THEN
   RAISE EXCEPTION 'upload write identity and terminal state are immutable';
  END IF;
 END IF;
 IF NEW.status='attached' AND NOT EXISTS(SELECT 1 FROM assets a
   WHERE a.id=NEW.asset_id AND a.owner_id=NEW.owner_id AND a.source_type='upload'
   AND a.storage_backend=NEW.storage_backend AND a.storage_key=NEW.storage_key AND a.size_bytes=NEW.size_bytes)
   THEN RAISE EXCEPTION 'upload attachment lacks committed asset binding'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER upload_write_evidence BEFORE INSERT OR UPDATE OR DELETE ON upload_writes
 FOR EACH ROW EXECUTE FUNCTION protect_upload_write_evidence();

-- Prevent a concurrent asset writer from adopting a location which cleanup has retired.
CREATE FUNCTION protect_upload_write_asset() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE w upload_writes%ROWTYPE;
BEGIN
 SELECT * INTO w FROM upload_writes WHERE storage_backend=NEW.storage_backend AND storage_key=NEW.storage_key FOR UPDATE;
 IF NOT FOUND AND NEW.storage_key ~ '(^|/)upload-' THEN
  RAISE EXCEPTION 'reserved upload location requires journal evidence';
 END IF;
 IF FOUND AND (w.status='cleaned' OR NEW.id<>w.asset_id OR NEW.owner_id<>w.owner_id
   OR NEW.source_type<>'upload' OR NEW.size_bytes IS DISTINCT FROM w.size_bytes) THEN
  RAISE EXCEPTION 'reserved upload write location cannot be reused';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER upload_write_asset_guard BEFORE INSERT OR UPDATE OF storage_backend,storage_key,owner_id,source_id,source_type,size_bytes ON assets
 FOR EACH ROW EXECUTE FUNCTION protect_upload_write_asset();

-- Locations reserved by the journal cannot commit a partially attached asset.
-- Pre-journal historical uploads remain valid; their evidence is not fabricated.
CREATE FUNCTION check_upload_write_result() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF EXISTS(SELECT 1 FROM upload_writes WHERE asset_id=NEW.id) AND NOT EXISTS(
  SELECT 1 FROM upload_writes w JOIN assets a ON a.id=w.asset_id
  WHERE a.id=NEW.id AND w.status='attached' AND w.owner_id=a.owner_id
  AND w.storage_backend=a.storage_backend AND w.storage_key=a.storage_key AND w.size_bytes=a.size_bytes) THEN
  RAISE EXCEPTION 'journaled upload requires attached write evidence';
 END IF;
 RETURN NEW;
END $$;
CREATE CONSTRAINT TRIGGER upload_write_result_guard AFTER INSERT ON assets
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN(NEW.source_type='upload')
 EXECUTE FUNCTION check_upload_write_result();

ALTER TABLE maintenance_health DROP CONSTRAINT maintenance_health_kind_check;
ALTER TABLE maintenance_health ADD CONSTRAINT maintenance_health_kind_check CHECK(kind IN (
 'legal_hold_expiry','legal_hold_cleanup','product_cleanup_reconciliation','account_deletion_reconciliation',
 'original_media_cleanup_reconciliation','product_refund_reconciliation','generation_output_cleanup','generation_execution_recovery','asset_scan_execution_recovery','upload_write_cleanup'));
