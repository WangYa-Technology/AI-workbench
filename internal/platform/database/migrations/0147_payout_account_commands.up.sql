CREATE TABLE payout_account_commands (
 user_id uuid PRIMARY KEY REFERENCES users(id),
 identity jsonb NOT NULL,
 email text NOT NULL,
 command_version text NOT NULL CHECK(command_version='stripe-connect-account-v1'),
 idempotency_key text NOT NULL UNIQUE CHECK(idempotency_key='connect-account-' || user_id::text),
 reserved_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(reserved_at)),
 destination_id text,
 completed_at timestamptz,
 CHECK(jsonb_typeof(identity)='object' AND
   (identity->>'provider'='stripe') IS TRUE AND
   (identity->>'merchantId' ~ '^acct_[A-Za-z0-9_]+$') IS TRUE AND
   jsonb_typeof(identity->'liveMode') IS NOT DISTINCT FROM 'boolean' AND
   COALESCE(length(identity->>'endpoint'),0)>0 AND
   COALESCE(length(identity->>'apiVersion'),0)>0 AND
   COALESCE(length(identity->>'requestVersion'),0)>0),
 CHECK((destination_id IS NULL) = (completed_at IS NULL)),
 CHECK(destination_id IS NULL OR destination_id ~ '^acct_[A-Za-z0-9_]+$'),
 CHECK(completed_at IS NULL OR (isfinite(completed_at) AND completed_at >= reserved_at))
);

-- Preserve the original request and first possible external write across crashes.
CREATE FUNCTION protect_payout_account_command() RETURNS trigger AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'payout account commands are immutable'; END IF;
 IF OLD.completed_at IS NOT NULL OR NEW.completed_at IS NULL OR
 ROW(NEW.user_id,NEW.identity,NEW.email,NEW.command_version,NEW.idempotency_key,NEW.reserved_at)
 IS DISTINCT FROM
 ROW(OLD.user_id,OLD.identity,OLD.email,OLD.command_version,OLD.idempotency_key,OLD.reserved_at)
 THEN RAISE EXCEPTION 'payout account command evidence is immutable'; END IF;
 RETURN NEW;
END; $$ LANGUAGE plpgsql;
CREATE TRIGGER payout_account_commands_guard BEFORE UPDATE OR DELETE ON payout_account_commands
 FOR EACH ROW EXECUTE FUNCTION protect_payout_account_command();
