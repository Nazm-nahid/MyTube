CREATE TABLE IF NOT EXISTS videos (
    id           UUID PRIMARY KEY,
    title        TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    filename     TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size_bytes   BIGINT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS users (
    id         UUID PRIMARY KEY,
    google_sub TEXT NOT NULL UNIQUE,
    email      TEXT NOT NULL,
    name       TEXT NOT NULL,
    picture    TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sessions (
    token_hash BYTEA PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL
);

-- Empty for videos uploaded before sign-in existed.
ALTER TABLE videos ADD COLUMN IF NOT EXISTS user_id UUID REFERENCES users (id);

-- Columns left over from when the service stored the video file.
ALTER TABLE videos
    DROP COLUMN IF EXISTS s3_key,
    DROP COLUMN IF EXISTS object_key,
    DROP COLUMN IF EXISTS status,
    DROP COLUMN IF EXISTS uploaded_at;
