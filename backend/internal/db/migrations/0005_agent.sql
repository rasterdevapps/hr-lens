-- Desktop agent: the computers that report activity, and the screenshots and
-- screen recordings they upload. Files live on disk under DATA_DIR; rows point to them.

CREATE TABLE devices (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id       bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name          text NOT NULL,                 -- computer name
    os            text NOT NULL DEFAULT '',
    agent_version text NOT NULL DEFAULT '',
    token_hash    bytea NOT NULL UNIQUE,         -- only a hash of the device token is kept
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_seen_at  timestamptz,
    revoked_at    timestamptz
);
CREATE INDEX devices_user_id_idx ON devices (user_id);

CREATE TYPE capture_kind AS ENUM ('screenshot', 'recording');

CREATE TABLE captures (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id      bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    device_id    bigint REFERENCES devices (id) ON DELETE SET NULL,
    kind         capture_kind NOT NULL,
    started_at   timestamptz NOT NULL,           -- when the screenshot was taken / recording began
    duration_sec integer NOT NULL DEFAULT 0,     -- recordings only
    path         text NOT NULL,                  -- relative to DATA_DIR
    bytes        bigint NOT NULL,
    content_type text NOT NULL,
    app          text NOT NULL DEFAULT '',       -- front application when taken
    title        text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX captures_user_time_idx ON captures (user_id, kind, started_at);
CREATE INDEX captures_created_idx ON captures (created_at);

-- Activity sent by an agent names its device; rows without one are seed sample data
ALTER TABLE activity_events ADD COLUMN device_id bigint REFERENCES devices (id) ON DELETE SET NULL;
