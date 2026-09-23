-- Seller publication is separate from the immutable buyer contract. Existing
-- listings are not silently assigned an approval they never received.
CREATE TABLE product_publications (
 product_id uuid PRIMARY KEY REFERENCES products(id),
 review_status text NOT NULL DEFAULT 'draft' CHECK (review_status IN ('draft','pending','approved','rejected','blocked')),
 submitted_version text CHECK (submitted_version ~ '^[0-9a-f]{64}$'),
 approved_version text CHECK (approved_version ~ '^[0-9a-f]{64}$'),
 review_reason text NOT NULL DEFAULT '',
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX products_seller_directory ON products(seller_id,created_at DESC,id DESC);
CREATE INDEX product_publications_queue ON product_publications(review_status,product_id);

CREATE VIEW product_listing_versions AS
 SELECT p.id AS product_id,
 encode(public.digest(jsonb_build_object('offer',o.offer_version,'category',p.category)::text,'sha256'),'hex') AS content_version,
 encode(public.digest(jsonb_build_object('product',to_jsonb(p),'offer',o.offer_version,'publication',to_jsonb(m))::text,'sha256'),'hex') AS version
 FROM products p JOIN product_offers o ON o.product_id=p.id
 LEFT JOIN product_publications m ON m.product_id=p.id;

CREATE TABLE product_listing_commands (
 actor_id uuid NOT NULL REFERENCES users(id),
 key_sha256 text NOT NULL CHECK (key_sha256 ~ '^[0-9a-f]{64}$'),
 request_sha256 text NOT NULL CHECK (request_sha256 ~ '^[0-9a-f]{64}$'),
 product_id uuid NOT NULL REFERENCES products(id),
 action text NOT NULL CHECK (action IN ('create','edit','submit','pause','approve','reject','block','reopen')),
 snapshot jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(actor_id,key_sha256)
);
CREATE INDEX product_listing_commands_product ON product_listing_commands(product_id,created_at);
CREATE TRIGGER product_listing_commands_immutable BEFORE UPDATE OR DELETE ON product_listing_commands
 FOR EACH ROW EXECUTE FUNCTION reject_product_contract_mutation();

-- An eligible original must be independently owned and stored. References,
-- purchased deliveries, task grants, public works and advertised samples need
-- their own rights/publication resolution before they can become originals.
CREATE INDEX product_source_work_references ON works(asset_id) WHERE status<>'removed';
CREATE INDEX product_source_task_references ON delivery_assets(asset_id);
CREATE INDEX product_source_task_grants ON task_delivery_grants(source_asset_id);
CREATE INDEX product_source_generation_output ON generations(output_asset_id);
CREATE VIEW product_source_candidates AS
 SELECT a.id AS asset_id,a.owner_id FROM assets a
 JOIN users u ON u.id=a.owner_id AND u.status='active'
 WHERE a.source_type IN ('upload','generation') AND a.origin_asset_id IS NULL
 AND a.scan_status='clean' AND a.license_code IS DISTINCT FROM 'task-contract'
 AND a.storage_backend IN ('local_file','s3') AND length(a.storage_key)>0
 AND (a.source_type='upload' OR EXISTS(SELECT 1 FROM generations g WHERE g.id=a.source_id AND g.output_asset_id=a.id AND g.owner_id=a.owner_id AND g.status='succeeded'))
 AND NOT EXISTS(SELECT 1 FROM assets alias WHERE alias.id<>a.id
   AND alias.storage_backend=a.storage_backend AND alias.storage_key=a.storage_key)
 AND NOT EXISTS(SELECT 1 FROM works w WHERE w.asset_id=a.id AND w.status<>'removed')
 AND NOT EXISTS(SELECT 1 FROM delivery_assets da WHERE da.asset_id=a.id)
 AND NOT EXISTS(SELECT 1 FROM task_delivery_grants grant_record WHERE grant_record.source_asset_id=a.id)
 AND NOT EXISTS(SELECT 1 FROM products p WHERE p.preview_asset_id=a.id)
 AND NOT EXISTS(SELECT 1 FROM product_delivery_snapshots d WHERE d.storage_backend=a.storage_backend AND d.storage_key=a.storage_key)
 AND NOT EXISTS(SELECT 1 FROM generations g WHERE (g.id=a.source_id OR g.output_asset_id=a.id)
   AND (g.mode='chat' OR g.source_asset_id IS NOT NULL OR g.mask_asset_id IS NOT NULL OR g.source_work_id IS NOT NULL OR g.source_task_id IS NOT NULL OR g.parent_generation_id IS NOT NULL OR EXISTS(SELECT 1 FROM generation_reference_assets r WHERE r.generation_id=g.id)));

CREATE OR REPLACE VIEW public_products AS
 SELECT p.* FROM products p
 JOIN users seller ON seller.id=p.seller_id AND seller.status='active'
 JOIN licenses l ON l.code=p.license_code AND l.status='active'
 JOIN assets a ON a.id=p.asset_id AND a.scan_status='clean'
 LEFT JOIN assets origin ON origin.id=a.origin_asset_id
 WHERE p.status='active' AND (a.origin_asset_id IS NULL OR origin.scan_status='clean')
 AND (NOT EXISTS(SELECT 1 FROM product_publications m WHERE m.product_id=p.id)
 OR EXISTS(SELECT 1 FROM product_publications m JOIN product_listing_versions v ON v.product_id=m.product_id
   WHERE m.product_id=p.id AND m.review_status='approved' AND m.approved_version=v.content_version
   AND EXISTS(SELECT 1 FROM product_source_candidates candidate WHERE candidate.asset_id=p.asset_id AND candidate.owner_id=p.seller_id)));
