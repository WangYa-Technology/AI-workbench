-- A category is a directory label, not an asset kind. Freeze the fulfillment
-- contract on each task so category edits/replacement cannot change old briefs.
ALTER TABLE task_types ADD COLUMN delivery_kind text;
UPDATE task_types SET delivery_kind = CASE
  WHEN code IN ('image','video','audio','document','prompt','workflow','mixed') THEN code
  WHEN code='text' THEN 'document'
  ELSE icon END;
ALTER TABLE task_types ALTER COLUMN delivery_kind SET NOT NULL;
ALTER TABLE task_types ADD CONSTRAINT task_types_delivery_kind_check
  CHECK (delivery_kind IN ('image','video','audio','document','prompt','workflow','mixed'));

CREATE FUNCTION default_task_delivery_kind() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.delivery_kind IS NULL THEN
    NEW.delivery_kind := CASE
      WHEN NEW.code IN ('image','video','audio','document','prompt','workflow','mixed') THEN NEW.code
      WHEN NEW.code='text' THEN 'document'
      ELSE NEW.icon END;
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER task_types_delivery_kind_default BEFORE INSERT ON task_types
  FOR EACH ROW EXECUTE FUNCTION default_task_delivery_kind();

ALTER TABLE demands ADD COLUMN delivery_kind text;
UPDATE demands d SET delivery_kind=t.delivery_kind FROM task_types t WHERE t.code=d.deliverable_type;
ALTER TABLE demands ALTER COLUMN delivery_kind SET NOT NULL;
ALTER TABLE demands ADD CONSTRAINT demands_delivery_kind_check
  CHECK (delivery_kind IN ('image','video','audio','document','prompt','workflow','mixed'));
CREATE FUNCTION freeze_task_delivery_kind() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='INSERT' THEN
    SELECT delivery_kind INTO NEW.delivery_kind FROM task_types
      WHERE code=NEW.deliverable_type AND scope='task' FOR SHARE;
  ELSIF NEW.delivery_kind IS DISTINCT FROM OLD.delivery_kind THEN
    RAISE EXCEPTION 'Task delivery kind is fixed at publication';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER demands_delivery_kind_snapshot BEFORE INSERT OR UPDATE ON demands
  FOR EACH ROW EXECUTE FUNCTION freeze_task_delivery_kind();
