DELETE FROM role_permissions WHERE permission_id IN ('assets:upload','admin:media');
DELETE FROM permissions WHERE id IN ('assets:upload','admin:media');

DROP INDEX IF EXISTS assets_scan_queue_idx;
ALTER TABLE assets
  DROP COLUMN IF EXISTS scanned_at,
  DROP COLUMN IF EXISTS scan_reason,
  DROP COLUMN IF EXISTS size_bytes,
  DROP COLUMN IF EXISTS uploaded_filename;
