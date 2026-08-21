DROP INDEX IF EXISTS generations_conversation_idx;
ALTER TABLE generations DROP COLUMN IF EXISTS conversation_id;
DROP INDEX IF EXISTS creation_conversations_owner_updated_idx;
DROP TABLE IF EXISTS creation_conversations;
