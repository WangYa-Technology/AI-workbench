DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM risk_signals WHERE signal_type='account_link') THEN
    RAISE EXCEPTION 'cannot remove identity-link risk support while evidence exists';
  END IF;
END;
$$;

DROP INDEX IF EXISTS sessions_network_link_idx;

ALTER TABLE risk_rule_revisions
  DROP COLUMN account_link_window_hours,
  DROP COLUMN account_link_min_accounts,
  DROP COLUMN account_link_score;

ALTER TABLE risk_signals DROP CONSTRAINT risk_signals_signal_type_check;
ALTER TABLE risk_signals ADD CONSTRAINT risk_signals_signal_type_check
  CHECK (signal_type IN ('task_dispute','transaction_refund','community_report','media_rejection'));

ALTER TABLE risk_signals DROP CONSTRAINT risk_signals_resource_type_check;
ALTER TABLE risk_signals ADD CONSTRAINT risk_signals_resource_type_check
  CHECK (resource_type IN ('task','order','post','asset'));
