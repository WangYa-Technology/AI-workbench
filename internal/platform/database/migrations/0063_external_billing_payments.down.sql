ALTER TABLE billing_entries DROP CONSTRAINT IF EXISTS billing_entries_entry_type_check;
ALTER TABLE billing_entries ADD CONSTRAINT billing_entries_entry_type_check CHECK (entry_type IN (
  'generation_charge','product_purchase','product_sale','product_refund',
  'task_payment','task_earning','admin_adjustment','initial_credit','subscription_purchase'
));
ALTER TABLE payment_provider_events DROP CONSTRAINT IF EXISTS payment_provider_events_purpose_check;
ALTER TABLE payment_provider_events ADD CONSTRAINT payment_provider_events_purpose_check CHECK (purpose IS NULL OR purpose IN ('product','task'));
ALTER TABLE payment_intents DROP CONSTRAINT IF EXISTS payment_intents_purpose_check;
ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_purpose_check CHECK (purpose IN ('product','task'));
ALTER TABLE payment_intents DROP CONSTRAINT IF EXISTS payment_intents_resource_relation_check;
ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_resource_relation_check CHECK (
  (purpose='product' AND order_id IS NOT NULL AND proposal_id IS NULL AND payee_id IS NOT NULL) OR
  (purpose='task' AND order_id IS NULL)
);
