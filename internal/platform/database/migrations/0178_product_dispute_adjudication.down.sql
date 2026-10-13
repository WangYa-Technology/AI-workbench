DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM product_payment_dispute_adjudications) OR EXISTS (SELECT 1 FROM jobs WHERE payload->>'disputeAdjudicationBackfill'='true') THEN
    RAISE EXCEPTION 'cannot remove product payment dispute adjudication evidence' USING ERRCODE='55000';
  END IF;
END $$;

DROP TRIGGER IF EXISTS product_payment_dispute_adjudications_immutable ON product_payment_dispute_adjudications;
DROP FUNCTION IF EXISTS reject_product_payment_dispute_adjudication_mutation();
DROP TABLE product_payment_dispute_adjudications;
DROP FUNCTION validate_product_dispute_adjudication();
