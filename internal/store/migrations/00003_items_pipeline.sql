-- +goose Up
-- 影音、產出、步驟與任務佇列（plan 第 21 節）。時間欄位都是 Unix 毫秒，取自注入的 clock；ID 是 UUIDv7。

CREATE TABLE channels (
    id            TEXT PRIMARY KEY,
    platform      TEXT NOT NULL,
    external_id   TEXT NOT NULL,
    title         TEXT NOT NULL DEFAULT '',
    url           TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    updated_at    INTEGER NOT NULL,
    UNIQUE (platform, external_id)
) STRICT;

-- config_snapshots 存四層覆寫合併後的生效設定；相同內容（sha256）只存一份。
CREATE TABLE config_snapshots (
    id          TEXT PRIMARY KEY,
    sha256      TEXT NOT NULL UNIQUE,
    config_json TEXT NOT NULL,
    created_at  INTEGER NOT NULL
) STRICT;

-- part：Bilibili 的分P 從 1 起算，其他平台為 0（UNIQUE 不能含 NULL）。
-- until_stage：單次新增指定「做到哪一步為止」，空字串代表全部。
CREATE TABLE items (
    id             TEXT PRIMARY KEY,
    platform       TEXT NOT NULL,
    external_id    TEXT NOT NULL,
    part           INTEGER NOT NULL DEFAULT 0,
    channel_id     TEXT REFERENCES channels (id),
    profile_id     TEXT,
    overrides_json TEXT NOT NULL DEFAULT '{}',
    until_stage    TEXT NOT NULL DEFAULT '',
    title          TEXT NOT NULL DEFAULT '',
    url            TEXT NOT NULL,
    published_at   INTEGER,
    duration_s     INTEGER,
    original_lang  TEXT,
    lang_source    TEXT,
    lang_mixed     INTEGER NOT NULL DEFAULT 0,
    availability   TEXT,
    media_kind     TEXT,
    metadata_json  TEXT NOT NULL DEFAULT '{}',
    created_at     INTEGER NOT NULL,
    deleted_at     INTEGER,
    UNIQUE (platform, external_id, part)
) STRICT;

CREATE TABLE artifacts (
    id              TEXT PRIMARY KEY,
    item_id         TEXT NOT NULL REFERENCES items (id) ON DELETE CASCADE,
    kind            TEXT NOT NULL,
    variant         TEXT NOT NULL DEFAULT '',
    lang            TEXT NOT NULL DEFAULT '',
    format          TEXT NOT NULL,
    path            TEXT NOT NULL, -- 相對於資料目錄，例如 artifacts/<item-id>/<artifact-id>.json
    sha256          TEXT NOT NULL,
    size            INTEGER NOT NULL,
    parents_json    TEXT NOT NULL DEFAULT '[]',
    provenance_json TEXT NOT NULL,
    superseded_by   TEXT,
    pinned          INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL
) STRICT;

CREATE INDEX artifacts_item ON artifacts (item_id, kind, variant, lang);

-- actions_json 是修正動作（例如「上傳 cookie」）；error_message 同時是 waiting／blocked 時給人看的訊息。
CREATE TABLE stage_runs (
    id                 TEXT PRIMARY KEY,
    item_id            TEXT NOT NULL REFERENCES items (id) ON DELETE CASCADE,
    stage              TEXT NOT NULL,
    lang               TEXT NOT NULL DEFAULT '',
    variant            TEXT NOT NULL DEFAULT '',
    status             TEXT NOT NULL,
    block_reason       TEXT NOT NULL DEFAULT '',
    error_class        TEXT NOT NULL DEFAULT '',
    error_message      TEXT NOT NULL DEFAULT '',
    actions_json       TEXT NOT NULL DEFAULT '[]',
    executor           TEXT NOT NULL DEFAULT '',
    cache_key          TEXT NOT NULL DEFAULT '',
    impl_version       INTEGER NOT NULL DEFAULT 0,
    config_snapshot_id TEXT NOT NULL REFERENCES config_snapshots (id),
    artifact_id        TEXT REFERENCES artifacts (id),
    not_before         INTEGER,
    started_at         INTEGER,
    finished_at        INTEGER,
    created_at         INTEGER NOT NULL
) STRICT;

CREATE INDEX stage_runs_item ON stage_runs (item_id, stage);
CREATE INDEX stage_runs_cache ON stage_runs (item_id, stage, cache_key);

CREATE TABLE tasks (
    id             TEXT PRIMARY KEY,
    stage_run_id   TEXT NOT NULL REFERENCES stage_runs (id) ON DELETE CASCADE,
    kind           TEXT NOT NULL,
    executor       TEXT NOT NULL,
    resource_class TEXT NOT NULL,
    state          TEXT NOT NULL,
    priority       INTEGER NOT NULL,
    payload_json   TEXT NOT NULL DEFAULT '{}',
    chunk_index    INTEGER,
    result_path    TEXT,
    lease_token    TEXT,
    lease_owner    TEXT,
    lease_until    INTEGER,
    attempts       INTEGER NOT NULL DEFAULT 0,
    max_attempts   INTEGER NOT NULL DEFAULT 3,
    not_before     INTEGER,
    deadline_at    INTEGER,
    created_at     INTEGER NOT NULL
) STRICT;

CREATE INDEX tasks_claim ON tasks (state, executor, not_before);
CREATE INDEX tasks_stage_run ON tasks (stage_run_id);

-- source_probes：每次 Probe 的結果（SRC-R17）。run_id 把同一次 Probe 的列放在一起。
-- state：used | rejected | unavailable；reason 說明為什麼沒有採用。
CREATE TABLE source_probes (
    id              TEXT PRIMARY KEY,
    item_id         TEXT NOT NULL REFERENCES items (id) ON DELETE CASCADE,
    run_id          TEXT NOT NULL,
    source_id       TEXT NOT NULL,
    lang            TEXT NOT NULL DEFAULT '',
    kind            TEXT NOT NULL DEFAULT '',
    available       INTEGER NOT NULL,
    est_cost        REAL,
    state           TEXT NOT NULL,
    rejected_reason TEXT NOT NULL DEFAULT '',
    probed_at       INTEGER NOT NULL
) STRICT;

CREATE INDEX source_probes_item ON source_probes (item_id);

-- +goose Down
DROP TABLE source_probes;
DROP TABLE tasks;
DROP TABLE stage_runs;
DROP TABLE artifacts;
DROP TABLE items;
DROP TABLE config_snapshots;
DROP TABLE channels;
