CREATE TABLE media_cleanup_recoveries (
 original_job_id uuid PRIMARY KEY REFERENCES jobs(id),
 retry_job_id uuid NOT NULL UNIQUE REFERENCES jobs(id),
 requested_by uuid NOT NULL REFERENCES users(id),
 expected_attempts integer NOT NULL CHECK(expected_attempts>=0),
 reason text NOT NULL CHECK(char_length(reason) BETWEEN 10 AND 2000),
 created_at timestamptz NOT NULL DEFAULT now(),
 CHECK(original_job_id<>retry_job_id)
);
CREATE FUNCTION reject_media_cleanup_recovery_mutation() RETURNS trigger AS $$
BEGIN
 RAISE EXCEPTION 'media cleanup recovery evidence is immutable';
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER media_cleanup_recovery_immutable BEFORE UPDATE OR DELETE ON media_cleanup_recoveries
 FOR EACH ROW EXECUTE FUNCTION reject_media_cleanup_recovery_mutation();
CREATE INDEX media_cleanup_jobs_subject ON jobs ((payload->>'userId'),status)
 WHERE kind='data_rights.media_cleanup';
