CREATE TABLE provider_cost_reconciliations (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  provider text NOT NULL CHECK (provider IN ('openai')),
  period_start timestamptz NOT NULL,
  period_end timestamptz NOT NULL,
  currency text,
  provider_cost_micros bigint CHECK (provider_cost_micros IS NULL OR provider_cost_micros >= 0),
  local_estimated_cost_micros bigint CHECK (local_estimated_cost_micros IS NULL OR local_estimated_cost_micros >= 0),
  reported_input_tokens bigint CHECK (reported_input_tokens IS NULL OR reported_input_tokens >= 0),
  reported_total_tokens bigint CHECK (reported_total_tokens IS NULL OR reported_total_tokens >= 0),
  variance_micros bigint,
  overage_threshold_micros bigint NOT NULL CHECK (overage_threshold_micros BETWEEN 0 AND 1000000000000),
  status text NOT NULL CHECK (status IN ('queued','running','matched','overage','failed')),
  request_reason text NOT NULL CHECK (char_length(request_reason) BETWEEN 12 AND 1000),
  requested_by uuid NOT NULL REFERENCES users(id),
  job_id uuid UNIQUE REFERENCES jobs(id),
  error_code text CHECK (error_code IS NULL OR error_code ~ '^[a-z0-9_]{3,80}$'),
  started_at timestamptz,
  completed_at timestamptz,
  version integer NOT NULL DEFAULT 1 CHECK (version > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (period_start = date_trunc('day', period_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
  CHECK (period_end = date_trunc('day', period_end AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
  CHECK (period_end > period_start AND period_end <= period_start + interval '180 days'),
  CHECK ((status IN ('queued','running')) = (currency IS NULL AND provider_cost_micros IS NULL AND local_estimated_cost_micros IS NULL AND reported_input_tokens IS NULL AND reported_total_tokens IS NULL AND variance_micros IS NULL AND completed_at IS NULL AND error_code IS NULL)),
  CHECK ((status IN ('matched','overage')) = (currency IS NOT NULL AND provider_cost_micros IS NOT NULL AND local_estimated_cost_micros IS NOT NULL AND reported_input_tokens IS NOT NULL AND reported_total_tokens IS NOT NULL AND variance_micros = provider_cost_micros - local_estimated_cost_micros AND completed_at IS NOT NULL AND error_code IS NULL)),
  CHECK ((status='failed') = (error_code IS NOT NULL AND completed_at IS NOT NULL))
);

CREATE UNIQUE INDEX provider_cost_reconciliations_active_period_idx
  ON provider_cost_reconciliations(provider,period_start,period_end)
  WHERE status IN ('queued','running');
CREATE INDEX provider_cost_reconciliations_history_idx
  ON provider_cost_reconciliations(provider,created_at DESC,id DESC);

CREATE FUNCTION protect_provider_cost_reconciliation() RETURNS trigger AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'provider reconciliation evidence cannot be deleted';
  END IF;
  IF OLD.status IN ('matched','overage','failed') THEN
    RAISE EXCEPTION 'finalized provider reconciliation evidence is immutable';
  END IF;
  IF NEW.id <> OLD.id OR NEW.provider <> OLD.provider OR NEW.period_start <> OLD.period_start OR NEW.period_end <> OLD.period_end
     OR NEW.overage_threshold_micros <> OLD.overage_threshold_micros OR NEW.request_reason <> OLD.request_reason
     OR NEW.requested_by <> OLD.requested_by OR NEW.job_id <> OLD.job_id OR NEW.created_at <> OLD.created_at THEN
    RAISE EXCEPTION 'provider reconciliation request evidence is immutable';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER provider_cost_reconciliations_protected
BEFORE UPDATE OR DELETE ON provider_cost_reconciliations
FOR EACH ROW EXECUTE FUNCTION protect_provider_cost_reconciliation();
