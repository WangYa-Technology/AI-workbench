-- Stop old payment writers before upgrading. Durable dispatch evidence remains
-- unchanged; only an active, correctly bound job may perform its first send.
CREATE FUNCTION payment_transfer_job_executable(execution_job uuid, expected_kind text, expected_payload jsonb)
RETURNS boolean LANGUAGE plpgsql AS $$
BEGIN
 IF current_setting('app.payment_transfer_execution_protocol',true) IS DISTINCT FROM 'job-v1'
 OR expected_kind NOT IN ('payment.settle_product','payment.fund_seller_payout') THEN
  RETURN false;
 END IF;
 PERFORM 1 FROM jobs j WHERE j.id=execution_job AND j.kind=expected_kind
 AND j.payload=expected_payload AND j.status IN ('queued','running') FOR SHARE OF j;
 RETURN FOUND;
END; $$;

CREATE FUNCTION protect_transfer_execution_job() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME='seller_payout_funding_dispatches' THEN
  IF NEW.started_at IS NOT NULL AND (TG_OP='INSERT' OR OLD.started_at IS NULL) THEN
   IF NOT payment_transfer_job_executable(NEW.job_id,'payment.fund_seller_payout',jsonb_build_object('transferId',NEW.transfer_id)) THEN
    RAISE EXCEPTION 'source first dispatch requires an executable bound job' USING ERRCODE='23514';
   END IF;
  END IF;
 ELSIF TG_TABLE_NAME='product_settlement_dispatches' THEN
  IF NEW.reserved_at IS NOT NULL AND (TG_OP='INSERT' OR OLD.reserved_at IS NULL) THEN
   IF NOT payment_transfer_job_executable(NEW.job_id,'payment.settle_product',jsonb_build_object('settlementId',NEW.settlement_id)) THEN
    RAISE EXCEPTION 'settlement first dispatch requires an executable bound job' USING ERRCODE='23514';
   END IF;
  END IF;
 ELSE
  RAISE EXCEPTION 'unsupported transfer execution table' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END; $$;

CREATE TRIGGER seller_funding_execution_job_guard BEFORE INSERT OR UPDATE ON seller_payout_funding_dispatches
 FOR EACH ROW EXECUTE FUNCTION protect_transfer_execution_job();
CREATE TRIGGER product_settlement_execution_job_guard BEFORE INSERT OR UPDATE ON product_settlement_dispatches
 FOR EACH ROW EXECUTE FUNCTION protect_transfer_execution_job();
