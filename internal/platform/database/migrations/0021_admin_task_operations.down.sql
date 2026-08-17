DELETE FROM role_permissions WHERE permission_id='admin:tasks';
DELETE FROM permissions WHERE id='admin:tasks';

DROP TRIGGER IF EXISTS task_events_immutable ON task_events;
DROP FUNCTION IF EXISTS reject_task_event_mutation();

ALTER TABLE task_disputes DROP COLUMN IF EXISTS version;
