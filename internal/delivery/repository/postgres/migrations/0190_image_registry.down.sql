DROP TABLE IF EXISTS image_blob_pushes;
ALTER TABLE media_uploads DROP COLUMN IF EXISTS purpose;
DROP TABLE IF EXISTS event_registry_secrets;
DROP TABLE IF EXISTS platform_images;
DROP TABLE IF EXISTS image_registry_settings;
