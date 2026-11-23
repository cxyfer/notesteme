-- +goose Up
-- M5-S3：內容倉庫（HREP-R1～R10，ADR-0031 第 3、5 點）。時間欄位是 Unix 毫秒，取自注入的 clock；ID 是 UUIDv7。

-- videos 是影片：(platform, external_id, part) 識別，沒有分P時 part 為 0（本機檔案不能分享）。
-- key_sha256 是 sha256("<platform>:<external_id>:<part>")（沒有分P時 part 是空字串，ADR-0032 第 3 點），給 ASR 前查詢。
CREATE TABLE videos (
    id                  TEXT PRIMARY KEY,
    platform            TEXT NOT NULL CHECK (platform IN ('youtube', 'bilibili', 'podcast')),
    external_id         TEXT NOT NULL,
    part                INTEGER NOT NULL DEFAULT 0,
    title               TEXT NOT NULL DEFAULT '',
    channel_external_id TEXT NOT NULL DEFAULT '',
    channel_title       TEXT NOT NULL DEFAULT '',
    duration_s          REAL,
    original_lang       TEXT NOT NULL DEFAULT '',
    published_at        TEXT NOT NULL DEFAULT '',
    key_sha256          TEXT NOT NULL,
    first_shared_at     INTEGER NOT NULL,
    UNIQUE (platform, external_id, part)
) STRICT;

CREATE INDEX videos_key ON videos (key_sha256);
CREATE INDEX videos_channel ON videos (platform, channel_external_id);

-- blobs 是 hubblob 中的檔案（以 sha256 為鍵，多個版本共用）。
CREATE TABLE blobs (
    sha256     TEXT PRIMARY KEY,
    size       INTEGER NOT NULL,
    media_type TEXT NOT NULL,
    created_at INTEGER NOT NULL
) STRICT;

-- versions 是一份產出內容；鍵是 (video_id, kind, variant, lang, sha256)，相同的鍵只存一份（HREP-R5）。
-- provenance_json 只有白名單欄位（HREP-R4）；三個模型欄位與 model_verified 在上傳時決定（HREP-R7）。
-- visibility：public、authenticated、private、group:<name>（HACC-R14）。score 是投票的總和（S5）。
CREATE TABLE versions (
    id               TEXT PRIMARY KEY,
    video_id         TEXT NOT NULL REFERENCES videos (id),
    kind             TEXT NOT NULL CHECK (kind IN ('transcript', 'note', 'chapters', 'frames')),
    variant          TEXT NOT NULL DEFAULT '',
    lang             TEXT NOT NULL DEFAULT '',
    format           TEXT NOT NULL,
    sha256           TEXT NOT NULL REFERENCES blobs (sha256),
    size             INTEGER NOT NULL,
    provenance_json  TEXT NOT NULL DEFAULT '{}',
    transcribe_model TEXT NOT NULL DEFAULT '',
    correct_model    TEXT NOT NULL DEFAULT '',
    translate_model  TEXT NOT NULL DEFAULT '',
    model_verified   INTEGER NOT NULL,
    visibility       TEXT NOT NULL,
    license          TEXT NOT NULL,
    state            TEXT NOT NULL DEFAULT 'visible' CHECK (state IN ('visible', 'hidden', 'removed', 'withdrawn')),
    score            INTEGER NOT NULL DEFAULT 0,
    created_at       INTEGER NOT NULL,
    UNIQUE (video_id, kind, variant, lang, sha256)
) STRICT;

CREATE INDEX versions_sha ON versions (sha256);

-- version_blobs 是版本引用的 blob：content 是內容本身，image 是畫面或筆記的圖片。
CREATE TABLE version_blobs (
    version_id TEXT NOT NULL REFERENCES versions (id),
    sha256     TEXT NOT NULL REFERENCES blobs (sha256),
    role       TEXT NOT NULL CHECK (role IN ('content', 'image')),
    PRIMARY KEY (version_id, sha256, role)
) STRICT;

CREATE INDEX version_blobs_sha ON version_blobs (sha256);

-- shares 是一次上傳（HREP-R1）；撤回時 withdrawn_at 有值（HREP-R10）。bundle_size 用來計算每日上傳量（HREP-R11）。
CREATE TABLE shares (
    id            TEXT PRIMARY KEY,
    user_id       TEXT NOT NULL REFERENCES users (id),
    video_id      TEXT NOT NULL REFERENCES videos (id),
    license       TEXT NOT NULL,
    attested_at   INTEGER NOT NULL,
    bundle_sha256 TEXT NOT NULL,
    bundle_size   INTEGER NOT NULL,
    created_at    INTEGER NOT NULL,
    withdrawn_at  INTEGER
) STRICT;

CREATE INDEX shares_user ON shares (user_id, created_at);

-- share_versions 是分享包含的版本，position 是 manifest 中的順序；deduplicated 表示分享時版本已經存在。
CREATE TABLE share_versions (
    share_id     TEXT NOT NULL REFERENCES shares (id),
    version_id   TEXT NOT NULL REFERENCES versions (id),
    position     INTEGER NOT NULL,
    deduplicated INTEGER NOT NULL,
    PRIMARY KEY (share_id, version_id)
) STRICT;

CREATE INDEX share_versions_version ON share_versions (version_id);

-- derivations 是衍生關係（HREP-R6）：parent（parents_sha256）或 regenerated_from（hub_origin.version_id）。
CREATE TABLE derivations (
    child_version_id  TEXT NOT NULL REFERENCES versions (id),
    parent_version_id TEXT NOT NULL REFERENCES versions (id),
    relation          TEXT NOT NULL CHECK (relation IN ('parent', 'regenerated_from')),
    PRIMARY KEY (child_version_id, parent_version_id, relation)
) STRICT;

CREATE INDEX derivations_parent ON derivations (parent_version_id);

-- removed_hashes 是依下架申請移除的內容（HREP-R9、HACC-R16），之後相同的內容不能再上傳到同一部影片。
CREATE TABLE removed_hashes (
    video_id    TEXT NOT NULL REFERENCES videos (id),
    sha256      TEXT NOT NULL,
    takedown_id TEXT NOT NULL DEFAULT '',
    removed_at  INTEGER NOT NULL,
    PRIMARY KEY (video_id, sha256)
) STRICT;

-- +goose Down
DROP TABLE removed_hashes;
DROP TABLE derivations;
DROP TABLE share_versions;
DROP TABLE shares;
DROP TABLE version_blobs;
DROP TABLE versions;
DROP TABLE blobs;
DROP TABLE videos;
