-- Stop/drain old binaries before upgrading. No external request is recalled by SQL.
LOCK TABLE jobs IN SHARE ROW EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM jobs WHERE status='running') THEN
  RAISE EXCEPTION 'drain running jobs before enabling generation execution recovery';
 END IF;
END $$;

CREATE TABLE generation_executions (
 generation_id uuid PRIMARY KEY REFERENCES generations(id),
 job_id uuid NOT NULL UNIQUE REFERENCES jobs(id),
 recovery_after timestamptz NOT NULL DEFAULT now() CHECK(isfinite(recovery_after)),
 recovery_checks bigint NOT NULL DEFAULT 0 CHECK(recovery_checks>=0),
 last_error_code text CHECK(last_error_code='generation_recovery_failed')
);
-- Only an unambiguous original job is evidence. Missing/duplicate/malformed
-- historical jobs remain unbound and require explicit investigation.
INSERT INTO generation_executions(generation_id,job_id)
 SELECT g.id,(array_agg(j.id))[1] FROM generations g JOIN jobs j
 ON j.kind='generation.generate' AND j.payload->>'generationId'=g.id::text
 GROUP BY g.id HAVING count(*)=1;
CREATE INDEX generation_execution_recovery_due_idx ON generation_executions(recovery_after,generation_id);

CREATE FUNCTION require_generation_execution_protocol() RETURNS void LANGUAGE plpgsql AS $$ BEGIN
 IF current_setting('app.generation_execution_protocol',true) IS DISTINCT FROM 'lease-v1' THEN
  RAISE EXCEPTION 'generation-execution-aware application required' USING ERRCODE='23514';
 END IF;
END $$;
CREATE FUNCTION check_generation_execution_writer() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 PERFORM require_generation_execution_protocol(); RETURN NEW;
END $$;
CREATE TRIGGER generation_execution_writer BEFORE INSERT OR UPDATE ON generations
 FOR EACH ROW EXECUTE FUNCTION check_generation_execution_writer();
CREATE FUNCTION protect_generation_execution_job() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.status='running' OR (TG_OP='UPDATE' AND OLD.status='running') THEN
  PERFORM require_generation_execution_protocol();
 END IF;
 IF TG_OP='UPDATE' AND (NEW.kind,NEW.payload) IS DISTINCT FROM (OLD.kind,OLD.payload)
 AND EXISTS(SELECT 1 FROM generation_executions WHERE job_id=OLD.id) THEN
  RAISE EXCEPTION 'bound generation job identity is immutable';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER generation_execution_job_guard BEFORE INSERT OR UPDATE ON jobs
 FOR EACH ROW EXECUTE FUNCTION protect_generation_execution_job();
CREATE FUNCTION protect_generation_execution() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 PERFORM require_generation_execution_protocol();
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'generation execution evidence cannot be deleted'; END IF;
 IF TG_OP='UPDATE' AND (NEW.generation_id,NEW.job_id) IS DISTINCT FROM (OLD.generation_id,OLD.job_id) THEN
  RAISE EXCEPTION 'generation execution binding is immutable';
 END IF;
 IF TG_OP='INSERT' AND NOT EXISTS(SELECT 1 FROM jobs WHERE id=NEW.job_id
   AND kind='generation.generate' AND payload->>'generationId'=NEW.generation_id::text) THEN
  RAISE EXCEPTION 'generation execution requires matching job';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER generation_execution_guard BEFORE INSERT OR UPDATE OR DELETE ON generation_executions
 FOR EACH ROW EXECUTE FUNCTION protect_generation_execution();

ALTER TABLE maintenance_health DROP CONSTRAINT maintenance_health_kind_check;
ALTER TABLE maintenance_health ADD CONSTRAINT maintenance_health_kind_check CHECK(kind IN (
 'legal_hold_expiry','legal_hold_cleanup','product_cleanup_reconciliation','account_deletion_reconciliation',
 'original_media_cleanup_reconciliation','product_refund_reconciliation','generation_output_cleanup','generation_execution_recovery'));
