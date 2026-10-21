-- Productivity rules: how each application and website is rated (Productivity > Configure Apps).
-- A browser window is rated by its website's rule; an application rule covers windows with no website.
-- Changing a rule re-rates the matching activity_events rows.

CREATE TYPE activity_rule_kind AS ENUM ('application', 'website');

CREATE TABLE activity_rules (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind         activity_rule_kind NOT NULL,
    name         text NOT NULL,                -- app_name for applications, domain for websites
    category     text NOT NULL DEFAULT 'Uncategorized',
    productivity activity_productivity NOT NULL DEFAULT 'neutral',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    updated_by   bigint REFERENCES users (id) ON DELETE SET NULL
);
CREATE UNIQUE INDEX activity_rules_name_key ON activity_rules (kind, lower(name));

-- Finding the events a rule covers
CREATE INDEX activity_events_domain_idx ON activity_events (lower(domain), started_at) WHERE domain IS NOT NULL;
CREATE INDEX activity_events_app_idx ON activity_events (lower(app_name), started_at);
