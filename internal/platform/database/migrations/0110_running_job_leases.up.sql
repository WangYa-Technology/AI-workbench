CREATE INDEX jobs_running_lease_expiry_idx ON jobs(lease_expires_at,id) WHERE status='running';
