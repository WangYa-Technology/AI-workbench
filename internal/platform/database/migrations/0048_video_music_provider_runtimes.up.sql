UPDATE provider_profiles
SET provider='byteplus_video',
    model_name='dreamina-seedance-2-0-fast-260128',
    display_name='BytePlus Seedance Video',
    description='Default-off BytePlus ModelArk Seedance video adapter. Cost is a Local Test reservation estimate, not a Provider invoice.',
    estimated_cost_cents=80,
    admin_enabled=false,
    updated_at=now()
WHERE id='video-provider' AND mode='video';

UPDATE provider_profiles
SET provider='minimax_music',
    model_name='music-3.0',
    display_name='MiniMax Music',
    description='Default-off MiniMax Music generation adapter. Activation requires an eligible existing Provider account and explicit rights approval.',
    estimated_cost_cents=20,
    admin_enabled=false,
    updated_at=now()
WHERE id='music-provider' AND mode='music';
