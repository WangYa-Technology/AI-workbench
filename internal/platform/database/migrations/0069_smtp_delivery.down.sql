-- Refuse rollback while SMTP evidence exists. Never restore retired sessions.
ALTER TABLE identity_email_delivery_attempts DROP CONSTRAINT identity_email_delivery_attempts_adapter_check;
ALTER TABLE identity_email_delivery_attempts ADD CONSTRAINT identity_email_delivery_attempts_adapter_check CHECK (adapter IN ('local_file','disabled'));
ALTER TABLE identity_auth_challenge_delivery_attempts DROP CONSTRAINT identity_auth_challenge_delivery_attempts_adapter_check;
ALTER TABLE identity_auth_challenge_delivery_attempts ADD CONSTRAINT identity_auth_challenge_delivery_attempts_adapter_check CHECK (adapter IN ('local_file','disabled'));
