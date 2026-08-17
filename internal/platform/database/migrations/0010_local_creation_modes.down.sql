DELETE FROM provider_profiles WHERE id IN ('local-chat-v1','local-video-v1','local-music-v1');
ALTER TABLE generations DROP COLUMN IF EXISTS output_text;
