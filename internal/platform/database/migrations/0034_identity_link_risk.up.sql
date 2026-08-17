ALTER TABLE risk_signals DROP CONSTRAINT risk_signals_resource_type_check;
ALTER TABLE risk_signals ADD CONSTRAINT risk_signals_resource_type_check
  CHECK (resource_type IN ('task','order','post','asset','user'));

ALTER TABLE risk_signals DROP CONSTRAINT risk_signals_signal_type_check;
ALTER TABLE risk_signals ADD CONSTRAINT risk_signals_signal_type_check
  CHECK (signal_type IN ('task_dispute','transaction_refund','community_report','media_rejection','account_link'));

ALTER TABLE risk_rule_revisions
  ADD COLUMN account_link_score integer NOT NULL DEFAULT 65 CHECK (account_link_score BETWEEN 0 AND 100),
  ADD COLUMN account_link_min_accounts integer NOT NULL DEFAULT 3 CHECK (account_link_min_accounts BETWEEN 2 AND 20),
  ADD COLUMN account_link_window_hours integer NOT NULL DEFAULT 24 CHECK (account_link_window_hours BETWEEN 1 AND 168);

CREATE INDEX sessions_network_link_idx
  ON sessions(network_hash,created_at,user_id)
  WHERE network_hash IS NOT NULL;
