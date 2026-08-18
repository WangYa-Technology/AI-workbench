ALTER TABLE generations
  DROP CONSTRAINT IF EXISTS generations_parameters_object,
  DROP COLUMN IF EXISTS parameters;
