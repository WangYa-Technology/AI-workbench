-- Stop old API writers before migration. Existing entries retain their original
-- evidence; never invent idempotency keys for historical adjustments.
LOCK TABLE billing_entries IN SHARE ROW EXCLUSIVE MODE;
CREATE TABLE admin_finance_adjustments (
 operation_id uuid PRIMARY KEY,
 actor_id uuid NOT NULL REFERENCES users(id),
 idempotency_key text NOT NULL CHECK(idempotency_key ~ '^[A-Za-z0-9._:-]{8,128}$'),
 user_id uuid NOT NULL REFERENCES users(id),
 delta_cents integer NOT NULL CHECK(delta_cents BETWEEN -1000000 AND 1000000 AND delta_cents<>0),
 currency text NOT NULL CHECK(currency='USD'),
 request_id text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(actor_id,idempotency_key)
);
CREATE INDEX admin_finance_adjustments_user_idx ON admin_finance_adjustments(user_id,created_at,operation_id);

CREATE FUNCTION protect_finance_adjustment_command() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 RAISE EXCEPTION 'finance adjustment commands are immutable' USING ERRCODE='23514';
END $$;
CREATE TRIGGER finance_adjustment_command_immutable BEFORE UPDATE OR DELETE ON admin_finance_adjustments
 FOR EACH ROW EXECUTE FUNCTION protect_finance_adjustment_command();

CREATE FUNCTION check_finance_adjustment_receipt() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM billing_entries e WHERE e.operation_id=NEW.operation_id AND e.user_id=NEW.user_id
  AND e.entry_type='admin_adjustment' AND e.currency=NEW.currency AND e.amount_cents=abs(NEW.delta_cents)
  AND e.direction=CASE WHEN NEW.delta_cents>0 THEN 'credit' ELSE 'debit' END
  AND e.metadata->>'actorId'=NEW.actor_id::text AND e.metadata->>'requestId'=NEW.request_id
  AND e.metadata->>'source'='admin_adjustment')
 OR NOT EXISTS(SELECT 1 FROM audit_events a WHERE a.actor_id=NEW.actor_id AND a.action='admin.finance_adjusted'
  AND a.resource_type='user' AND a.resource_id=NEW.user_id AND a.request_id=NEW.request_id
  AND a.metadata->>'operationId'=NEW.operation_id::text AND a.metadata->>'deltaCents'=NEW.delta_cents::text
  AND a.metadata->>'currency'=NEW.currency) THEN
  RAISE EXCEPTION 'finance adjustment requires matching ledger and audit' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE CONSTRAINT TRIGGER finance_adjustment_receipt AFTER INSERT ON admin_finance_adjustments
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_finance_adjustment_receipt();

-- Older binaries cannot add an untracked adjustment even if they ignore the
-- newly required HTTP header. Failure aborts their balance update as well.
CREATE FUNCTION check_finance_adjustment_entry() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF TG_OP<>'INSERT' THEN
  IF EXISTS(SELECT 1 FROM admin_finance_adjustments c WHERE c.operation_id=OLD.operation_id) THEN
   RAISE EXCEPTION 'command-backed finance entries are immutable' USING ERRCODE='23514';
  END IF;
 END IF;
 IF TG_OP<>'DELETE' AND NEW.entry_type='admin_adjustment' THEN
  IF NOT EXISTS(SELECT 1 FROM admin_finance_adjustments c WHERE c.operation_id=NEW.operation_id
   AND c.user_id=NEW.user_id AND c.currency=NEW.currency AND abs(c.delta_cents)=NEW.amount_cents
   AND NEW.direction=CASE WHEN c.delta_cents>0 THEN 'credit' ELSE 'debit' END
   AND NEW.metadata->>'actorId'=c.actor_id::text AND NEW.metadata->>'requestId'=c.request_id
   AND NEW.metadata->>'source'='admin_adjustment') THEN
   RAISE EXCEPTION 'finance-command-aware application required' USING ERRCODE='23514';
  END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER finance_adjustment_entry_guard BEFORE INSERT OR UPDATE OR DELETE ON billing_entries
 FOR EACH ROW EXECUTE FUNCTION check_finance_adjustment_entry();
