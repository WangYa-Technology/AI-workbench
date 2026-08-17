DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM risk_signals WHERE signal_type IN ('community_report','media_rejection')) THEN
    RAISE EXCEPTION '0030 cannot be rolled back after governance risk evidence exists';
  END IF;
END;
$$;

ALTER TABLE risk_signals DROP CONSTRAINT risk_signals_signal_type_check;
ALTER TABLE risk_signals ADD CONSTRAINT risk_signals_signal_type_check
  CHECK (signal_type IN ('task_dispute','transaction_refund'));

ALTER TABLE risk_signals DROP CONSTRAINT risk_signals_resource_type_check;
ALTER TABLE risk_signals ADD CONSTRAINT risk_signals_resource_type_check
  CHECK (resource_type IN ('task','order'));

ALTER TABLE risk_rule_revisions
  DROP COLUMN media_rejection_score,
  DROP COLUMN community_report_score;
