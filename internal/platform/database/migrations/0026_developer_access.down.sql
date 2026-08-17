DELETE FROM role_permissions WHERE permission_id IN ('developer:credentials','admin:developer');
DELETE FROM permissions WHERE id IN ('developer:credentials','admin:developer');
DROP TABLE IF EXISTS developer_api_keys;
DROP TABLE IF EXISTS developer_service_accounts;
DROP TABLE IF EXISTS developer_access_control;
