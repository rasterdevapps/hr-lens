-- Work shifts (Administration > Shifts). A person signing in after start + grace is marked Late.

CREATE TABLE shifts (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name          text NOT NULL,
    start_minute  int NOT NULL CHECK (start_minute BETWEEN 0 AND 1439),   -- minutes after midnight, in the person's day
    grace_minutes int NOT NULL DEFAULT 0 CHECK (grace_minutes BETWEEN 0 AND 240),
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX shifts_name_key ON shifts (lower(name));

-- Without a shift the default applies (see defaultShift in internal/api/shifts.go)
ALTER TABLE users ADD COLUMN shift_id bigint REFERENCES shifts (id) ON DELETE SET NULL;
CREATE INDEX users_shift_id_idx ON users (shift_id);
