-- Historical sales belong to the accepted seller, never the current product
-- owner. A legacy order needs an explicit payment payee; do not guess ownership.
CREATE VIEW product_sale_owners AS
SELECT order_id,contract->'product'->>'sellerId' AS seller_id
FROM product_order_contracts
UNION ALL
SELECT p.order_id,p.payee_id::text AS seller_id
FROM payment_intents p
WHERE p.purpose='product' AND p.payee_id IS NOT NULL
  AND NOT EXISTS(SELECT 1 FROM product_order_contracts c WHERE c.order_id=p.order_id);

CREATE INDEX product_contracts_seller_idx
  ON product_order_contracts ((contract->'product'->>'sellerId'),order_id);
CREATE INDEX product_payment_payee_idx
  ON payment_intents (payee_id,order_id) WHERE purpose='product';
