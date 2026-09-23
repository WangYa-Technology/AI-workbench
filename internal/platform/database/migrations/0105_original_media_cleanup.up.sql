-- Managed original locations only; never enumerate or delete arbitrary bucket objects.
CREATE VIEW original_media_cleanup_locations AS
 SELECT owner_id,storage_backend,storage_key FROM assets WHERE source_type IN ('generation','upload')
 UNION
 SELECT root.owner_id,c.contract->'asset'->>'storageBackend',c.contract->'asset'->>'storageKey'
 FROM product_order_contracts c JOIN assets root ON root.id=c.root_asset_id
 WHERE root.source_type IN ('generation','upload');

CREATE TABLE original_media_cleanup_receipts (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 owner_id uuid NOT NULL REFERENCES users(id),
 initiator_user_id uuid NOT NULL REFERENCES users(id),
 storage_backend text NOT NULL,
 storage_key_sha256 text NOT NULL CHECK(storage_key_sha256 ~ '^[0-9a-f]{64}$'),
 outcome text NOT NULL CHECK(outcome IN ('removed','already_absent')),
 size_bytes bigint CHECK(size_bytes>=0),
 job_id uuid REFERENCES jobs(id),
 request_id uuid REFERENCES data_rights_requests(id),
 verified_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK((job_id IS NULL)<>(request_id IS NULL)),
 CHECK((outcome='removed' AND size_bytes IS NOT NULL) OR (outcome='already_absent' AND size_bytes IS NULL)),
 UNIQUE(owner_id,storage_backend,storage_key_sha256)
);
CREATE INDEX original_media_cleanup_receipt_initiator ON original_media_cleanup_receipts(initiator_user_id,verified_at,id);

CREATE FUNCTION guard_original_media_cleanup_receipt() RETURNS trigger AS $$
BEGIN
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'original media cleanup receipt is immutable'; END IF;
 IF NOT EXISTS(SELECT 1 FROM users WHERE id=NEW.owner_id AND status='deleted') OR
 NOT EXISTS(SELECT 1 FROM users WHERE id=NEW.initiator_user_id AND status='deleted') OR
 NOT EXISTS(SELECT 1 FROM original_media_cleanup_locations l WHERE l.owner_id=NEW.owner_id
  AND l.storage_backend=NEW.storage_backend AND encode(public.digest(l.storage_key,'sha256'),'hex')=NEW.storage_key_sha256) OR
 (NEW.job_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM jobs WHERE id=NEW.job_id AND kind='data_rights.media_cleanup' AND payload->>'userId'=NEW.initiator_user_id::text)) OR
 (NEW.request_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM data_rights_requests WHERE id=NEW.request_id AND user_id=NEW.initiator_user_id
  AND request_type='account_deletion' AND status IN ('processing','completed'))) THEN
  RAISE EXCEPTION 'original media cleanup receipt must bind a known location and cleanup execution';
 END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER original_media_cleanup_receipt_guard BEFORE INSERT OR UPDATE OR DELETE ON original_media_cleanup_receipts
 FOR EACH ROW EXECUTE FUNCTION guard_original_media_cleanup_receipt();

CREATE VIEW original_media_cleanup_policy AS
 SELECT r.id AS request_id,r.user_id,r.created_at,
 CASE WHEN u.status<>'deleted' THEN 'account_active'
 WHEN r.status<>'completed' OR r.completed_at IS NULL OR r.cancel_until IS NULL OR
 NOT EXISTS(SELECT 1 FROM data_rights_deletion_receipts receipt WHERE receipt.request_id=r.id
  AND receipt.subject_ref=r.subject_ref AND receipt.completed_at=r.completed_at) OR
 NOT EXISTS(SELECT 1 FROM data_rights_events e WHERE e.request_id=r.id AND e.actor_id=r.user_id AND e.event_type='requested' AND e.to_status IN ('scheduled','blocked')) OR
 NOT EXISTS(SELECT 1 FROM data_rights_events e WHERE e.request_id=r.id AND e.event_type='deletion_prepared' AND e.to_status='processing') OR
 NOT EXISTS(SELECT 1 FROM data_rights_events e WHERE e.request_id=r.id AND e.event_type='deletion_completed' AND e.to_status='completed') THEN 'inconsistent_stage'
 WHEN r.execute_after>now() OR r.cancel_until>now() OR r.completed_at<r.execute_after OR r.completed_at<r.cancel_until THEN 'inconsistent_stage'
 WHEN EXISTS(SELECT 1 FROM data_rights_legal_holds h WHERE h.user_id=r.user_id AND h.status='active' AND h.expires_at>now()) THEN 'legal_hold'
 ELSE '' END AS unavailable_reason
 FROM data_rights_requests r JOIN users u ON u.id=r.user_id WHERE r.request_type='account_deletion';

