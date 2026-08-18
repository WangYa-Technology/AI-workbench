ALTER TABLE generations
  ADD COLUMN parent_generation_id uuid REFERENCES generations(id);

CREATE INDEX generations_parent_generation_idx
  ON generations (parent_generation_id)
  WHERE parent_generation_id IS NOT NULL;
