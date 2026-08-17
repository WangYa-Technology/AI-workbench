DELETE FROM role_permissions WHERE permission_id IN ('community:interact','community:report','admin:governance');
DELETE FROM permissions WHERE id IN ('community:interact','community:report','admin:governance');

DROP TABLE IF EXISTS governance_events;
DROP TABLE IF EXISTS moderation_appeals;
DROP TABLE IF EXISTS content_reports;
DROP TABLE IF EXISTS user_follows;
DROP TABLE IF EXISTS post_reactions;

DROP INDEX IF EXISTS comments_post_published_idx;
ALTER TABLE comments DROP COLUMN IF EXISTS updated_at;
