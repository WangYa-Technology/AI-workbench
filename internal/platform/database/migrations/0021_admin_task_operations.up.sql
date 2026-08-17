ALTER TABLE task_disputes
  ADD COLUMN version integer NOT NULL DEFAULT 1 CHECK (version > 0);

CREATE FUNCTION reject_task_event_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'task evidence is append-only';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER task_events_immutable BEFORE UPDATE OR DELETE ON task_events
  FOR EACH ROW EXECUTE FUNCTION reject_task_event_mutation();

INSERT INTO permissions(id,module,description,risk_level,resource_authorization) VALUES
  ('admin:tasks','admin','Review task operations and resolve Local Test disputes','high',true)
ON CONFLICT (id) DO NOTHING;

INSERT INTO role_permissions(role,permission_id)
VALUES ('admin','admin:tasks')
ON CONFLICT DO NOTHING;
