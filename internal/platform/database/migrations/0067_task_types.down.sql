ALTER TABLE demands DROP CONSTRAINT demands_task_type_fk;
UPDATE demands SET deliverable_type='mixed' WHERE deliverable_type NOT IN ('image','video','audio','prompt','workflow','mixed');
ALTER TABLE demands ADD CONSTRAINT demands_deliverable_type_check CHECK (deliverable_type IN ('image','video','audio','prompt','workflow','mixed'));
DROP TABLE task_types;
