DELETE FROM role_permissions WHERE permission_id='admin:settings';
DELETE FROM permissions WHERE id='admin:settings';
DROP TRIGGER IF EXISTS system_setting_revisions_immutable ON system_setting_revisions;
DROP FUNCTION IF EXISTS reject_system_setting_revision_mutation();
DROP TABLE IF EXISTS system_setting_state;
DROP TABLE IF EXISTS system_setting_revisions;
