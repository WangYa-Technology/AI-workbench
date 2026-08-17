ALTER TABLE generation_commands
  ADD COLUMN request_hash text NOT NULL DEFAULT '';

ALTER TABLE generations
  ADD CONSTRAINT generations_charged_within_estimate
  CHECK (charged_cost_cents <= estimated_cost_cents);