CREATE VIEW original_media_cleanup_candidates AS
 SELECT p.request_id,p.user_id,p.created_at,last_job.id AS previous_job_id
 FROM original_media_cleanup_policy p
 LEFT JOIN LATERAL (SELECT id,status FROM jobs WHERE kind='data_rights.media_cleanup' AND payload->>'userId'=p.user_id::text
  ORDER BY created_at DESC,id DESC LIMIT 1) last_job ON true
 WHERE p.unavailable_reason='' AND (last_job.id IS NULL OR last_job.status='succeeded')
 AND NOT EXISTS(SELECT 1 FROM jobs j WHERE j.kind='data_rights.media_cleanup' AND j.payload->>'userId'=p.user_id::text AND j.status IN ('queued','running'))
 AND EXISTS(SELECT 1 FROM original_media_cleanup_locations l WHERE l.owner_id=p.user_id
  AND NOT EXISTS(SELECT 1 FROM original_media_cleanup_receipts receipt WHERE receipt.owner_id=l.owner_id AND receipt.storage_backend=l.storage_backend
   AND receipt.storage_key_sha256=encode(public.digest(l.storage_key,'sha256'),'hex')));

CREATE TABLE original_media_cleanup_reconciliations (
 job_id uuid PRIMARY KEY REFERENCES jobs(id),
 request_id uuid NOT NULL REFERENCES data_rights_requests(id),
 user_id uuid NOT NULL REFERENCES users(id),
 previous_job_id uuid REFERENCES jobs(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 CHECK(previous_job_id IS NULL OR previous_job_id<>job_id)
);
CREATE UNIQUE INDEX original_media_cleanup_reconciliation_predecessor ON original_media_cleanup_reconciliations
 (user_id,COALESCE(previous_job_id,'00000000-0000-0000-0000-000000000000'::uuid));
CREATE INDEX original_media_cleanup_reconciliation_user ON original_media_cleanup_reconciliations(user_id,created_at,job_id);
CREATE INDEX original_media_cleanup_last_job ON jobs ((payload->>'userId'),created_at DESC,id DESC) WHERE kind='data_rights.media_cleanup';
CREATE INDEX original_media_cleanup_completed_scan ON data_rights_requests(created_at,id) WHERE request_type='account_deletion' AND status='completed';
CREATE FUNCTION guard_original_media_cleanup_reconciliation() RETURNS trigger AS $$
BEGIN
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'original media cleanup reconciliation evidence is immutable'; END IF;
 IF NOT EXISTS(SELECT 1 FROM original_media_cleanup_policy WHERE request_id=NEW.request_id AND user_id=NEW.user_id AND unavailable_reason='') OR
 NOT EXISTS(SELECT 1 FROM jobs WHERE id=NEW.job_id AND kind='data_rights.media_cleanup' AND payload->>'userId'=NEW.user_id::text AND status='queued') OR
 (NEW.previous_job_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM jobs WHERE id=NEW.previous_job_id AND kind='data_rights.media_cleanup'
  AND payload->>'userId'=NEW.user_id::text AND status='succeeded')) THEN
  RAISE EXCEPTION 'original media cleanup reconciliation must bind completed deletion and matching jobs';
 END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER original_media_cleanup_reconciliation_guard BEFORE INSERT OR UPDATE OR DELETE ON original_media_cleanup_reconciliations
 FOR EACH ROW EXECUTE FUNCTION guard_original_media_cleanup_reconciliation();
