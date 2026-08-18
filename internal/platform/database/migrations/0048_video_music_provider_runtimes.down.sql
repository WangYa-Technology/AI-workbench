UPDATE provider_profiles
SET provider='external',model_name='video-model',display_name='Video generation',
    description='No approved production provider is configured.',estimated_cost_cents=0,admin_enabled=false,updated_at=now()
WHERE id='video-provider' AND mode='video';

UPDATE provider_profiles
SET provider='external',model_name='music-model',display_name='Music generation',
    description='No approved production provider is configured.',estimated_cost_cents=0,admin_enabled=false,updated_at=now()
WHERE id='music-provider' AND mode='music';
