DROP TRIGGER IF EXISTS provider_cost_reconciliations_protected ON provider_cost_reconciliations;
DROP FUNCTION IF EXISTS protect_provider_cost_reconciliation();
DROP TABLE IF EXISTS provider_cost_reconciliations;
