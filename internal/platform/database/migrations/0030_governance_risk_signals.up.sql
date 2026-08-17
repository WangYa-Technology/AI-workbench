ALTER TABLE risk_signals DROP CONSTRAINT risk_signals_resource_type_check;
ALTER TABLE risk_signals ADD CONSTRAINT risk_signals_resource_type_check
  CHECK (resource_type IN ('task','order','post','asset'));

ALTER TABLE risk_signals DROP CONSTRAINT risk_signals_signal_type_check;
ALTER TABLE risk_signals ADD CONSTRAINT risk_signals_signal_type_check
  CHECK (signal_type IN ('task_dispute','transaction_refund','community_report','media_rejection'));

ALTER TABLE risk_rule_revisions
  ADD COLUMN community_report_score integer NOT NULL DEFAULT 35 CHECK (community_report_score BETWEEN 0 AND 100),
  ADD COLUMN media_rejection_score integer NOT NULL DEFAULT 75 CHECK (media_rejection_score BETWEEN 0 AND 100);

WITH active_rule AS (
  SELECT r.id,r.version,r.community_report_score AS score,r.medium_threshold,r.high_threshold,r.critical_threshold
  FROM risk_rule_state s JOIN risk_rule_revisions r ON r.id=s.active_revision_id WHERE s.singleton=true
), migrated AS (
  INSERT INTO risk_signals(source_key,resource_type,resource_id,subject_user_id,actor_user_id,signal_type,severity,score,summary,evidence,detected_at,updated_at)
  SELECT 'community_report:'||cr.id::text,'post',cr.resource_id,cr.subject_author_id,cr.reporter_id,'community_report',
         CASE WHEN ar.score>=ar.critical_threshold THEN 'critical' WHEN ar.score>=ar.high_threshold THEN 'high'
              WHEN ar.score>=ar.medium_threshold THEN 'medium' ELSE 'low' END,
         ar.score,'Community report requires risk review.',
         jsonb_build_object('reportId',cr.id,'category',cr.category,'riskRuleRevisionId',ar.id,'riskRuleVersion',ar.version),
         cr.created_at,cr.created_at
  FROM content_reports cr CROSS JOIN active_rule ar
  ON CONFLICT (source_key) DO NOTHING
  RETURNING id,actor_user_id,detected_at
)
INSERT INTO risk_events(signal_id,actor_id,kind,to_status,reason,created_at)
SELECT id,actor_user_id,'detected','open','Existing Community report migrated into the risk review queue.',detected_at FROM migrated;

WITH active_rule AS (
  SELECT r.id,r.version,r.media_rejection_score AS score,r.medium_threshold,r.high_threshold,r.critical_threshold
  FROM risk_rule_state s JOIN risk_rule_revisions r ON r.id=s.active_revision_id WHERE s.singleton=true
), migrated AS (
  INSERT INTO risk_signals(source_key,resource_type,resource_id,subject_user_id,actor_user_id,signal_type,severity,score,summary,evidence,detected_at,updated_at)
  SELECT 'media_rejection:'||a.id::text,'asset',a.id,a.owner_id,NULL,'media_rejection',
         CASE WHEN ar.score>=ar.critical_threshold THEN 'critical' WHEN ar.score>=ar.high_threshold THEN 'high'
              WHEN ar.score>=ar.medium_threshold THEN 'medium' ELSE 'low' END,
         ar.score,'Rejected uploaded media requires risk review.',
         jsonb_build_object('assetKind',a.kind,'mimeType',a.mime_type,'riskRuleRevisionId',ar.id,'riskRuleVersion',ar.version),
         COALESCE(a.scanned_at,a.created_at),COALESCE(a.scanned_at,a.created_at)
  FROM assets a CROSS JOIN active_rule ar WHERE a.source_type='upload' AND a.scan_status='rejected'
  ON CONFLICT (source_key) DO NOTHING
  RETURNING id,actor_user_id,detected_at
)
INSERT INTO risk_events(signal_id,actor_id,kind,to_status,reason,created_at)
SELECT id,actor_user_id,'detected','open','Existing rejected upload migrated into the risk review queue.',detected_at FROM migrated;
