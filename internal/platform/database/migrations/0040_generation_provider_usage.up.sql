CREATE TABLE generation_provider_usage (
  generation_id uuid PRIMARY KEY REFERENCES generations(id),
  provider text NOT NULL CHECK (char_length(provider) BETWEEN 2 AND 80),
  model_name text NOT NULL CHECK (char_length(model_name) BETWEEN 2 AND 160),
  status text NOT NULL CHECK (status IN ('reported','not_reported')),
  input_tokens integer CHECK (input_tokens IS NULL OR input_tokens BETWEEN 0 AND 2000000),
  cached_input_tokens integer CHECK (cached_input_tokens IS NULL OR cached_input_tokens BETWEEN 0 AND 2000000),
  output_tokens integer CHECK (output_tokens IS NULL OR output_tokens BETWEEN 0 AND 2000000),
  reasoning_tokens integer CHECK (reasoning_tokens IS NULL OR reasoning_tokens BETWEEN 0 AND 2000000),
  total_tokens integer CHECK (total_tokens IS NULL OR total_tokens BETWEEN 0 AND 4000000),
  recorded_at timestamptz NOT NULL DEFAULT now(),
  CHECK (
    (status='reported' AND input_tokens IS NOT NULL AND output_tokens IS NOT NULL AND total_tokens IS NOT NULL
      AND cached_input_tokens IS NOT NULL AND reasoning_tokens IS NOT NULL
      AND cached_input_tokens <= input_tokens AND reasoning_tokens <= output_tokens
      AND total_tokens >= input_tokens + output_tokens)
    OR
    (status='not_reported' AND input_tokens IS NULL AND cached_input_tokens IS NULL
      AND output_tokens IS NULL AND reasoning_tokens IS NULL AND total_tokens IS NULL)
  )
);

CREATE INDEX generation_provider_usage_provider_idx ON generation_provider_usage(provider,model_name,recorded_at DESC);

CREATE FUNCTION reject_generation_provider_usage_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'generation provider usage evidence is immutable';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER generation_provider_usage_immutable
BEFORE UPDATE OR DELETE ON generation_provider_usage
FOR EACH ROW EXECUTE FUNCTION reject_generation_provider_usage_mutation();
