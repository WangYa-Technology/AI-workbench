DROP TRIGGER IF EXISTS generation_provider_usage_immutable ON generation_provider_usage;
DROP FUNCTION IF EXISTS reject_generation_provider_usage_mutation();
DROP INDEX IF EXISTS generation_provider_usage_provider_idx;
DROP TABLE IF EXISTS generation_provider_usage;
