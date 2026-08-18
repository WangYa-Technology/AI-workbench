UPDATE provider_profiles
SET model_name='gpt-chat',
    description='Production adapter requires approved credentials and paid-call authorization.',
    estimated_cost_cents=0,
    admin_enabled=false,
    updated_at=now()
WHERE id='openai-chat' AND mode='chat' AND provider='openai';

UPDATE provider_profiles
SET model_name='gpt-image',
    description='Production adapter requires approved credentials and paid-call authorization.',
    estimated_cost_cents=0,
    admin_enabled=false,
    updated_at=now()
WHERE id='openai-image' AND mode='image' AND provider='openai';
