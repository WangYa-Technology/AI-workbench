-- Bounded operator evidence inventory walks original orders, including rows
-- without contracts. It must not sort or join the entire historical catalogue.
CREATE INDEX orders_delivery_evidence_created_idx ON orders(created_at DESC,id DESC);
