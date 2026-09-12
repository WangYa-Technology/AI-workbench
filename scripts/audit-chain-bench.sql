\set request_id random(1, 1000000000)
INSERT INTO audit_events(action, resource_type, request_id, metadata)
VALUES ('benchmark.event', 'benchmark', :request_id::text, '{}'::jsonb);
