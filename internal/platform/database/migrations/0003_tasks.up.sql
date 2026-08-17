ALTER TABLE demands
  ADD COLUMN summary text NOT NULL DEFAULT '',
  ADD COLUMN deliverables jsonb NOT NULL DEFAULT '[]'::jsonb,
  ADD COLUMN acceptance_rules jsonb NOT NULL DEFAULT '[]'::jsonb,
  ADD COLUMN rights_terms text NOT NULL DEFAULT '',
  ADD COLUMN ai_disclosure_requirement text NOT NULL DEFAULT '',
  ADD COLUMN client_timezone text NOT NULL DEFAULT 'UTC',
  ADD COLUMN allow_direct_accept boolean NOT NULL DEFAULT false,
  ADD COLUMN idempotency_key text,
  ADD COLUMN accepted_at timestamptz,
  ADD COLUMN cancelled_at timestamptz;

CREATE UNIQUE INDEX demands_client_idempotency_key
  ON demands(client_id,idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX demands_marketplace_idx ON demands(status,deadline,budget_cents DESC);
CREATE INDEX demands_client_idx ON demands(client_id,updated_at DESC);
CREATE INDEX demands_assignee_idx ON demands(assignee_id,updated_at DESC) WHERE assignee_id IS NOT NULL;

ALTER TABLE generations ADD COLUMN source_task_id uuid REFERENCES demands(id);
CREATE INDEX generations_source_task_idx ON generations(source_task_id) WHERE source_task_id IS NOT NULL;

ALTER TABLE proposals
  ADD COLUMN deliverables text NOT NULL DEFAULT '',
  ADD COLUMN timeline_days integer CHECK (timeline_days IS NULL OR timeline_days > 0),
  ADD COLUMN idempotency_key text,
  ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

CREATE UNIQUE INDEX proposals_creator_idempotency_key
  ON proposals(creator_id,idempotency_key) WHERE idempotency_key IS NOT NULL;

ALTER TABLE deliveries
  ADD COLUMN version integer NOT NULL DEFAULT 1 CHECK (version > 0),
  ADD COLUMN idempotency_key text,
  ADD COLUMN review_note text NOT NULL DEFAULT '',
  ADD COLUMN reviewed_at timestamptz,
  ADD COLUMN accepted_at timestamptz;

CREATE UNIQUE INDEX deliveries_creator_idempotency_key
  ON deliveries(creator_id,idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE UNIQUE INDEX deliveries_single_active_review
  ON deliveries(demand_id) WHERE status='submitted';

CREATE TABLE task_events (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  demand_id uuid NOT NULL REFERENCES demands(id) ON DELETE CASCADE,
  actor_id uuid REFERENCES users(id),
  kind text NOT NULL,
  from_status text,
  to_status text NOT NULL,
  note text NOT NULL DEFAULT '',
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX task_events_demand_idx ON task_events(demand_id,created_at,id);

CREATE TABLE task_disputes (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  demand_id uuid NOT NULL REFERENCES demands(id) ON DELETE CASCADE,
  opened_by uuid NOT NULL REFERENCES users(id),
  reason text NOT NULL,
  status text NOT NULL DEFAULT 'open' CHECK (status IN ('open','resolved_creator','resolved_client')),
  resolution_note text NOT NULL DEFAULT '',
  resolved_by uuid REFERENCES users(id),
  resolved_at timestamptz,
  idempotency_key text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(opened_by,idempotency_key)
);

CREATE UNIQUE INDEX task_disputes_single_open ON task_disputes(demand_id) WHERE status='open';

CREATE TABLE task_settlements (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  demand_id uuid NOT NULL UNIQUE REFERENCES demands(id) ON DELETE CASCADE,
  client_id uuid NOT NULL REFERENCES users(id),
  creator_id uuid NOT NULL REFERENCES users(id),
  amount_cents integer NOT NULL CHECK (amount_cents > 0),
  currency text NOT NULL CHECK (char_length(currency)=3),
  mode text NOT NULL DEFAULT 'local_test' CHECK (mode='local_test'),
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE task_commands (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  actor_id uuid NOT NULL REFERENCES users(id),
  demand_id uuid REFERENCES demands(id) ON DELETE CASCADE,
  operation text NOT NULL,
  idempotency_key text NOT NULL,
  request_hash text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(actor_id,operation,idempotency_key)
);

CREATE UNIQUE INDEX ledger_task_entry_unique
  ON ledger_entries(operation_id,account_id,direction,reason);
