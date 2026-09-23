-- Preserve the provider-verified display summary with the original bank binding.
-- Drain old bank-binding writers before upgrading. Do not backfill historical
-- evidence from today's mutable bank directory. Empty name means unnamed bank;
-- NULL pair means no historical summary was recorded.
LOCK TABLE seller_payout_bank_targets IN ACCESS EXCLUSIVE MODE;
ALTER TABLE seller_payout_bank_targets
  ADD COLUMN bank_name text,
  ADD COLUMN last4 text,
  ADD CONSTRAINT seller_payout_bank_summary_valid CHECK (
    (bank_name IS NULL AND last4 IS NULL) OR
    (bank_name IS NOT NULL AND last4 IS NOT NULL
     AND char_length(bank_name)<=120
     AND last4 ~ '^[0-9]{4}$'
     -- Unicode control and format characters, including bidi overrides.
     AND bank_name !~ U&'[[:cntrl:]\0080-\009F\00AD\0600\0601-\0605\061C\06DD\070F\0890\0891\08E2\180E\200B\200C-\200F\202A-\202E\2060-\2064\2066-\206F\FEFF\FFF9\FFFA-\FFFB\+0110BD\+0110CD\+013430-\+01343F\+01BCA0-\+01BCA3\+01D173-\+01D17A\+0E0001\+0E0020\+0E0021-\+0E007F]')
  );

CREATE FUNCTION require_seller_payout_bank_summary() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.bank_name IS NULL OR NEW.last4 IS NULL THEN
    RAISE EXCEPTION 'new bank bindings require a verified display summary' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER seller_payout_bank_summary_guard
  BEFORE INSERT ON seller_payout_bank_targets
  FOR EACH ROW EXECUTE FUNCTION require_seller_payout_bank_summary();
-- Existing bank-target UPDATE/DELETE protection also protects these columns.
