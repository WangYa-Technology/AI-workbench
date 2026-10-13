-- Keep the common task-delivery asset scan selective before the anti-joins
-- that exclude product roots and other disallowed origins.
CREATE INDEX assets_task_delivery_owner_created_idx
  ON assets(owner_id, created_at DESC, id DESC)
  WHERE scan_status='clean'
    AND source_type<>'purchase'
    AND license_code<>'task-contract';
