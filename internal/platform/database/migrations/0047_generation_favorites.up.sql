ALTER TABLE generations ADD COLUMN favorited_at timestamptz;

CREATE INDEX generations_owner_favorites_idx
  ON generations (owner_id, favorited_at DESC)
  WHERE favorited_at IS NOT NULL;
