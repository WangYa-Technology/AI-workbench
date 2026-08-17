ALTER TABLE generations ADD COLUMN output_text text;

INSERT INTO provider_profiles(id,mode,provider,model_name,display_name,description,estimated_cost_cents,currency,local_test,admin_enabled)
VALUES
  ('local-chat-v1','chat','local_test','hcai-local-chat-v1','Local Chat Test','Deterministic text response for conversation workflow verification.',2,'USD',true,true),
  ('local-video-v1','video','local_test','hcai-local-video-v1','Local Video Test','Deterministic project-owned video loop for workflow verification.',20,'USD',true,true),
  ('local-music-v1','music','local_test','hcai-local-music-v1','Local Music Test','Deterministic generated WAV tone study for workflow verification.',8,'USD',true,true)
ON CONFLICT (id) DO UPDATE SET
  description=excluded.description,estimated_cost_cents=excluded.estimated_cost_cents,local_test=true,admin_enabled=true,updated_at=now();
