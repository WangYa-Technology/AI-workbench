-- Never remove durable reversal commands or their financial exclusion.
LOCK TABLE seller_source_reversal_commands IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM seller_source_reversal_commands) THEN
  RAISE EXCEPTION 'source reversal commands must be retained' USING ERRCODE='55000';
 END IF;
END; $$;
CREATE OR REPLACE FUNCTION assert_seller_bank_payout_command(command seller_bank_payout_commands) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 PERFORM 1 FROM product_settlements s JOIN payment_intents p ON p.id=s.payment_id
 JOIN orders o ON o.id=s.order_id WHERE s.id=command.settlement_id FOR UPDATE OF p,o;
 PERFORM pg_advisory_xact_lock(hashtextextended(command.seller_id::text,0));
 PERFORM 1 FROM seller_payout_requests WHERE id=command.payout_request_id FOR UPDATE;
 PERFORM 1 FROM product_settlements WHERE id=command.settlement_id FOR UPDATE;
 PERFORM 1 FROM users WHERE id=command.seller_id FOR SHARE;
 PERFORM 1 FROM payment_destinations WHERE user_id=command.seller_id AND provider='stripe' FOR SHARE;
 PERFORM 1 FROM product_settlement_settings WHERE singleton=true FOR SHARE;
 PERFORM 1 FROM users u JOIN role_permissions rp ON rp.role=u.role
 WHERE u.id=command.actor_id AND u.status='active' AND rp.permission_id='admin:finance' FOR SHARE OF u,rp;
 IF NOT FOUND OR command.actor_id=command.seller_id THEN
  RAISE EXCEPTION 'bank payout requires current independent finance authority' USING ERRCODE='23514';
 END IF;
 IF NOT EXISTS (
  SELECT 1 FROM seller_payout_requests r
  JOIN seller_payout_transfers t ON t.payout_request_id=r.id
  JOIN seller_payout_funding_dispatches d ON d.transfer_id=t.id
  JOIN seller_payout_funding_admissions f ON f.transfer_id=t.id AND f.payout_request_id=r.id
  JOIN seller_payout_reviews v ON v.id=f.review_id AND v.payout_request_id=r.id
  JOIN seller_payout_bank_targets b ON b.payout_request_id=r.id AND b.seller_id=r.seller_id
  JOIN seller_payout_request_allocations a ON a.payout_request_id=r.id AND a.settlement_id=t.settlement_id
  JOIN product_settlements s ON s.id=t.settlement_id AND s.seller_id=r.seller_id
  JOIN payment_intents p ON p.id=s.payment_id JOIN orders o ON o.id=s.order_id
  JOIN product_sale_owners own ON own.order_id=o.id AND own.seller_id=r.seller_id::text
  JOIN product_checkout_requests original ON original.payment_id=p.id
  JOIN users seller ON seller.id=r.seller_id
  JOIN payment_destinations pd ON pd.user_id=r.seller_id AND pd.provider='stripe'
  WHERE r.id=command.payout_request_id AND r.seller_id=command.seller_id
  AND r.status IN ('processing','reconciliation_required') AND seller.status='active'
  AND t.id=command.source_transfer_id AND t.status='succeeded' AND t.provider='stripe'
  AND t.provider_transfer_id=command.source_provider_transfer_id AND d.started_at IS NOT NULL
  AND f.id=command.funding_admission_id AND v.id=command.review_id AND v.revision=command.review_revision
  AND v.decision='approved' AND v.actor_id<>r.seller_id
  AND NOT EXISTS(SELECT 1 FROM seller_payout_reviews later WHERE later.payout_request_id=r.id AND later.revision>v.revision)
  AND s.id=command.settlement_id AND p.id=command.payment_id AND p.purpose='product'
  AND s.status='available' AND s.available_at<=clock_timestamp() AND s.provider='stripe'
  AND p.status='paid' AND o.status='fulfilled' AND p.provider=s.provider AND p.live_mode=s.live_mode
  AND p.order_id=o.id AND p.resource_id=o.product_id AND p.payer_id=o.buyer_id AND p.payee_id=r.seller_id
  AND p.amount_cents=o.amount_cents AND p.amount_cents=s.gross_amount_cents AND p.currency=o.currency
  AND d.payment_id=p.id AND d.provider_charge_id=p.provider_charge_id
  AND a.released_at IS NULL AND a.amount_cents=command.amount_cents
  AND r.amount_cents=command.amount_cents AND t.amount_cents=command.amount_cents AND s.net_amount_cents=command.amount_cents
  AND b.amount_cents=command.amount_cents AND v.amount_cents=command.amount_cents
  AND b.settlement_id=s.id AND b.payment_id=p.id AND v.settlement_id=s.id
  AND r.currency=command.currency AND t.currency=command.currency AND s.currency=command.currency
  AND b.currency=command.currency AND v.currency=command.currency AND p.currency=command.currency
  AND t.provider_identity=command.provider_identity AND b.provider_identity=command.provider_identity
  AND original.identity=command.provider_identity AND command.provider_identity->>'provider'='stripe'
  AND command.provider_identity->'liveMode'=to_jsonb(s.live_mode) AND t.live_mode=s.live_mode
  AND t.destination_id=command.destination_id AND b.destination_id=command.destination_id
  AND b.bank_destination_id=command.bank_destination_id AND v.bank_destination_id=command.bank_destination_id
  AND pd.destination_id=command.destination_id AND pd.status='verified' AND pd.charges_enabled AND pd.payouts_enabled
  AND pd.original_merchant_id=command.provider_identity->>'merchantId'
  AND COALESCE(pd.original_store_id,'')=COALESCE(command.provider_identity->>'storeId','')
  AND pd.original_live_mode=s.live_mode AND pd.original_endpoint=command.provider_identity->>'endpoint'
  AND pd.original_api_version=command.provider_identity->>'apiVersion'
  AND pd.original_request_version=command.provider_identity->>'requestVersion'
  AND EXISTS(SELECT 1 FROM product_settlement_settings WHERE singleton=true AND payout_mode='seller_payout')
  AND NOT seller_funds_recovery_blocks(r.seller_id,s.id)
  AND NOT EXISTS(SELECT 1 FROM product_refund_review WHERE payment_id=p.id)
  AND NOT EXISTS(SELECT 1 FROM product_checkout_lookup_review WHERE payment_id=p.id)
  AND NOT EXISTS(SELECT 1 FROM product_settlement_dispatches WHERE settlement_id=s.id AND reserved_at IS NOT NULL)
  AND NOT EXISTS(SELECT 1 FROM seller_payout_transfers other WHERE other.payout_request_id=r.id AND other.id<>t.id)
  AND NOT EXISTS(SELECT 1 FROM seller_payout_funding_reads WHERE transfer_id=t.id AND (finished_at IS NULL OR requires_review))
 ) THEN
  RAISE EXCEPTION 'bank payout command conflicts with funded source, approval or reserved funds' USING ERRCODE='23514';
 END IF;
