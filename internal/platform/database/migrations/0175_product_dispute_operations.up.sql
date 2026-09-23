ALTER TABLE product_payment_disputes
  ADD COLUMN review_status text NOT NULL DEFAULT 'new'
    CHECK (review_status IN ('new','acknowledged','evidence_requested','evidence_submitted','escalated')),
  ADD COLUMN review_route text NOT NULL DEFAULT 'finance'
    CHECK (review_route IN ('finance','seller_support','provider_review','collections')),
  ADD COLUMN evidence_status text NOT NULL DEFAULT 'not_requested'
    CHECK (evidence_status IN ('not_requested','requested','submitted'));

CREATE TABLE product_payment_dispute_operations (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  dispute_id uuid NOT NULL REFERENCES product_payment_disputes(id) ON DELETE RESTRICT,
  actor_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  idempotency_key text NOT NULL CHECK (
    char_length(idempotency_key) BETWEEN 8 AND 128
    AND idempotency_key ~ '^[A-Za-z0-9._:-]+$'
  ),
  action text NOT NULL CHECK (action IN ('route','request_evidence','record_evidence_submission','escalate_recovery')),
  route text NOT NULL CHECK (route IN ('finance','seller_support','provider_review','collections')),
  -- PostgreSQL text values cannot contain NUL bytes; checking chr(0) would
  -- itself raise "null character not permitted" for every valid insert.
  reason text NOT NULL CHECK (char_length(reason) BETWEEN 10 AND 2000),
  evidence_reference text CHECK (
    evidence_reference IS NULL OR
    (char_length(evidence_reference) BETWEEN 3 AND 500
      AND evidence_reference ~ '^[A-Za-z0-9][A-Za-z0-9._:/#-]*$')
  ),
  expected_version bigint NOT NULL CHECK (expected_version > 0),
  resulting_version bigint NOT NULL CHECK (resulting_version = expected_version + 1),
  from_review_status text NOT NULL CHECK (from_review_status IN ('new','acknowledged','evidence_requested','evidence_submitted','escalated')),
  to_review_status text NOT NULL CHECK (to_review_status IN ('acknowledged','evidence_requested','evidence_submitted','escalated')),
  from_evidence_status text NOT NULL CHECK (from_evidence_status IN ('not_requested','requested','submitted')),
  to_evidence_status text NOT NULL CHECK (to_evidence_status IN ('not_requested','requested','submitted')),
  request_id text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  UNIQUE(actor_id,idempotency_key),
  UNIQUE(dispute_id,resulting_version),
  CHECK ((action='record_evidence_submission') = (evidence_reference IS NOT NULL)),
  CHECK (action<>'escalate_recovery' OR route='collections')
);
CREATE INDEX product_payment_dispute_operations_history_idx
  ON product_payment_dispute_operations(dispute_id,created_at DESC,id DESC);

CREATE TABLE product_payment_dispute_evidence_submissions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  dispute_id uuid NOT NULL REFERENCES product_payment_disputes(id) ON DELETE RESTRICT,
  operation_id uuid NOT NULL UNIQUE REFERENCES product_payment_dispute_operations(id) ON DELETE RESTRICT,
  submitted_by uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  provider_reference text NOT NULL CHECK (
    char_length(provider_reference) BETWEEN 3 AND 500
    AND provider_reference ~ '^[A-Za-z0-9][A-Za-z0-9._:/#-]*$'
  ),
  submitted_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX product_payment_dispute_evidence_history_idx
  ON product_payment_dispute_evidence_submissions(dispute_id,submitted_at DESC,id DESC);

CREATE FUNCTION reject_product_payment_dispute_operation_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'product payment dispute operation evidence is immutable' USING ERRCODE='55000';
END;
$$;
CREATE TRIGGER product_payment_dispute_operations_immutable
BEFORE UPDATE OR DELETE ON product_payment_dispute_operations
FOR EACH ROW EXECUTE FUNCTION reject_product_payment_dispute_operation_mutation();
CREATE TRIGGER product_payment_dispute_evidence_submissions_immutable
BEFORE UPDATE OR DELETE ON product_payment_dispute_evidence_submissions
FOR EACH ROW EXECUTE FUNCTION reject_product_payment_dispute_operation_mutation();

CREATE INDEX product_payment_disputes_directory_idx
  ON product_payment_disputes(
    (CASE WHEN action_status='won' THEN 1 ELSE 0 END),
    due_by,
    id
  );
