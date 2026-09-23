package productpolicy

// PurchaseAssetAccessSQL is a predicate for the outer assets alias "a". Keep
// metadata capabilities, downloads and queued generation references on the same
// policy. It uses the original order and accepted sources, never today's seller
// or listing status: a buyer's rights can outlive a seller's account.
//
// Historical internal entitlements remain readable without inventing a payment
// or contract. An external payment without its contract, or an unbound delivery
// snapshot, requires reconciliation instead of falling back to mutable media.
const PurchaseAssetAccessSQL = `EXISTS(
 SELECT 1 FROM entitlements access_e
 JOIN orders access_o ON access_o.id=access_e.order_id
 JOIN users access_u ON access_u.id=access_e.user_id
 LEFT JOIN product_order_contracts access_c ON access_c.order_id=access_o.id
 LEFT JOIN product_delivery_snapshots access_d ON access_d.order_id=access_o.id
 WHERE a.source_type='purchase' AND a.scan_status='clean'
 AND access_e.asset_id=a.id AND access_e.user_id=a.owner_id AND access_e.status='active'
 AND a.source_id=access_o.id AND access_o.buyer_id=a.owner_id
 AND access_e.product_id=access_o.product_id AND access_e.license_code=a.license_code
 AND access_o.status IN ('fulfilled','refund_requested') AND access_u.status='active'
 AND CASE WHEN access_c.order_id IS NOT NULL THEN
   CASE WHEN access_c.contract ? 'delivery' THEN a.origin_asset_id IS NULL
     ELSE a.origin_asset_id=access_c.root_asset_id END
   AND access_c.contract->'product'->>'id'=access_o.product_id::text
   AND access_c.contract->'license'->>'code'=access_e.license_code
   AND (SELECT count(*)>0 AND COALESCE(bool_and(
     src.id IS NOT NULL AND root.id IS NOT NULL AND src.scan_status='clean' AND root.scan_status='clean'),false)
     FROM product_order_media_sources sources
     LEFT JOIN assets src ON src.id=sources.source_asset_id
     LEFT JOIN assets root ON root.id=sources.root_asset_id
     WHERE sources.order_id=access_o.id)
   AND CASE WHEN access_o.delivery_snapshot_required THEN access_d.state='ready'
     ELSE access_d.order_id IS NULL AND NOT (access_c.contract ? 'delivery')
       AND access_c.contract->'asset'->>'sourceType' IN ('upload','generation')
       AND COALESCE(access_c.contract->'asset'->>'storageBackend','')<>''
       AND COALESCE(access_c.contract->'asset'->>'storageKey','')<>'' END
 ELSE NOT access_o.delivery_snapshot_required AND access_d.order_id IS NULL
   AND NOT EXISTS(SELECT 1 FROM payment_intents access_pi WHERE access_pi.order_id=access_o.id)
   AND EXISTS(SELECT 1 FROM assets origin WHERE origin.id=a.origin_asset_id AND origin.scan_status='clean')
 END)`

// An accepted derivative permission overrides live license changes. The old
// license fallback applies only to internal historical entitlements above.
const PurchaseAssetReuseSQL = `(` + PurchaseAssetAccessSQL + ` AND EXISTS(
 SELECT 1 FROM entitlements reuse_e JOIN licenses reuse_l ON reuse_l.code=reuse_e.license_code
 LEFT JOIN product_order_contracts reuse_c ON reuse_c.order_id=reuse_e.order_id
 WHERE reuse_e.asset_id=a.id AND reuse_e.order_id=a.source_id
 AND NOT COALESCE(reuse_c.contract ? 'delivery',false)
 AND CASE WHEN reuse_c.order_id IS NOT NULL THEN COALESCE((reuse_c.contract->'license'->>'allowsDerivatives')::boolean,false) ELSE reuse_l.allows_derivatives END))`
