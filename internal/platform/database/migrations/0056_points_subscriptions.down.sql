DROP TRIGGER IF EXISTS users_create_default_point_account_and_subscription ON users;
DROP FUNCTION IF EXISTS create_default_point_account_and_subscription();
ALTER TABLE billing_entries DROP CONSTRAINT billing_entries_entry_type_check;
ALTER TABLE billing_entries ADD CONSTRAINT billing_entries_entry_type_check CHECK (entry_type IN (
  'generation_charge','product_purchase','product_sale','product_refund',
  'task_payment','task_earning','admin_adjustment','initial_credit'
));
ALTER TABLE generations
  DROP COLUMN IF EXISTS point_usage_snapshot,
  DROP COLUMN IF EXISTS point_pricing_snapshot,
  DROP COLUMN IF EXISTS charged_points,
  DROP COLUMN IF EXISTS estimated_points,
  DROP COLUMN IF EXISTS provider_model_id;
DROP TABLE IF EXISTS point_reservations;
DROP TABLE IF EXISTS model_point_pricing_rules;
DROP TABLE IF EXISTS point_entries;
DROP TABLE IF EXISTS user_subscriptions;
DROP TABLE IF EXISTS point_accounts;
DROP TABLE IF EXISTS subscription_plan_models;
DROP TABLE IF EXISTS subscription_plans;
