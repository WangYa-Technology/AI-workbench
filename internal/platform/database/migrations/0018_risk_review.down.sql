DELETE FROM role_permissions WHERE permission_id='admin:risk';
DELETE FROM permissions WHERE id='admin:risk';

DROP TRIGGER IF EXISTS risk_events_immutable ON risk_events;
DROP FUNCTION IF EXISTS reject_risk_event_mutation();
DROP TABLE IF EXISTS risk_events;
DROP TABLE IF EXISTS risk_signals;
