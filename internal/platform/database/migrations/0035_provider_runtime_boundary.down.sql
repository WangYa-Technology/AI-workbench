UPDATE jobs
SET kind='generation.local',updated_at=now()
WHERE kind='generation.generate';
