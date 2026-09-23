DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM product_payment_dispute_operations)
     OR EXISTS (SELECT 1 FROM product_payment_dispute_evidence_submissions) THEN
    RAISE EXCEPTION 'cannot remove product payment dispute operation evidence' USING ERRCODE='55000';
  END IF;
END $$;

DROP INDEX IF EXISTS product_payment_disputes_directory_idx;
DROP TRIGGER IF EXISTS product_payment_dispute_evidence_submissions_immutable ON product_payment_dispute_evidence_submissions;
DROP TRIGGER IF EXISTS product_payment_dispute_operations_immutable ON product_payment_dispute_operations;
DROP FUNCTION IF EXISTS reject_product_payment_dispute_operation_mutation();
DROP TABLE product_payment_dispute_evidence_submissions;
DROP TABLE product_payment_dispute_operations;
ALTER TABLE product_payment_disputes
  DROP COLUMN evidence_status,
  DROP COLUMN review_route,
  DROP COLUMN review_status;
