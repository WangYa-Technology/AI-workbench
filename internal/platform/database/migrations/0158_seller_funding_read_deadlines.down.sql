LOCK TABLE seller_payout_funding_reads IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
  IF EXISTS(SELECT 1 FROM seller_payout_funding_reads WHERE read_deadline IS NOT NULL) THEN
    RAISE EXCEPTION 'seller funding read deadlines must be retained' USING ERRCODE='55000';
  END IF;
END;
$$;

CREATE OR REPLACE FUNCTION protect_seller_payout_funding_read() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' THEN
    RAISE EXCEPTION 'seller funding read evidence is immutable' USING ERRCODE='55000';
  END IF;
  IF TG_OP='INSERT' THEN
    IF NEW.finished_at IS NOT NULL THEN
      RAISE EXCEPTION 'seller funding read must be registered before completion' USING ERRCODE='23514';
    END IF;
  ELSIF ROW(NEW.id,NEW.transfer_id,NEW.started_at) IS DISTINCT FROM ROW(OLD.id,OLD.transfer_id,OLD.started_at)
     OR OLD.finished_at IS NOT NULL THEN
    RAISE EXCEPTION 'seller funding read evidence is immutable' USING ERRCODE='55000';
  END IF;
  RETURN NEW;
END;
$$;
DROP INDEX seller_payout_funding_reads_unrecorded_idx;
ALTER TABLE seller_payout_funding_reads DROP COLUMN read_deadline;
