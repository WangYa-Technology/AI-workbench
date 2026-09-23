-- Run only through the explicit development maintenance command, in one transaction.
-- Existing foreign keys stay enabled: unexpected dependencies abort the entire purge.
DO $$ BEGIN
  IF COALESCE(current_setting('hcai.retired_fixture_purge', true),'') NOT IN ('development','test') THEN
    RAISE EXCEPTION 'Development/test maintenance context is required';
  END IF;
END $$;

LOCK TABLE users,assets,works,posts,products,demands,task_events,audit_events,audit_chain_state,
  system_settings IN EXCLUSIVE MODE;

CREATE TEMP TABLE purge_users(id uuid PRIMARY KEY) ON COMMIT DROP;
INSERT INTO purge_users VALUES
 ('00000000-0000-4000-8000-000000000001'),('00000000-0000-4000-8000-000000000002'),
 ('00000000-0000-4000-8000-000000000003'),('00000000-0000-4000-8000-000000000004');
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM users WHERE id IN (SELECT id FROM purge_users)
    AND (status<>'deleted' OR password_hash IS NOT NULL OR email<>'deleted+'||id::text||'@hcai.invalid')) THEN
    RAISE EXCEPTION 'Only the four already-retired shared identities can be purged';
  END IF;
END $$;

CREATE TEMP TABLE purge_assets ON COMMIT DROP AS SELECT id FROM assets WHERE owner_id IN (SELECT id FROM purge_users);
CREATE TEMP TABLE purge_works ON COMMIT DROP AS SELECT id FROM works WHERE author_id IN (SELECT id FROM purge_users);
CREATE TEMP TABLE purge_posts ON COMMIT DROP AS SELECT id FROM posts WHERE author_id IN (SELECT id FROM purge_users);
CREATE TEMP TABLE purge_products ON COMMIT DROP AS SELECT id FROM products WHERE seller_id IN (SELECT id FROM purge_users);
CREATE TEMP TABLE purge_demands ON COMMIT DROP AS SELECT id FROM demands WHERE client_id IN (SELECT id FROM purge_users);
CREATE TEMP TABLE purge_ids(id uuid PRIMARY KEY) ON COMMIT DROP;
INSERT INTO purge_ids SELECT id FROM purge_users UNION SELECT id FROM purge_assets
  UNION SELECT id FROM purge_works UNION SELECT id FROM purge_posts
  UNION SELECT id FROM purge_products UNION SELECT id FROM purge_demands;
INSERT INTO purge_ids SELECT id FROM user_subscriptions WHERE user_id IN (SELECT id FROM purge_users) ON CONFLICT DO NOTHING;
INSERT INTO purge_ids SELECT id FROM task_events WHERE demand_id IN (SELECT id FROM purge_demands) ON CONFLICT DO NOTHING;

-- Never cascade into another person's content, trade or generation.
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM comments WHERE post_id IN (SELECT id FROM purge_posts) AND author_id NOT IN (SELECT id FROM purge_users))
    OR EXISTS (SELECT 1 FROM proposals WHERE demand_id IN (SELECT id FROM purge_demands) AND creator_id NOT IN (SELECT id FROM purge_users))
    OR EXISTS (SELECT 1 FROM orders WHERE product_id IN (SELECT id FROM purge_products))
    OR EXISTS (SELECT 1 FROM generations WHERE owner_id IN (SELECT id FROM purge_users)) THEN
    RAISE EXCEPTION 'Unexpected business dependencies require a separately reviewed cleanup';
  END IF;
END $$;

-- Only these two append-only tables contain retired fixture evidence in this database.
-- Disable their mutation guards within this transaction; keep all FK checks enabled.
ALTER TABLE task_events DISABLE TRIGGER task_events_immutable;
ALTER TABLE audit_events DISABLE TRIGGER audit_events_immutable;

DELETE FROM request_observations WHERE route='/api/v1/auth/demo' OR request_id IN (
  SELECT e.request_id FROM audit_events e WHERE e.actor_id IN (SELECT id FROM purge_users)
    OR e.resource_id IN (SELECT id FROM purge_ids)
    OR EXISTS (SELECT 1 FROM purge_ids p WHERE strpos(e.metadata::text,p.id::text)>0)
);
DELETE FROM audit_events e WHERE e.actor_id IN (SELECT id FROM purge_users)
  OR e.resource_id IN (SELECT id FROM purge_ids)
  OR EXISTS (SELECT 1 FROM purge_ids p WHERE strpos(e.metadata::text,p.id::text)>0);
DELETE FROM task_events WHERE demand_id IN (SELECT id FROM purge_demands) OR actor_id IN (SELECT id FROM purge_users);
DELETE FROM comments WHERE author_id IN (SELECT id FROM purge_users);
DELETE FROM posts WHERE id IN (SELECT id FROM purge_posts);
DELETE FROM works WHERE id IN (SELECT id FROM purge_works);
DELETE FROM products WHERE id IN (SELECT id FROM purge_products);
DELETE FROM demands WHERE id IN (SELECT id FROM purge_demands);
DELETE FROM assets WHERE id IN (SELECT id FROM purge_assets);
-- Preserve the site's actual settings; clear the retired editor attribution.
UPDATE system_settings SET updated_by=NULL WHERE updated_by IN (SELECT id FROM purge_users);
DELETE FROM users WHERE id IN (SELECT id FROM purge_users);

-- Preserve remaining event IDs, sequence numbers and business fields. Rebaseline only
-- their chain hashes after explicitly authorized fixture-history erasure.
DO $$
DECLARE item audit_events%ROWTYPE; previous text := ''; current_hash text; last_sequence bigint := 0;
BEGIN
  FOR item IN SELECT * FROM audit_events ORDER BY sequence LOOP
    current_hash := audit_event_hash(item.sequence,NULLIF(previous,''),item.id,item.actor_id,
      item.action,item.resource_type,item.resource_id,item.reason,item.request_id,item.metadata,item.created_at);
    UPDATE audit_events SET previous_hash=NULLIF(previous,''),event_hash=current_hash WHERE id=item.id;
    previous := current_hash;
    last_sequence := item.sequence;
  END LOOP;
  UPDATE audit_chain_state SET head_sequence=last_sequence,head_hash=previous,updated_at=now() WHERE singleton=true;
END $$;
ALTER TABLE task_events ENABLE TRIGGER task_events_immutable;
ALTER TABLE audit_events ENABLE TRIGGER audit_events_immutable;

-- Fail closed if a polymorphic reference or an unexpected old row survived.
DO $$
DECLARE item record; remaining bigint;
BEGIN
  FOR item IN SELECT tablename FROM pg_tables WHERE schemaname=current_schema() AND tablename<>'schema_migrations' LOOP
    EXECUTE format('SELECT count(*) FROM %I t WHERE EXISTS (SELECT 1 FROM purge_ids p WHERE strpos(to_jsonb(t)::text,p.id::text)>0)',item.tablename) INTO remaining;
    IF remaining>0 THEN RAISE EXCEPTION 'Retired fixture references remain in %',item.tablename; END IF;
  END LOOP;
END $$;
SELECT 'Retired fixture rows removed; foreign keys and evidence guards remain enabled.' AS result;
