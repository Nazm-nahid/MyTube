CREATE TABLE IF NOT EXISTS videos (
    id           UUID PRIMARY KEY,
    title        TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    filename     TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size_bytes   BIGINT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Columns left over from when the service stored the video file.
ALTER TABLE videos
    DROP COLUMN IF EXISTS s3_key,
    DROP COLUMN IF EXISTS object_key,
    DROP COLUMN IF EXISTS status,
    DROP COLUMN IF EXISTS uploaded_at;
