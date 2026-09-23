LOCK TABLE subscription_plans IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM subscription_plans WHERE version > 1)
 OR EXISTS(SELECT 1 FROM audit_events WHERE action IN ('billing.plan_created','billing.plan_updated')) THEN
  RAISE EXCEPTION 'subscription plan command history prevents downgrade';
 END IF;
END $$;
DROP TRIGGER subscription_plans_version ON subscription_plans;
DROP FUNCTION advance_subscription_plan_version();
ALTER TABLE subscription_plans DROP COLUMN version;
