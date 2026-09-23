-- Worker reconciliation reads due holds without rewriting historical evidence
-- during deployment. State changes and deletion scheduling happen atomically.
CREATE INDEX data_rights_hold_expiry ON data_rights_legal_holds(expires_at,id)
 WHERE status IN ('active','expired');
CREATE INDEX data_rights_hold_subject_history ON data_rights_legal_holds(user_id,expires_at,id);
