-- +goose Up
-- M5-S3：內容倉庫（HREP-R1～R10），同 sqlite/migrations/00004_repository.sql。
-- provenance_json 用 TEXT 而不是 JSONB：JSONB 會改寫 key 的順序與空白，讀回來的位元組要和寫入的相同。

CREATE TABLE videos (
    id                  TEXT COLLATE "C" PRIMARY KEY,
    platform            TEXT COLLATE "C" NOT NULL CHECK (platform IN ('youtube', 'bilibili', 'podcast')),
    external_id         TEXT COLLATE "C" NOT NULL,
    part                INTEGER NOT NULL DEFAULT 0,
    title               TEXT NOT NULL DEFAULT '',
    channel_external_id TEXT COLLATE "C" NOT NULL DEFAULT '',
    channel_title       TEXT NOT NULL DEFAULT '',
    duration_s          DOUBLE PRECISION,
    original_lang       TEXT NOT NULL DEFAULT '',
    published_at        TEXT NOT NULL DEFAULT '',
    key_sha256          TEXT COLLATE "C" NOT NULL,
    first_shared_at     TIMESTAMPTZ NOT NULL,
    CONSTRAINT videos_key UNIQUE (platform, external_id, part)
);

CREATE INDEX videos_key_sha256 ON videos (key_sha256);
CREATE INDEX videos_channel ON videos (platform, channel_external_id);

CREATE TABLE blobs (
    sha256     TEXT COLLATE "C" PRIMARY KEY,
    size       BIGINT NOT NULL,
    media_type TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE versions (
    id               TEXT COLLATE "C" PRIMARY KEY,
    video_id         TEXT COLLATE "C" NOT NULL REFERENCES videos (id),
    kind             TEXT COLLATE "C" NOT NULL CHECK (kind IN ('transcript', 'note', 'chapters', 'frames')),
    variant          TEXT COLLATE "C" NOT NULL DEFAULT '',
    lang             TEXT COLLATE "C" NOT NULL DEFAULT '',
    format           TEXT NOT NULL,
    sha256           TEXT COLLATE "C" NOT NULL REFERENCES blobs (sha256),
    size             BIGINT NOT NULL,
    provenance_json  TEXT NOT NULL DEFAULT '{}',
    transcribe_model TEXT COLLATE "C" NOT NULL DEFAULT '',
    correct_model    TEXT COLLATE "C" NOT NULL DEFAULT '',
    translate_model  TEXT COLLATE "C" NOT NULL DEFAULT '',
    model_verified   BOOLEAN NOT NULL,
    visibility       TEXT COLLATE "C" NOT NULL,
    license          TEXT NOT NULL,
    state            TEXT COLLATE "C" NOT NULL DEFAULT 'visible' CHECK (state IN ('visible', 'hidden', 'removed', 'withdrawn')),
    score            INTEGER NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL,
    CONSTRAINT versions_key UNIQUE (video_id, kind, variant, lang, sha256)
);

CREATE INDEX versions_sha ON versions (sha256);

CREATE TABLE version_blobs (
    version_id TEXT COLLATE "C" NOT NULL REFERENCES versions (id),
    sha256     TEXT COLLATE "C" NOT NULL REFERENCES blobs (sha256),
    role       TEXT NOT NULL CHECK (role IN ('content', 'image')),
    PRIMARY KEY (version_id, sha256, role)
);

CREATE INDEX version_blobs_sha ON version_blobs (sha256);

CREATE TABLE shares (
    id            TEXT COLLATE "C" PRIMARY KEY,
    user_id       TEXT COLLATE "C" NOT NULL REFERENCES users (id),
    video_id      TEXT COLLATE "C" NOT NULL REFERENCES videos (id),
    license       TEXT NOT NULL,
    attested_at   TIMESTAMPTZ NOT NULL,
    bundle_sha256 TEXT COLLATE "C" NOT NULL,
    bundle_size   BIGINT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL,
    withdrawn_at  TIMESTAMPTZ
);

CREATE INDEX shares_user ON shares (user_id, created_at);

CREATE TABLE share_versions (
    share_id     TEXT COLLATE "C" NOT NULL REFERENCES shares (id),
    version_id   TEXT COLLATE "C" NOT NULL REFERENCES versions (id),
    position     INTEGER NOT NULL,
    deduplicated BOOLEAN NOT NULL,
    PRIMARY KEY (share_id, version_id)
);

CREATE INDEX share_versions_version ON share_versions (version_id);

CREATE TABLE derivations (
    child_version_id  TEXT COLLATE "C" NOT NULL REFERENCES versions (id),
    parent_version_id TEXT COLLATE "C" NOT NULL REFERENCES versions (id),
    relation          TEXT COLLATE "C" NOT NULL CHECK (relation IN ('parent', 'regenerated_from')),
    PRIMARY KEY (child_version_id, parent_version_id, relation)
);

CREATE INDEX derivations_parent ON derivations (parent_version_id);

CREATE TABLE removed_hashes (
    video_id    TEXT COLLATE "C" NOT NULL REFERENCES videos (id),
    sha256      TEXT COLLATE "C" NOT NULL,
    takedown_id TEXT NOT NULL DEFAULT '',
    removed_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (video_id, sha256)
);

-- +goose Down
DROP TABLE removed_hashes;
DROP TABLE derivations;
DROP TABLE share_versions;
DROP TABLE shares;
DROP TABLE version_blobs;
DROP TABLE versions;
DROP TABLE blobs;
DROP TABLE videos;
