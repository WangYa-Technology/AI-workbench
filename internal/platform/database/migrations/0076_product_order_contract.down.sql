DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM product_order_contracts) THEN
    RAISE EXCEPTION 'cannot roll back accepted product contracts; preserve transaction evidence';
  END IF;
END;
$$;

DROP TABLE product_order_contracts;
DROP FUNCTION reject_product_contract_mutation();
DROP VIEW product_offers;
