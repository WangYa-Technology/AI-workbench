-- Stop matching writers/workers before rollback. No financial evidence is
-- removed; the previous schema does not provide executable-job protection.
DROP TRIGGER seller_funding_execution_job_guard ON seller_payout_funding_dispatches;
DROP TRIGGER product_settlement_execution_job_guard ON product_settlement_dispatches;
DROP FUNCTION protect_transfer_execution_job();
DROP FUNCTION payment_transfer_job_executable(uuid,text,jsonb);
