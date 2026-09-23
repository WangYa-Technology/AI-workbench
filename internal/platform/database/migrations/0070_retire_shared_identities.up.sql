-- Keep foreign keys and immutable transaction evidence, but erase the shared identities.
CREATE TEMP TABLE retired_identity_ids ON COMMIT DROP AS
SELECT id FROM users WHERE lower(email) LIKE '%@demo.hcai.local';

DELETE FROM sessions WHERE user_id IN (SELECT id FROM retired_identity_ids);
DELETE FROM oauth_accounts WHERE user_id IN (SELECT id FROM retired_identity_ids);

UPDATE developer_api_keys SET status='revoked',revoked_at=COALESCE(revoked_at,now()),
  last_ip_hash=NULL,version=version+1
WHERE service_account_id IN (
  SELECT id FROM developer_service_accounts WHERE owner_id IN (SELECT id FROM retired_identity_ids)
) AND status='active';
UPDATE developer_service_accounts SET status='revoked',revoked_at=COALESCE(revoked_at,now()),
  updated_at=now(),version=version+1
WHERE owner_id IN (SELECT id FROM retired_identity_ids) AND status='active';
UPDATE developer_webhook_endpoints SET status='revoked',revoked_at=COALESCE(revoked_at,now()),
  updated_at=now(),version=version+1
WHERE owner_id IN (SELECT id FROM retired_identity_ids) AND status='active';

UPDATE jobs SET status='cancelled',updated_at=now()
WHERE status IN ('queued','running') AND (
  (kind IN ('identity.email_action.deliver','identity.email_action.expire') AND
    payload->>'actionId' IN (SELECT id::text FROM identity_email_actions WHERE user_id IN (SELECT id FROM retired_identity_ids))) OR
  (kind IN ('identity.auth_challenge.deliver','identity.auth_challenge.expire') AND
    payload->>'challengeId' IN (SELECT id::text FROM identity_auth_challenges WHERE lower(email_snapshot) LIKE '%@demo.hcai.local')) OR
  (kind='notification.deliver' AND
    payload->>'notificationId' IN (SELECT id::text FROM notifications WHERE user_id IN (SELECT id FROM retired_identity_ids)))
);

UPDATE identity_email_actions SET
  status=CASE WHEN status IN ('queued','delivered') THEN 'cancelled' ELSE status END,
  cancelled_at=CASE WHEN status IN ('queued','delivered') THEN now() ELSE cancelled_at END,
  email_snapshot='deleted+'||user_id::text||'@hcai.invalid',
  token_hash=NULL,token_nonce=NULL,token_ciphertext=NULL,updated_at=now(),version=version+1
WHERE user_id IN (SELECT id FROM retired_identity_ids);
UPDATE identity_auth_challenges SET
  status=CASE WHEN status IN ('queued','delivered') THEN 'cancelled' ELSE status END,
  cancelled_at=CASE WHEN status IN ('queued','delivered') THEN now() ELSE cancelled_at END,
  email_snapshot='deleted+'||id::text||'@hcai.invalid',
  code_hash=NULL,code_nonce=NULL,code_ciphertext=NULL,updated_at=now()
WHERE lower(email_snapshot) LIKE '%@demo.hcai.local';

DELETE FROM notification_preferences WHERE user_id IN (SELECT id FROM retired_identity_ids);
DELETE FROM notifications WHERE user_id IN (SELECT id FROM retired_identity_ids);
DELETE FROM post_reactions WHERE user_id IN (SELECT id FROM retired_identity_ids);
DELETE FROM user_follows WHERE follower_id IN (SELECT id FROM retired_identity_ids)
  OR following_id IN (SELECT id FROM retired_identity_ids);

UPDATE works SET status='removed',prompt=NULL,prompt_visibility='private',summary='',updated_at=now()
WHERE author_id IN (SELECT id FROM retired_identity_ids);
UPDATE posts SET status='removed',body='[Removed]',updated_at=now()
WHERE author_id IN (SELECT id FROM retired_identity_ids);
UPDATE comments SET status='removed',body='[Removed]'
WHERE author_id IN (SELECT id FROM retired_identity_ids);
UPDATE products SET status='removed',description='[Removed]',updated_at=now()
WHERE seller_id IN (SELECT id FROM retired_identity_ids);
UPDATE assets SET scan_status='rejected',uploaded_filename=NULL,version_note=NULL,
  scan_reason='Source account retired.',scanned_at=now()
WHERE owner_id IN (SELECT id FROM retired_identity_ids);
UPDATE demands SET status='cancelled',updated_at=now()
WHERE client_id IN (SELECT id FROM retired_identity_ids) AND status IN ('draft','open');

UPDATE users SET email='deleted+'||id::text||'@hcai.invalid',
  handle='deleted_'||replace(id::text,'-',''),display_name='Deleted account',
  password_hash=NULL,email_verified_at=NULL,role='member',status='deleted',
  locale='en-US',timezone='UTC',updated_at=now()
WHERE id IN (SELECT id FROM retired_identity_ids);
