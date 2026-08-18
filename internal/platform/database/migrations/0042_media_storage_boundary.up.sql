ALTER TABLE assets
  ADD COLUMN storage_backend text,
  ADD COLUMN storage_key text;

UPDATE assets
SET storage_backend='local_file',
    storage_key=id::text || CASE mime_type
      WHEN 'image/jpeg' THEN '.jpg'
      WHEN 'image/png' THEN '.png'
      WHEN 'video/mp4' THEN '.mp4'
      WHEN 'audio/wav' THEN '.wav'
      WHEN 'text/plain' THEN '.txt'
      WHEN 'text/plain; charset=utf-8' THEN '.txt'
    END
WHERE source_type IN ('upload','generation');

ALTER TABLE assets
  ADD CONSTRAINT assets_storage_backend_check CHECK (storage_backend IS NULL OR storage_backend IN ('local_file','s3')),
  ADD CONSTRAINT assets_storage_evidence_check CHECK (
    (source_type IN ('upload','generation') AND storage_backend IS NOT NULL AND storage_key IS NOT NULL AND char_length(storage_key) BETWEEN 1 AND 1024) OR
    (source_type NOT IN ('upload','generation') AND storage_backend IS NULL AND storage_key IS NULL)
  );

CREATE UNIQUE INDEX assets_storage_object_unique
  ON assets(storage_backend,storage_key)
  WHERE storage_backend IS NOT NULL AND storage_key IS NOT NULL;
