ALTER TABLE comments ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

CREATE INDEX comments_post_published_idx ON comments(post_id,created_at,id) WHERE status='published';

CREATE TABLE post_reactions (
  post_id uuid NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind text NOT NULL CHECK (kind IN ('like','bookmark')),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(post_id,user_id,kind)
);

CREATE INDEX post_reactions_user_idx ON post_reactions(user_id,kind,created_at DESC);

CREATE TABLE user_follows (
  follower_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  following_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(follower_id,following_id),
  CHECK (follower_id <> following_id)
);

CREATE INDEX user_follows_following_idx ON user_follows(following_id,created_at DESC);

CREATE TABLE content_reports (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  reporter_id uuid NOT NULL REFERENCES users(id),
  resource_type text NOT NULL CHECK (resource_type IN ('work','post','comment')),
  resource_id uuid NOT NULL,
  subject_author_id uuid NOT NULL REFERENCES users(id),
  category text NOT NULL CHECK (category IN ('spam','harassment','copyright','sexual','violence','misleading','other')),
  details text NOT NULL,
  status text NOT NULL DEFAULT 'open' CHECK (status IN ('open','reviewing','resolved','dismissed')),
  outcome text CHECK (outcome IN ('no_action','hidden','removed')),
  previous_status text,
  moderator_id uuid REFERENCES users(id),
  resolution_reason text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  resolved_at timestamptz
);

CREATE UNIQUE INDEX content_reports_one_open_idx
ON content_reports(reporter_id,resource_type,resource_id)
WHERE status IN ('open','reviewing');
CREATE INDEX content_reports_queue_idx ON content_reports(status,created_at,id);
CREATE INDEX content_reports_subject_idx ON content_reports(subject_author_id,created_at DESC);

CREATE TABLE moderation_appeals (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  report_id uuid NOT NULL REFERENCES content_reports(id) ON DELETE CASCADE,
  appellant_id uuid NOT NULL REFERENCES users(id),
  reason text NOT NULL,
  status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','upheld','denied')),
  reviewer_id uuid REFERENCES users(id),
  resolution_reason text,
  created_at timestamptz NOT NULL DEFAULT now(),
  resolved_at timestamptz,
  UNIQUE(report_id,appellant_id)
);

CREATE INDEX moderation_appeals_queue_idx ON moderation_appeals(status,created_at,id);

CREATE TABLE governance_events (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  report_id uuid NOT NULL REFERENCES content_reports(id) ON DELETE CASCADE,
  appeal_id uuid REFERENCES moderation_appeals(id) ON DELETE CASCADE,
  actor_id uuid NOT NULL REFERENCES users(id),
  kind text NOT NULL CHECK (kind IN ('reported','resolved','dismissed','appealed','appeal_upheld','appeal_denied')),
  from_status text,
  to_status text NOT NULL,
  reason text NOT NULL,
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX governance_events_report_idx ON governance_events(report_id,created_at,id);

INSERT INTO permissions(id,module,description,risk_level,resource_authorization) VALUES
  ('community:interact','community','Comment, react, bookmark, and follow','low',true),
  ('community:report','community','Report Community content and file appeals','medium',true),
  ('admin:governance','admin','Review reports and resolve moderation appeals','high',true)
ON CONFLICT (id) DO NOTHING;

INSERT INTO role_permissions(role,permission_id)
SELECT role,permission_id
FROM (VALUES ('member'),('creator'),('publisher'),('moderator'),('admin')) roles(role)
CROSS JOIN (VALUES ('community:interact'),('community:report')) permissions(permission_id)
ON CONFLICT DO NOTHING;

INSERT INTO role_permissions(role,permission_id)
VALUES ('moderator','admin:governance'),('admin','admin:governance')
ON CONFLICT DO NOTHING;
