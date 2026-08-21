UPDATE provider_config_models m
SET mode='chat', updated_at=now()
FROM provider_configs c
WHERE c.id=m.provider_id AND c.protocol='hctopup_async_image' AND m.model_name='gpt-image-2' AND m.mode='image';
