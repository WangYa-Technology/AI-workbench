CREATE TABLE licenses (
  code text PRIMARY KEY,
  name text NOT NULL,
  summary text NOT NULL,
  terms text NOT NULL,
  version text NOT NULL,
  allows_commercial boolean NOT NULL DEFAULT false,
  allows_derivatives boolean NOT NULL DEFAULT false,
  allows_redistribution boolean NOT NULL DEFAULT false,
  attribution_required boolean NOT NULL DEFAULT true,
  refund_window_days integer NOT NULL DEFAULT 0 CHECK (refund_window_days BETWEEN 0 AND 90),
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','retired')),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO licenses(code,name,summary,terms,version,allows_commercial,allows_derivatives,allows_redistribution,attribution_required,refund_window_days)
VALUES
  ('hcai-personal-v1','HCAI Personal License','Use in personal, non-commercial projects.','You may use the licensed asset in personal projects. Commercial use, standalone redistribution, resale, sublicensing, and model training are not permitted.','1.0',false,true,false,true,7),
  ('hcai-commercial-standard-v1','HCAI Commercial Standard','Use in personal and commercial end products with attribution.','You may adapt the asset and include it in personal or commercial end products. You may not resell, sublicense, redistribute, expose, or package the source asset as a competing standalone asset. Model training is not permitted. Attribution to the creator is required.','1.0',true,true,false,true,7);

ALTER TABLE products
  ADD CONSTRAINT products_license_code_fkey
  FOREIGN KEY (license_code) REFERENCES licenses(code);

ALTER TABLE assets
  ADD COLUMN origin_asset_id uuid REFERENCES assets(id);

CREATE INDEX assets_origin_idx ON assets(origin_asset_id) WHERE origin_asset_id IS NOT NULL;

ALTER TABLE generations
  ADD COLUMN source_asset_id uuid REFERENCES assets(id);

CREATE INDEX generations_source_asset_idx ON generations(source_asset_id) WHERE source_asset_id IS NOT NULL;

CREATE TABLE order_events (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  order_id uuid NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  actor_id uuid REFERENCES users(id),
  from_status text,
  to_status text NOT NULL CHECK (to_status IN ('test_pending','test_paid','fulfilled','refund_requested','test_refunded','cancelled')),
  reason text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX order_events_order_idx ON order_events(order_id,created_at,id);

CREATE TABLE entitlements (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES users(id),
  product_id uuid NOT NULL REFERENCES products(id),
  order_id uuid NOT NULL UNIQUE REFERENCES orders(id),
  asset_id uuid NOT NULL UNIQUE REFERENCES assets(id),
  license_code text NOT NULL REFERENCES licenses(code),
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','revoked','refunded')),
  granted_at timestamptz NOT NULL DEFAULT now(),
  revoked_at timestamptz,
  CHECK ((status='active' AND revoked_at IS NULL) OR (status<>'active' AND revoked_at IS NOT NULL))
);

CREATE UNIQUE INDEX entitlements_active_product_buyer_idx
  ON entitlements(user_id,product_id)
  WHERE status='active';

CREATE INDEX entitlements_user_idx ON entitlements(user_id,granted_at DESC);
