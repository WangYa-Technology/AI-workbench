DELETE FROM role_permissions WHERE permission_id='admin:risk_rules';
DELETE FROM permissions WHERE id='admin:risk_rules';

DROP TRIGGER IF EXISTS risk_rule_revisions_immutable ON risk_rule_revisions;
DROP FUNCTION IF EXISTS reject_risk_rule_revision_mutation();
DROP TABLE IF EXISTS risk_rule_state;
DROP TABLE IF EXISTS risk_rule_revisions;
