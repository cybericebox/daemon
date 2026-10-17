-- The platform image registry (settings in «Панель платформи»): one settings row
-- holding the provider, account, repository and the sealed push/pull tokens.
-- Tokens are stored sealed only (PLATFORM_SECRETS_KEY) and never read back.
CREATE TABLE image_registry_settings (
    id                    boolean     PRIMARY KEY DEFAULT true CHECK (id),
    provider              text        NOT NULL,
    account               text        NOT NULL,
    repository            text        NOT NULL,
    push_token_ciphertext text        NOT NULL,
    pull_token_ciphertext text        NOT NULL DEFAULT '',
    updated_at            timestamptz NOT NULL
);

-- Images uploaded to the platform registry: one row per pushed tag, kept for
-- the tag garbage collector (a tag older than 24 h no exercise version uses).
-- A tag carries the whole digest, so it determines its digest: UNIQUE (tag)
-- refuses a row that would re-point another digest's tag.
CREATE TABLE platform_images (
    digest      text        NOT NULL,
    tag         text        NOT NULL,
    name        text        NOT NULL,
    exercise_id uuid        NULL REFERENCES exercises (id) ON DELETE SET NULL,
    uploaded_by uuid        NULL,
    uploaded_at timestamptz NOT NULL,
    PRIMARY KEY (digest, tag),
    CONSTRAINT platform_images_tag_key UNIQUE (tag)
);

-- Registry credentials an organizer adds for an event's private images,
-- matched by registry host and an optional repository prefix.
CREATE TABLE event_registry_secrets (
    id                uuid        PRIMARY KEY,
    event_id          uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    host              text        NOT NULL,
    repository_prefix text        NOT NULL DEFAULT '',
    username          text        NOT NULL,
    token_ciphertext  text        NOT NULL,
    created_at        timestamptz NOT NULL,
    updated_at        timestamptz NOT NULL,
    UNIQUE (event_id, host, repository_prefix)
);

-- What a chunked upload is for: '' an ordinary file, 'image-blob' one blob of a container image on its way to
-- the platform registry (it never becomes a media file; a background job streams its chunks into the registry).
ALTER TABLE media_uploads
    ADD COLUMN purpose text NOT NULL DEFAULT '';

-- The background push of one image blob from its chunked upload: 'pushing' while queued or running, 'failed'
-- with a reason (unauthorized, unreachable, digest_mismatch, ...). A successful push removes the row.
CREATE TABLE image_blob_pushes (
    digest     text        PRIMARY KEY,
    state      text        NOT NULL CHECK (state IN ('pushing', 'failed')),
    reason     text        NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL
);
