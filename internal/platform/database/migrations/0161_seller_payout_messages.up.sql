-- Internal review notes are never repurposed as seller-facing messages.
-- Existing reviews keep an empty message; new decisions require an explicit one.
ALTER TABLE seller_payout_reviews ADD COLUMN seller_message text NOT NULL DEFAULT ''
 CHECK (seller_message='' OR char_length(btrim(seller_message)) BETWEEN 10 AND 1000);

CREATE FUNCTION require_seller_payout_message() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF char_length(btrim(NEW.seller_message)) NOT BETWEEN 10 AND 1000 THEN
  RAISE EXCEPTION 'new payout reviews require an explicit seller message' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER seller_payout_message_required BEFORE INSERT ON seller_payout_reviews
 FOR EACH ROW EXECUTE FUNCTION require_seller_payout_message();
-- The existing review guard also forbids changing or deleting this evidence.
