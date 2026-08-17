DELETE FROM role_permissions WHERE permission_id IN ('account:data-rights','admin:data-rights');
DELETE FROM permissions WHERE id IN ('account:data-rights','admin:data-rights');

DROP TRIGGER IF EXISTS data_rights_receipts_immutable ON data_rights_deletion_receipts;
DROP TRIGGER IF EXISTS data_rights_artifacts_immutable ON data_rights_export_artifacts;
DROP TRIGGER IF EXISTS data_rights_events_immutable ON data_rights_events;
DROP FUNCTION IF EXISTS reject_data_rights_evidence_mutation();
DROP TABLE IF EXISTS data_rights_legal_holds;
DROP TABLE IF EXISTS data_rights_deletion_receipts;
DROP TABLE IF EXISTS data_rights_export_artifacts;
DROP TABLE IF EXISTS data_rights_events;
DROP TABLE IF EXISTS data_rights_requests;
