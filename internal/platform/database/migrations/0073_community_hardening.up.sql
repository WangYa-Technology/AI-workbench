ALTER TABLE posts ADD COLUMN owner_removed boolean NOT NULL DEFAULT false;
INSERT INTO role_permissions(role,permission_id) VALUES('moderator','community:publish') ON CONFLICT DO NOTHING;
ALTER TABLE posts DROP CONSTRAINT posts_title_length_check;
ALTER TABLE posts ADD CONSTRAINT posts_title_length_check CHECK(work_id IS NOT NULL OR
 (title IS NOT NULL AND char_length(title)<=120 AND (status IN ('draft','removed') OR char_length(title)>=3)));
ALTER TABLE comments ADD COLUMN version integer NOT NULL DEFAULT 1;
ALTER TABLE content_reports ADD COLUMN version integer NOT NULL DEFAULT 1;
ALTER TABLE moderation_appeals ADD COLUMN version integer NOT NULL DEFAULT 1;
ALTER TABLE content_reports ADD COLUMN decision_version integer NOT NULL DEFAULT 0;
UPDATE content_reports SET decision_version=1 WHERE status IN ('resolved','dismissed');
ALTER TABLE moderation_appeals ADD COLUMN decision_version integer NOT NULL DEFAULT 1;
ALTER TABLE moderation_appeals ADD COLUMN decision_outcome text;
UPDATE moderation_appeals a SET decision_outcome=r.outcome FROM content_reports r WHERE r.id=a.report_id;
ALTER TABLE moderation_appeals DROP CONSTRAINT moderation_appeals_report_id_appellant_id_key;
ALTER TABLE moderation_appeals ADD CONSTRAINT moderation_appeals_actor_decision_key UNIQUE(report_id,appellant_id,decision_version);

-- The same public projection is used by feeds, details and interactions.
CREATE VIEW community_visible_posts AS
 SELECT p.* FROM posts p JOIN users u ON u.id=p.author_id
 LEFT JOIN works w ON w.id=p.work_id LEFT JOIN assets a ON a.id=w.asset_id
 WHERE p.status='published' AND NOT p.owner_removed AND u.status='active'
 AND (p.work_id IS NULL OR (w.status='published' AND a.scan_status='clean'));

CREATE TABLE community_commands (
 actor_id uuid NOT NULL REFERENCES users(id),
 operation text NOT NULL,
 request_key text NOT NULL,
 payload_hash text NOT NULL,
 resource_id uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(actor_id,operation,request_key)
);

CREATE TABLE community_feed_snapshots (
 id uuid PRIMARY KEY,
 viewer_id uuid,
 scope_hash text NOT NULL,
 expires_at timestamptz NOT NULL DEFAULT now()+interval '30 minutes'
);
CREATE INDEX community_feed_snapshots_expiry ON community_feed_snapshots(expires_at);
CREATE TABLE community_feed_snapshot_items (
 snapshot_id uuid NOT NULL REFERENCES community_feed_snapshots(id) ON DELETE CASCADE,
 position bigint NOT NULL,
 post_id uuid NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
 PRIMARY KEY(snapshot_id,position),
 UNIQUE(snapshot_id,post_id)
);

CREATE FUNCTION protect_governance_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'governance evidence is immutable'; END;
$$;
CREATE TRIGGER governance_events_immutable BEFORE UPDATE OR DELETE ON governance_events
 FOR EACH ROW EXECUTE FUNCTION protect_governance_event();

-- Track changes made outside moderation as well as controlled operations.
CREATE FUNCTION increment_content_version() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.version=OLD.version THEN NEW.version := OLD.version+1; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER posts_revision BEFORE UPDATE ON posts FOR EACH ROW EXECUTE FUNCTION increment_content_version();
CREATE TRIGGER works_revision BEFORE UPDATE ON works FOR EACH ROW EXECUTE FUNCTION increment_content_version();
CREATE TRIGGER comments_revision BEFORE UPDATE ON comments FOR EACH ROW EXECUTE FUNCTION increment_content_version();

CREATE TABLE content_moderation_resources (
 resource_type text NOT NULL CHECK(resource_type IN ('post','work','comment')),
 resource_id uuid NOT NULL,
 base_status text NOT NULL,
 applied_version integer NOT NULL,
 PRIMARY KEY(resource_type,resource_id)
);
CREATE TABLE content_moderation_holds (
 report_id uuid NOT NULL REFERENCES content_reports(id),
 resource_type text NOT NULL,
 resource_id uuid NOT NULL,
 status text NOT NULL CHECK(status IN ('hidden','removed')),
 PRIMARY KEY(report_id,resource_type,resource_id),
 FOREIGN KEY(resource_type,resource_id) REFERENCES content_moderation_resources(resource_type,resource_id)
);
