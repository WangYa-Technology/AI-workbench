UPDATE jobs
SET kind='generation.generate',updated_at=now()
WHERE kind='generation.local';
