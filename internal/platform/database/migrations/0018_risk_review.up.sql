CREATE TABLE risk_signals (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  source_key text NOT NULL UNIQUE CHECK (char_length(source_key) BETWEEN 8 AND 200),
  resource_type text NOT NULL CHECK (resource_type IN ('task','order')),
  resource_id uuid NOT NULL,
  subject_user_id uuid NOT NULL REFERENCES users(id),
  actor_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
  signal_type text NOT NULL CHECK (signal_type IN ('task_dispute','transaction_refund')),
  severity text NOT NULL CHECK (severity IN ('low','medium','high','critical')),
  score integer NOT NULL CHECK (score BETWEEN 0 AND 100),
  status text NOT NULL DEFAULT 'open' CHECK (status IN ('open','reviewing','resolved','dismissed')),
  summary text NOT NULL CHECK (char_length(summary) BETWEEN 10 AND 240),
  evidence jsonb NOT NULL DEFAULT '{}'::jsonb,
  version integer NOT NULL DEFAULT 1 CHECK (version > 0),
  reviewer_id uuid REFERENCES users(id) ON DELETE SET NULL,
  resolution_outcome text CHECK (resolution_outcome IN ('monitor','no_action','escalated')),
  resolution_reason text CHECK (resolution_reason IS NULL OR char_length(resolution_reason) BETWEEN 10 AND 1000),
  detected_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  resolved_at timestamptz,
  CONSTRAINT risk_resolution_shape CHECK (
    (status='open' AND reviewer_id IS NULL AND resolution_outcome IS NULL AND resolution_reason IS NULL AND resolved_at IS NULL)
    OR (status='reviewing' AND reviewer_id IS NOT NULL AND resolution_outcome='monitor' AND resolution_reason IS NOT NULL AND resolved_at IS NULL)
    OR (status='resolved' AND reviewer_id IS NOT NULL AND resolution_outcome='escalated' AND resolution_reason IS NOT NULL AND resolved_at IS NOT NULL)
    OR (status='dismissed' AND reviewer_id IS NOT NULL AND resolution_outcome='no_action' AND resolution_reason IS NOT NULL AND resolved_at IS NOT NULL)
  )
);

CREATE INDEX risk_signals_queue_idx ON risk_signals(status,severity,score DESC,detected_at,id);
CREATE INDEX risk_signals_subject_idx ON risk_signals(subject_user_id,detected_at DESC,id DESC);
CREATE INDEX risk_signals_resource_idx ON risk_signals(resource_type,resource_id);

CREATE TABLE risk_events (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  signal_id uuid NOT NULL REFERENCES risk_signals(id) ON DELETE CASCADE,
  actor_id uuid REFERENCES users(id) ON DELETE SET NULL,
  kind text NOT NULL CHECK (kind IN ('detected','reviewed')),
  from_status text,
  to_status text NOT NULL CHECK (to_status IN ('open','reviewing','resolved','dismissed')),
  reason text NOT NULL CHECK (char_length(reason) BETWEEN 10 AND 1000),
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX risk_events_signal_idx ON risk_events(signal_id,created_at,id);

CREATE FUNCTION reject_risk_event_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'risk evidence is append-only';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER risk_events_immutable BEFORE UPDATE OR DELETE ON risk_events
  FOR EACH ROW EXECUTE FUNCTION reject_risk_event_mutation();

INSERT INTO permissions(id,module,description,risk_level,resource_authorization) VALUES
  ('admin:risk','admin','Review durable task and transaction risk signals','high',true)
ON CONFLICT (id) DO NOTHING;

INSERT INTO role_permissions(role,permission_id)
VALUES ('moderator','admin:risk'),('admin','admin:risk')
ON CONFLICT DO NOTHING;

WITH migrated AS (
  INSERT INTO risk_signals(source_key,resource_type,resource_id,subject_user_id,actor_user_id,signal_type,severity,score,summary,evidence,detected_at,updated_at)
  SELECT 'task_dispute:'||d.id::text,'task',d.id,td.opened_by,td.opened_by,'task_dispute','high',85,
         'Task dispute requires operational review.',
         jsonb_build_object('taskStatus',d.status,'amountCents',d.budget_cents,'currency',d.currency),
         td.created_at,td.created_at
  FROM demands d
  JOIN LATERAL (
    SELECT opened_by,created_at FROM task_disputes WHERE demand_id=d.id ORDER BY created_at,id LIMIT 1
  ) td ON true
  WHERE d.status='disputed'
  ON CONFLICT (source_key) DO NOTHING
  RETURNING id,actor_user_id,detected_at
)
INSERT INTO risk_events(signal_id,actor_id,kind,to_status,reason,created_at)
SELECT id,actor_user_id,'detected','open','Existing disputed task migrated into the risk review queue.',detected_at FROM migrated;

WITH migrated AS (
  INSERT INTO risk_signals(source_key,resource_type,resource_id,subject_user_id,actor_user_id,signal_type,severity,score,summary,evidence,detected_at,updated_at)
  SELECT 'order_refund:'||o.id::text,'order',o.id,o.buyer_id,o.buyer_id,'transaction_refund','medium',55,
         'Local Test refund requires transaction review.',
         jsonb_build_object('orderStatus',o.status,'amountCents',o.amount_cents,'currency',o.currency,'paymentMode','local_test'),
         COALESCE(o.refunded_at,o.updated_at),COALESCE(o.refunded_at,o.updated_at)
  FROM orders o WHERE o.status='test_refunded'
  ON CONFLICT (source_key) DO NOTHING
  RETURNING id,actor_user_id,detected_at
)
INSERT INTO risk_events(signal_id,actor_id,kind,to_status,reason,created_at)
SELECT id,actor_user_id,'detected','open','Existing Local Test refund migrated into the risk review queue.',detected_at FROM migrated;
