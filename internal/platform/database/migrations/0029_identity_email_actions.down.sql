DELETE FROM role_permissions WHERE permission_id='admin:email_delivery';
DELETE FROM permissions WHERE id='admin:email_delivery';
DROP TRIGGER IF EXISTS identity_email_delivery_attempts_immutable ON identity_email_delivery_attempts;
DROP FUNCTION IF EXISTS reject_identity_email_attempt_mutation();
DROP TABLE IF EXISTS identity_email_delivery_attempts;
DROP TABLE IF EXISTS identity_email_actions;
