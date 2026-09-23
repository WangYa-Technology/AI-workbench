CREATE TABLE task_deadline_changes (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 demand_id uuid NOT NULL REFERENCES demands(id),
 proposed_by uuid NOT NULL REFERENCES users(id),
 previous_deadline timestamptz NOT NULL,
 deadline timestamptz NOT NULL,
 reason text NOT NULL,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','accepted','rejected')),
 resolved_by uuid REFERENCES users(id),
 resolved_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX task_deadline_one_pending ON task_deadline_changes(demand_id) WHERE status='pending';
INSERT INTO jobs(kind,payload,available_at,max_attempts)
 SELECT 'task.expire_open',jsonb_build_object('taskId',d.id),GREATEST(d.deadline,now()),20 FROM demands d
 WHERE d.status='open' AND NOT EXISTS(SELECT 1 FROM jobs j WHERE j.kind='task.expire_open' AND j.payload->>'taskId'=d.id::text AND j.status IN ('queued','running'));
