DELETE FROM role_permissions WHERE permission_id IN ('support:self','admin:support');
DELETE FROM permissions WHERE id IN ('support:self','admin:support');
DROP TRIGGER IF EXISTS support_events_immutable ON support_events;
DROP TRIGGER IF EXISTS support_messages_immutable ON support_messages;
DROP FUNCTION IF EXISTS reject_support_evidence_mutation();
DROP TABLE IF EXISTS support_events;
DROP TABLE IF EXISTS support_messages;
DROP TABLE IF EXISTS support_cases;
