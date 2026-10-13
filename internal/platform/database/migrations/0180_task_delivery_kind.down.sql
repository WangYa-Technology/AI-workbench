-- Refuse a downgrade that would silently reinterpret an existing task. The
-- snapshot is the source of truth after category edits, so it must agree with
-- the current mapping before the column can be removed.
DO $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM demands d
    JOIN task_types t ON t.code=d.deliverable_type
    WHERE d.delivery_kind IS DISTINCT FROM t.delivery_kind
  ) THEN
    RAISE EXCEPTION
      'cannot downgrade 0180: task delivery contracts differ from current category mappings';
  END IF;
END $$;

DROP TRIGGER demands_delivery_kind_snapshot ON demands;
DROP FUNCTION freeze_task_delivery_kind();
ALTER TABLE demands DROP COLUMN delivery_kind;
DROP TRIGGER task_types_delivery_kind_default ON task_types;
DROP FUNCTION default_task_delivery_kind();
ALTER TABLE task_types DROP COLUMN delivery_kind;
