ALTER TABLE product_payment_dispute_operations
  DROP CONSTRAINT IF EXISTS product_payment_dispute_operations_evidence_reference_check;

ALTER TABLE product_payment_dispute_operations
  ADD CONSTRAINT product_payment_dispute_operations_evidence_reference_check
  CHECK (
    evidence_reference IS NULL OR
    (char_length(evidence_reference) BETWEEN 3 AND 500
      AND evidence_reference ~ '^[A-Za-z0-9][A-Za-z0-9._:/#-]*$')
  );

ALTER TABLE product_payment_dispute_evidence_submissions
  DROP CONSTRAINT IF EXISTS product_payment_dispute_evidence_submissions_provider_reference_check;

ALTER TABLE product_payment_dispute_evidence_submissions
  ADD CONSTRAINT product_payment_dispute_evidence_submissions_provider_reference_check
  CHECK (
    char_length(provider_reference) BETWEEN 3 AND 500
    AND provider_reference ~ '^[A-Za-z0-9][A-Za-z0-9._:/#-]*$'
  );
