CREATE FUNCTION valid_wallet_topup_amounts(minimum_cents integer, amounts integer[]) RETURNS boolean
LANGUAGE sql IMMUTABLE STRICT SET search_path = pg_catalog AS $$
 SELECT minimum_cents BETWEEN 50 AND 99999999
    AND cardinality(amounts) <= 12
    AND (array_ndims(amounts) IS NULL OR array_ndims(amounts) = 1)
    AND NOT EXISTS (SELECT 1 FROM unnest(amounts) a WHERE a IS NULL OR a < minimum_cents OR a > 99999999)
    AND cardinality(amounts) = (SELECT count(DISTINCT a) FROM unnest(amounts) a)
$$;

CREATE TABLE wallet_topup_settings (
 singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
 id uuid NOT NULL UNIQUE DEFAULT gen_random_uuid(),
 version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
 minimum_amount_cents integer NOT NULL DEFAULT 50,
 preset_amounts_cents integer[] NOT NULL DEFAULT ARRAY[1000,2000,5000,10000,20000],
 updated_by uuid REFERENCES users(id),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT wallet_topup_settings_amounts CHECK (valid_wallet_topup_amounts(minimum_amount_cents,preset_amounts_cents))
);
INSERT INTO wallet_topup_settings(singleton) VALUES(true);

-- Older binaries cannot bypass a newly configured minimum. Existing accepted
-- intents remain replayable and fulfill using their original USD amount.
CREATE FUNCTION check_wallet_topup_minimum() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE minimum_cents integer;
BEGIN
 IF NEW.purpose = 'wallet_topup' THEN
  SELECT minimum_amount_cents INTO STRICT minimum_cents FROM wallet_topup_settings WHERE singleton=true FOR SHARE;
  IF NEW.currency <> 'USD' OR NEW.amount_cents < minimum_cents OR NEW.amount_cents > 99999999 THEN
   RAISE EXCEPTION 'wallet top-up amount is outside current settings' USING ERRCODE='23514', CONSTRAINT='wallet_topup_minimum';
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER wallet_topup_minimum_guard BEFORE INSERT ON payment_intents
FOR EACH ROW EXECUTE FUNCTION check_wallet_topup_minimum();
