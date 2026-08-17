ALTER TABLE assets
  ADD COLUMN uploaded_filename text,
  ADD COLUMN size_bytes bigint CHECK (size_bytes IS NULL OR size_bytes > 0),
  ADD COLUMN scan_reason text,
  ADD COLUMN scanned_at timestamptz;

CREATE INDEX assets_scan_queue_idx ON assets(scan_status,created_at,id) WHERE source_type='upload';

INSERT INTO permissions(id,module,description,risk_level,resource_authorization) VALUES
  ('assets:upload','assets','Upload media into the personal Asset workspace','medium',true),
  ('admin:media','admin','Review uploaded media scan decisions','high',true)
ON CONFLICT (id) DO NOTHING;

INSERT INTO role_permissions(role,permission_id)
SELECT role,'assets:upload'
FROM (VALUES ('member'),('creator'),('publisher'),('moderator'),('admin')) roles(role)
ON CONFLICT DO NOTHING;

INSERT INTO role_permissions(role,permission_id)
VALUES ('moderator','admin:media'),('admin','admin:media')
ON CONFLICT DO NOTHING;
