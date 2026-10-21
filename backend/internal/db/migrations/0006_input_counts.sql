-- Activity Levels: how many keys were pressed and mouse buttons clicked, per person per minute.
-- Only counts are kept: never which keys.

CREATE TABLE input_counts (
    user_id   bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    device_id bigint NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    minute    timestamptz NOT NULL,          -- start of the minute
    keys      integer NOT NULL DEFAULT 0 CHECK (keys >= 0),
    clicks    integer NOT NULL DEFAULT 0 CHECK (clicks >= 0),
    PRIMARY KEY (user_id, device_id, minute)
);
