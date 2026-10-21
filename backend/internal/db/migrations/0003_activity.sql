-- Activity: what each person had in front of them, and for how long.
-- One row is one unbroken stretch in one application window (and website, for browsers).
-- The desktop agent will send these; until then `go run ./cmd/seed` fills sample rows.

CREATE TYPE activity_productivity AS ENUM ('productive', 'neutral', 'unproductive');

CREATE TABLE activity_events (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id      bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    started_at   timestamptz NOT NULL,
    duration_sec integer NOT NULL CHECK (duration_sec > 0),
    app          text NOT NULL,                 -- process name, e.g. chrome.exe
    app_name     text NOT NULL,                 -- friendly name, e.g. Google Chrome
    title        text NOT NULL DEFAULT '',      -- window title
    domain       text,                          -- website, for browser windows only
    category     text NOT NULL DEFAULT 'Uncategorized',
    productivity activity_productivity NOT NULL DEFAULT 'neutral'
);
CREATE INDEX activity_events_user_time_idx ON activity_events (user_id, started_at);