END;
$$;


DROP TRIGGER seller_bank_read_disposition ON seller_bank_payout_reads;
DROP TRIGGER seller_bank_result_disposition ON seller_bank_payout_results;
DROP FUNCTION serialize_seller_bank_observation();
DROP FUNCTION assert_seller_source_reversal_command(seller_source_reversal_commands);
DROP TABLE seller_source_reversal_commands;
DROP FUNCTION protect_seller_source_reversal_command();
DROP FUNCTION seller_source_reversal_bank_state(uuid);
DROP FUNCTION lock_seller_payout_disposition(uuid);
DROP TRIGGER seller_payout_disposition_insert ON seller_payout_requests;
DROP FUNCTION create_seller_payout_disposition_lock();
DROP TABLE seller_payout_disposition_locks;
LOCK TABLE jobs IN SHARE ROW EXCLUSIVE MODE;
CREATE OR REPLACE FUNCTION protect_payment_job_execution_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  PERFORM 1 FROM payment_job_execution_locks WHERE job_id=OLD.id FOR UPDATE;
  RETURN OLD;
 END IF;
 IF TG_OP='UPDATE' THEN
  IF ROW(NEW.kind,NEW.payload,NEW.status) IS NOT DISTINCT FROM ROW(OLD.kind,OLD.payload,OLD.status) THEN
   RETURN NEW;
  END IF;
  IF OLD.kind IN ('payment.settle_product','payment.fund_seller_payout','payment.execute_seller_bank_payout')
   OR NEW.kind IN ('payment.settle_product','payment.fund_seller_payout','payment.execute_seller_bank_payout') THEN
   -- Change the tuple as well as locking it. A stale Repeatable Read or
   -- Serializable sender must fail serialization instead of seeing a job
   -- snapshot from before a committed cancellation or payload change.
   INSERT INTO payment_job_execution_locks(job_id) VALUES(NEW.id)
    ON CONFLICT(job_id) DO UPDATE SET revision=payment_job_execution_locks.revision+1;
  END IF;
 ELSE
  IF NEW.kind IN ('payment.settle_product','payment.fund_seller_payout','payment.execute_seller_bank_payout') THEN
   INSERT INTO payment_job_execution_locks(job_id) VALUES(NEW.id);
  END IF;
 END IF;
 RETURN NEW;
END; $$;

CREATE OR REPLACE FUNCTION payment_execution_job_matches(execution_job uuid,expected_kind text,expected_payload jsonb)
RETURNS boolean LANGUAGE plpgsql AS $$
BEGIN
 IF current_setting('app.payment_execution_lock_protocol',true) IS DISTINCT FROM 'lock-v1'
 OR expected_kind NOT IN ('payment.settle_product','payment.fund_seller_payout','payment.execute_seller_bank_payout') THEN
  RETURN false;
 END IF;
 -- Lock before reading the job. Read Committed observes a preceding stop;
 -- stale Repeatable Read/Serializable snapshots fail on the revised tuple.
 PERFORM 1 FROM payment_job_execution_locks WHERE job_id=execution_job FOR SHARE;
 IF NOT FOUND THEN RETURN false; END IF;
 PERFORM 1 FROM jobs j WHERE j.id=execution_job AND j.kind=expected_kind
 AND j.payload=expected_payload AND j.status IN ('queued','running');
 RETURN FOUND;
END; $$;


DELETE FROM payment_job_execution_locks WHERE job_id IN (SELECT id FROM jobs WHERE kind='payment.reverse_seller_source');
