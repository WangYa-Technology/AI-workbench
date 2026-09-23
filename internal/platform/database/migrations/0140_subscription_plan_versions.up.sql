ALTER TABLE subscription_plans ADD COLUMN version bigint NOT NULL DEFAULT 1 CHECK (version > 0);

-- Direct/older writers still invalidate an open editor's accepted revision.
CREATE FUNCTION advance_subscription_plan_version() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 NEW.version := OLD.version + 1;
 RETURN NEW;
END;
$$;
CREATE TRIGGER subscription_plans_version BEFORE UPDATE ON subscription_plans
 FOR EACH ROW EXECUTE FUNCTION advance_subscription_plan_version();
