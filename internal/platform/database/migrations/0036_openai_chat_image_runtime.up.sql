UPDATE provider_profiles
SET model_name='gpt-5.6-terra',
    display_name='OpenAI Chat',
    description='Default-off OpenAI Responses API adapter. Cost is a Local Test reservation estimate, not a Provider invoice.',
    estimated_cost_cents=10,
    admin_enabled=false,
    updated_at=now()
WHERE id='openai-chat' AND mode='chat' AND provider='openai';

UPDATE provider_profiles
SET model_name='gpt-image-2',
    display_name='OpenAI Image',
    description='Default-off OpenAI Image Generation API adapter. Cost is a Local Test reservation estimate, not a Provider invoice.',
    estimated_cost_cents=25,
    admin_enabled=false,
    updated_at=now()
WHERE id='openai-image' AND mode='image' AND provider='openai';
