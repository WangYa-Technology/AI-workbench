DELETE FROM role_permissions WHERE permission_id='admin:ranking';
DELETE FROM permissions WHERE id='admin:ranking';

DROP TRIGGER IF EXISTS discovery_ranking_revisions_immutable ON discovery_ranking_revisions;
DROP FUNCTION IF EXISTS reject_discovery_ranking_revision_mutation();
DROP TABLE IF EXISTS discovery_ranking_state;
DROP TABLE IF EXISTS discovery_ranking_revisions;
