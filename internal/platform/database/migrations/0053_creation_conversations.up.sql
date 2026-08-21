CREATE TABLE creation_conversations (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  owner_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  title text NOT NULL DEFAULT 'New conversation',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX creation_conversations_owner_updated_idx
  ON creation_conversations (owner_id, updated_at DESC, id DESC);

ALTER TABLE generations
  ADD COLUMN conversation_id uuid REFERENCES creation_conversations(id);

CREATE INDEX generations_conversation_idx
  ON generations (conversation_id, created_at ASC, id ASC)
  WHERE conversation_id IS NOT NULL;

WITH RECURSIVE generation_roots AS (
  SELECT id, id AS root_id
  FROM generations
  WHERE parent_generation_id IS NULL
  UNION ALL
  SELECT child.id, roots.root_id
  FROM generations child
  JOIN generation_roots roots ON roots.id = child.parent_generation_id
), conversation_roots AS (
  SELECT roots.root_id, g.owner_id, left(g.prompt, 120) AS title,
         min(g.created_at) AS created_at, max(g.updated_at) AS updated_at
  FROM generation_roots roots
  JOIN generations g ON g.id = roots.root_id
  GROUP BY roots.root_id, g.owner_id, g.prompt
)
INSERT INTO creation_conversations(id, owner_id, title, created_at, updated_at)
SELECT root_id, owner_id, NULLIF(title, ''), created_at, updated_at
FROM conversation_roots
ON CONFLICT (id) DO NOTHING;

WITH RECURSIVE generation_roots AS (
  SELECT id, id AS root_id
  FROM generations
  WHERE parent_generation_id IS NULL
  UNION ALL
  SELECT child.id, roots.root_id
  FROM generations child
  JOIN generation_roots roots ON roots.id = child.parent_generation_id
)
UPDATE generations g
SET conversation_id = roots.root_id
FROM generation_roots roots
WHERE g.id = roots.id
  AND g.conversation_id IS NULL;
