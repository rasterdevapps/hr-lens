-- When people sign in to, lock, unlock and sign out of their computer (sent by the agent).
-- The Time Tracker's start time is the day's first sign-in or unlock.

CREATE TYPE presence_kind AS ENUM ('login', 'unlock', 'lock', 'logout');

CREATE TABLE presence_events (
    user_id   bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    device_id bigint NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    kind      presence_kind NOT NULL,
    at        timestamptz NOT NULL,
    PRIMARY KEY (device_id, kind, at)     -- the same sign-in reported again is ignored
);
CREATE INDEX presence_events_user_time_idx ON presence_events (user_id, at);
