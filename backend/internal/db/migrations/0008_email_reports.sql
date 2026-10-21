-- Scheduled report emails (Emails page). Only the schedule is stored for now:
-- sending needs mail server settings, which are not set up yet.

CREATE TABLE email_reports (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name         text NOT NULL,
    report       text NOT NULL CHECK (report IN ('daily_summary', 'attendance', 'productivity', 'apps_websites')),
    frequency    text NOT NULL CHECK (frequency IN ('daily', 'weekly', 'monthly')),
    send_time    time NOT NULL,                     -- in time_zone
    weekday      smallint CHECK (weekday BETWEEN 0 AND 6),            -- weekly: 0 = Sunday
    month_day    smallint CHECK (month_day BETWEEN 1 AND 28),         -- monthly
    time_zone    text NOT NULL DEFAULT 'Asia/Kolkata',
    team_id      bigint REFERENCES teams (id) ON DELETE SET NULL,     -- NULL: everyone
    recipients   text[] NOT NULL,
    enabled      boolean NOT NULL DEFAULT true,
    created_by   bigint REFERENCES users (id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    last_sent_at timestamptz
);
