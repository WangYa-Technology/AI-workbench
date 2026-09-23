-- Existing reads retain unknown deadlines. Do not invent a bound that old
-- workers did not enforce; drain them before deploying the new reader.
ALTER TABLE seller_payout_funding_reads ADD COLUMN read_deadline timestamptz;
ALTER TABLE seller_payout_funding_reads ALTER COLUMN read_deadline
 SET DEFAULT (clock_timestamp()+interval '20 seconds');
CREATE INDEX seller_payout_funding_reads_unrecorded_idx
 ON seller_payout_funding_reads(read_deadline,transfer_id) WHERE finished_at IS NULL;

CREATE OR REPLACE FUNCTION protect_seller_payout_funding_read() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' THEN
    RAISE EXCEPTION 'seller funding read evidence is immutable' USING ERRCODE='55000';
  END IF;
  IF TG_OP='INSERT' THEN
    IF current_setting('app.seller_funding_read_protocol',true) IS DISTINCT FROM 'deadline-v1' THEN
      RAISE EXCEPTION 'seller funding reader must enforce durable deadlines' USING ERRCODE='55000';
    END IF;
    IF NEW.finished_at IS NOT NULL OR NEW.read_deadline IS NULL
       OR NOT isfinite(NEW.started_at) OR NOT isfinite(NEW.read_deadline)
       OR NEW.read_deadline<=NEW.started_at
       OR NEW.read_deadline>NEW.started_at+interval '21 seconds' THEN
      RAISE EXCEPTION 'seller funding read requires a finite dispatch deadline' USING ERRCODE='23514';
    END IF;
  ELSIF ROW(NEW.id,NEW.transfer_id,NEW.started_at,NEW.read_deadline)
     IS DISTINCT FROM ROW(OLD.id,OLD.transfer_id,OLD.started_at,OLD.read_deadline)
     OR OLD.finished_at IS NOT NULL THEN
    RAISE EXCEPTION 'seller funding read evidence is immutable' USING ERRCODE='55000';
  END IF;
  RETURN NEW;
END;
$$;
