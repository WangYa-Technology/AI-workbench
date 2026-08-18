ALTER TABLE generations
  ADD COLUMN parameters jsonb NOT NULL DEFAULT '{}'::jsonb,
  ADD CONSTRAINT generations_parameters_object CHECK (jsonb_typeof(parameters) = 'object');
