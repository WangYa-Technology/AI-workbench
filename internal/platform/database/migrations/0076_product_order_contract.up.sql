-- The same canonical offer is used by the public detail and checkout acceptance.
-- Storage evidence stays server-side; only its opaque version is exposed.
CREATE VIEW product_offers AS
SELECT product_id,source_asset_id,root_asset_id,contract,
       encode(public.digest(contract::text,'sha256'),'hex') AS offer_version
FROM (
  SELECT p.id AS product_id,a.id AS source_asset_id,root.id AS root_asset_id,
    jsonb_build_object(
      'product',jsonb_build_object('id',p.id,'sellerId',p.seller_id,'title',p.title,
        'description',p.description,'productType',p.product_type,'priceCents',p.price_cents,
        'currency',p.currency,'aiDisclosure',p.ai_disclosure,'includedFiles',p.included_files,'compatibility',p.compatibility),
      'license',jsonb_build_object('code',l.code,'name',l.name,'summary',l.summary,'version',l.version,'terms',l.terms,
        'allowsCommercial',l.allows_commercial,'allowsDerivatives',l.allows_derivatives,
        'allowsRedistribution',l.allows_redistribution,'attributionRequired',l.attribution_required,
        'refundWindowDays',l.refund_window_days),
      'asset',jsonb_build_object('id',a.id,'rootId',root.id,'familyId',a.family_id,'version',a.version_number,
        'title',a.title,'kind',a.kind,'mimeType',a.mime_type,'width',a.width,'height',a.height,
        'storageBackend',root.storage_backend,'storageKey',root.storage_key,'sourceType',root.source_type)
    ) AS contract
  FROM products p JOIN licenses l ON l.code=p.license_code
  JOIN assets a ON a.id=p.asset_id JOIN assets root ON root.id=COALESCE(a.origin_asset_id,a.id)
) offers;

CREATE TABLE product_order_contracts (
  order_id uuid PRIMARY KEY REFERENCES orders(id),
  source_asset_id uuid NOT NULL REFERENCES assets(id),
  root_asset_id uuid NOT NULL REFERENCES assets(id),
  offer_version text NOT NULL CHECK (offer_version ~ '^[0-9a-f]{64}$'),
  contract jsonb NOT NULL,
  accepted_at timestamptz NOT NULL DEFAULT now(),
  CHECK (offer_version=encode(public.digest(contract::text,'sha256'),'hex')),
  CHECK ((contract->'asset'->>'id')::uuid=source_asset_id),
  CHECK ((contract->'asset'->>'rootId')::uuid=root_asset_id)
);
CREATE INDEX product_order_contracts_root ON product_order_contracts(root_asset_id);

CREATE FUNCTION reject_product_contract_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'product order contract evidence is immutable';
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER product_order_contracts_immutable BEFORE UPDATE OR DELETE ON product_order_contracts
  FOR EACH ROW EXECUTE FUNCTION reject_product_contract_mutation();

-- Do not manufacture historical acceptance evidence from today's mutable offer.
-- Existing external orders without a contract require reconciliation.
