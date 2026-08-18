ALTER TABLE payment_destinations DROP CONSTRAINT IF EXISTS payment_destinations_state_check;
ALTER TABLE payment_destinations DROP CONSTRAINT IF EXISTS payment_destinations_status_check;
ALTER TABLE payment_destinations DROP CONSTRAINT IF EXISTS payment_destinations_account_type_check;

UPDATE payment_destinations
SET status = CASE WHEN status = 'verified' THEN 'verified' ELSE 'disabled' END,
    admin_disabled = CASE WHEN status = 'verified' THEN false ELSE true END,
    details_submitted = CASE WHEN status = 'verified' THEN true ELSE false END,
    requirements_due = false;

ALTER TABLE payment_destinations ADD CONSTRAINT payment_destinations_status_check
  CHECK (status IN ('verified','disabled'));
ALTER TABLE payment_destinations ADD CONSTRAINT payment_destinations_check
  CHECK ((status='verified') = (verified_at IS NOT NULL AND charges_enabled AND payouts_enabled));
DROP INDEX IF EXISTS payment_destinations_onboarding_idx;
ALTER TABLE payment_destinations
  DROP COLUMN IF EXISTS onboarding_completed_at,
  DROP COLUMN IF EXISTS onboarding_started_at,
  DROP COLUMN IF EXISTS admin_disabled,
  DROP COLUMN IF EXISTS requirements_due,
  DROP COLUMN IF EXISTS details_submitted,
  DROP COLUMN IF EXISTS account_type;
DROP INDEX IF EXISTS payment_provider_events_destination_idx;
ALTER TABLE payment_provider_events
  DROP COLUMN IF EXISTS account_requirements_due,
  DROP COLUMN IF EXISTS account_details_submitted,
  DROP COLUMN IF EXISTS account_payouts_enabled,
  DROP COLUMN IF EXISTS account_charges_enabled,
  DROP COLUMN IF EXISTS destination_user_id;
