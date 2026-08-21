ALTER TABLE provider_config_models
  ADD COLUMN capabilities jsonb NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE provider_profiles
  ADD COLUMN capabilities jsonb NOT NULL DEFAULT '{}'::jsonb;

UPDATE provider_profiles
SET capabilities = CASE mode
  WHEN 'chat' THEN '{"outputFormats":["txt"],"resultFormats":["txt"],"referenceKinds":["document"],"supportsMask":false}'::jsonb
  WHEN 'image' THEN '{"aspectRatios":["auto","1:1","4:5","16:9"],"qualities":["auto","standard","high"],"outputFormats":["jpeg","png"],"resultFormats":["jpeg","png"],"referenceKinds":["image"],"supportsMask":true}'::jsonb
  WHEN 'video' THEN '{"aspectRatios":["auto","1:1","4:5","16:9"],"qualities":["auto","standard","high"],"durationSeconds":[5,10,30],"outputFormats":["mp4"],"resultFormats":["mp4"],"referenceKinds":["image"],"supportsMask":false}'::jsonb
  WHEN 'music' THEN '{"qualities":["auto","standard","high"],"durationSeconds":[5,10,30,60],"outputFormats":["wav"],"resultFormats":["wav"],"referenceKinds":["audio"],"supportsMask":false}'::jsonb
  ELSE '{}'::jsonb
END
WHERE capabilities = '{}'::jsonb;
