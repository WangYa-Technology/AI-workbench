CREATE TABLE support_cases (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  requester_id uuid NOT NULL REFERENCES users(id),
  category text NOT NULL CHECK (category IN ('general_support','billing','account','task_or_order','copyright')),
  subject text NOT NULL CHECK (char_length(subject) BETWEEN 4 AND 160),
  details text NOT NULL CHECK (char_length(details) BETWEEN 20 AND 4000),
  related_resource_type text CHECK (related_resource_type IN ('work','product','post','asset','generation','order','task')),
  related_resource_id uuid,
  locale text NOT NULL CHECK (locale IN ('en-US','zh-CN')),
  claimant_relationship text CHECK (claimant_relationship IN ('rights_holder','authorized_agent')),
  rights_statement text CHECK (rights_statement IS NULL OR char_length(rights_statement) BETWEEN 20 AND 1500),
  status text NOT NULL DEFAULT 'open' CHECK (status IN ('open','in_review','waiting_for_requester','resolved','closed')),
  version integer NOT NULL DEFAULT 1 CHECK (version > 0),
  assigned_operator_id uuid REFERENCES users(id) ON DELETE SET NULL,
  resolution_code text CHECK (resolution_code IS NULL OR resolution_code IN ('answered','fixed','refund_guidance','content_restricted','no_action','duplicate','withdrawn')),
  resolution_reason text CHECK (resolution_reason IS NULL OR char_length(resolution_reason) BETWEEN 10 AND 1000),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  resolved_at timestamptz,
  CONSTRAINT support_related_resource_pair CHECK ((related_resource_type IS NULL) = (related_resource_id IS NULL)),
  CONSTRAINT support_copyright_evidence CHECK (
    category <> 'copyright' OR
    (related_resource_type IN ('work','product','post') AND related_resource_id IS NOT NULL AND claimant_relationship IS NOT NULL AND rights_statement IS NOT NULL)
  ),
  CONSTRAINT support_resolution_evidence CHECK (
    (status IN ('resolved','closed') AND resolution_code IS NOT NULL AND resolution_reason IS NOT NULL AND resolved_at IS NOT NULL)
    OR (status NOT IN ('resolved','closed') AND resolved_at IS NULL)
  )
);

CREATE INDEX support_cases_requester_idx ON support_cases(requester_id,updated_at DESC,id DESC);
CREATE INDEX support_cases_operations_idx ON support_cases(status,updated_at DESC,id DESC);
CREATE INDEX support_cases_assignee_idx ON support_cases(assigned_operator_id,status,updated_at DESC) WHERE assigned_operator_id IS NOT NULL;
CREATE INDEX support_cases_related_idx ON support_cases(related_resource_type,related_resource_id) WHERE related_resource_id IS NOT NULL;

CREATE TABLE support_messages (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  case_id uuid NOT NULL REFERENCES support_cases(id) ON DELETE CASCADE,
  author_id uuid REFERENCES users(id) ON DELETE SET NULL,
  author_role text NOT NULL CHECK (author_role IN ('requester','operator')),
  body text NOT NULL CHECK (char_length(body) BETWEEN 2 AND 4000),
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX support_messages_case_idx ON support_messages(case_id,created_at,id);

CREATE TABLE support_events (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  case_id uuid NOT NULL REFERENCES support_cases(id) ON DELETE CASCADE,
  actor_id uuid REFERENCES users(id) ON DELETE SET NULL,
  kind text NOT NULL CHECK (kind IN ('created','requester_replied','operator_replied','status_changed')),
  from_status text,
  to_status text NOT NULL,
  message_id uuid REFERENCES support_messages(id) ON DELETE SET NULL,
  reason text NOT NULL,
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX support_events_case_idx ON support_events(case_id,created_at,id);

CREATE FUNCTION reject_support_evidence_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'support evidence is append-only';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER support_messages_immutable BEFORE UPDATE OR DELETE ON support_messages
  FOR EACH ROW EXECUTE FUNCTION reject_support_evidence_mutation();
CREATE TRIGGER support_events_immutable BEFORE UPDATE OR DELETE ON support_events
  FOR EACH ROW EXECUTE FUNCTION reject_support_evidence_mutation();

INSERT INTO permissions(id,module,description,risk_level,resource_authorization) VALUES
  ('support:self','support','Create and manage support cases owned by the current account','medium',true),
  ('admin:support','admin','Review, reply to, assign, and resolve support and copyright intake','high',true)
ON CONFLICT (id) DO NOTHING;

INSERT INTO role_permissions(role,permission_id)
SELECT role,'support:self' FROM (VALUES ('member'),('creator'),('publisher'),('moderator'),('admin')) roles(role)
ON CONFLICT DO NOTHING;

INSERT INTO role_permissions(role,permission_id)
VALUES ('moderator','admin:support'),('admin','admin:support')
ON CONFLICT DO NOTHING;
