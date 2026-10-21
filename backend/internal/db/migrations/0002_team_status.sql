-- Teams can be archived (made inactive) instead of deleted

CREATE TYPE team_status AS ENUM ('active', 'inactive');
ALTER TABLE teams ADD COLUMN status team_status NOT NULL DEFAULT 'active';
