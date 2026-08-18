ALTER TABLE payment_destinations
  ADD COLUMN account_type text NOT NULL DEFAULT 'manual',
  ADD COLUMN details_submitted boolean NOT NULL DEFAULT false,
  ADD COLUMN requirements_due boolean NOT NULL DEFAULT false,
  ADD COLUMN admin_disabled boolean NOT NULL DEFAULT false,
  ADD COLUMN onboarding_started_at timestamptz,
  ADD COLUMN onboarding_completed_at timestamptz;

UPDATE payment_destinations
SET details_submitted = (status = 'verified'),
    admin_disabled = (status = 'disabled'),
    onboarding_completed_at = CASE WHEN status = 'verified' THEN COALESCE(verified_at, updated_at) ELSE NULL END;

ALTER TABLE payment_destinations DROP CONSTRAINT IF EXISTS payment_destinations_status_check;
ALTER TABLE payment_destinations DROP CONSTRAINT IF EXISTS payment_destinations_check;
ALTER TABLE payment_destinations ADD CONSTRAINT payment_destinations_account_type_check
  CHECK (account_type IN ('manual','express'));
ALTER TABLE payment_destinations ADD CONSTRAINT payment_destinations_status_check
  CHECK (status IN ('pending_onboarding','pending_verification','verified','restricted','disabled'));
ALTER TABLE payment_destinations ADD CONSTRAINT payment_destinations_state_check
  CHECK ((status='verified') = (verified_at IS NOT NULL AND charges_enabled AND payouts_enabled AND details_submitted AND NOT requirements_due AND NOT admin_disabled));

CREATE INDEX payment_destinations_onboarding_idx ON payment_destinations(status,updated_at DESC,id);

ALTER TABLE payment_provider_events
  ADD COLUMN destination_user_id uuid,
  ADD COLUMN account_charges_enabled boolean,
  ADD COLUMN account_payouts_enabled boolean,
  ADD COLUMN account_details_submitted boolean,
  ADD COLUMN account_requirements_due boolean;
CREATE INDEX payment_provider_events_destination_idx ON payment_provider_events(destination_id,occurred_at,id)
  WHERE destination_id IS NOT NULL;
