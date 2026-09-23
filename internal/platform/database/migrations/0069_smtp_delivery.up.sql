ALTER TABLE identity_email_delivery_attempts DROP CONSTRAINT identity_email_delivery_attempts_adapter_check;
ALTER TABLE identity_email_delivery_attempts ADD CONSTRAINT identity_email_delivery_attempts_adapter_check CHECK (adapter IN ('local_file','disabled','smtp'));
ALTER TABLE identity_auth_challenge_delivery_attempts DROP CONSTRAINT identity_auth_challenge_delivery_attempts_adapter_check;
ALTER TABLE identity_auth_challenge_delivery_attempts ADD CONSTRAINT identity_auth_challenge_delivery_attempts_adapter_check CHECK (adapter IN ('local_file','disabled','smtp'));

-- Retire shared identities and all their sessions without deleting audit history.
UPDATE sessions SET revoked_at = now()
WHERE revoked_at IS NULL AND user_id IN (SELECT id FROM users WHERE email LIKE '%@demo.hcai.local');
UPDATE users SET status = 'suspended', password_hash = NULL, updated_at = now()
WHERE email LIKE '%@demo.hcai.local';

UPDATE works SET status = 'removed' WHERE author_id IN (SELECT id FROM users WHERE email LIKE '%@demo.hcai.local');
UPDATE posts SET status = 'removed' WHERE author_id IN (SELECT id FROM users WHERE email LIKE '%@demo.hcai.local');
UPDATE comments SET status = 'removed' WHERE author_id IN (SELECT id FROM users WHERE email LIKE '%@demo.hcai.local');
UPDATE products SET status = 'removed' WHERE seller_id IN (SELECT id FROM users WHERE email LIKE '%@demo.hcai.local');
UPDATE demands SET status = 'cancelled' WHERE client_id IN (SELECT id FROM users WHERE email LIKE '%@demo.hcai.local') AND status IN ('draft','open');
