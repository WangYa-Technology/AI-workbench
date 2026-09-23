DROP TABLE task_delivery_grants;
DROP FUNCTION reject_task_grant_mutation();
DROP TABLE delivery_assets;
ALTER TABLE deliveries DROP COLUMN rights_evidence, DROP COLUMN ai_disclosure;
ALTER TABLE demands DROP COLUMN allow_derivative_reuse;
