UPDATE provider_config_models
SET model_name=model_name||'-archived-'||left(id::text,8), updated_at=now()
WHERE model_name='gpt-image-2' AND mode='image' AND archived_at IS NOT NULL;

UPDATE provider_config_models m
SET mode='image',
    capabilities='{"aspectRatios":["auto","1:1","4:5","16:9"],"qualities":["auto","standard","high"],"outputFormats":["jpeg","png"],"resultFormats":["jpeg","png"],"referenceKinds":[],"supportsMask":false}'::jsonb,
    updated_at=now()
FROM provider_configs c
WHERE c.id=m.provider_id
  AND c.protocol='hctopup_async_image'
  AND m.model_name='gpt-image-2'
  AND m.mode='chat';

UPDATE provider_profiles p
SET mode='image',
    capabilities='{"aspectRatios":["auto","1:1","4:5","16:9"],"qualities":["auto","standard","high"],"outputFormats":["jpeg","png"],"resultFormats":["jpeg","png"],"referenceKinds":[],"supportsMask":false}'::jsonb,
    updated_at=now()
WHERE p.model_name='gpt-image-2'
  AND p.provider='openai'
  AND p.mode='chat'
  AND EXISTS (SELECT 1 FROM provider_config_models m WHERE m.id::text=p.id AND m.mode='image');
