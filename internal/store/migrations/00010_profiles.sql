-- +goose Up
-- Processing profiles (PRF). name is the identifier used in URLs and in items.profile_id.
-- current_version points at the latest row in profile_versions; every save adds a version (PRF-R3).
CREATE TABLE profiles (
    name            TEXT PRIMARY KEY,
    current_version INTEGER NOT NULL,
    builtin         INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
) STRICT;

-- Immutable versions. config_json is the profile layer as saved (no defaults merged in);
-- sha256 is over config_json.
CREATE TABLE profile_versions (
    name        TEXT NOT NULL REFERENCES profiles (name) ON DELETE CASCADE,
    version     INTEGER NOT NULL,
    config_json TEXT NOT NULL,
    sha256      TEXT NOT NULL,
    created_at  INTEGER NOT NULL,
    PRIMARY KEY (name, version)
) STRICT;

-- The built-in default profile is an empty layer, so items ingested with it get exactly the
-- pre-PRF effective config.
INSERT INTO profiles (name, current_version, builtin, created_at, updated_at) VALUES ('default', 1, 1, 0, 0);
INSERT INTO profile_versions (name, version, config_json, sha256, created_at)
VALUES ('default', 1, '{}', '44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a', 0);

-- Channel-level overrides (PRF-R8), merged between the profile and the item overrides.
ALTER TABLE channels ADD COLUMN overrides_json TEXT NOT NULL DEFAULT '{}';
-- Version of items.profile_id used by the latest ingest or rerun (PRF-R3). NULL for pre-PRF items.
ALTER TABLE items ADD COLUMN profile_version INTEGER;
-- The layers a snapshot was composed from (global, profile, channel, item, run). '{}' for
-- snapshots written before PRF; those are never re-bound.
ALTER TABLE config_snapshots ADD COLUMN layers_json TEXT NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE config_snapshots DROP COLUMN layers_json;
ALTER TABLE items DROP COLUMN profile_version;
ALTER TABLE channels DROP COLUMN overrides_json;
DROP TABLE profile_versions;
DROP TABLE profiles;
