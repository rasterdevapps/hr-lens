-- Stage 1: people, roles and sign-in

CREATE TYPE user_role AS ENUM ('super_admin', 'manager', 'user');
CREATE TYPE user_status AS ENUM ('active', 'inactive');

CREATE TABLE branches (
    id   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name text NOT NULL
);
CREATE UNIQUE INDEX branches_name_key ON branches (lower(name));

CREATE TABLE departments (
    id   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name text NOT NULL
);
CREATE UNIQUE INDEX departments_name_key ON departments (lower(name));

CREATE TABLE teams (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX teams_name_key ON teams (lower(name));

CREATE TABLE users (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    employee_code    text NOT NULL,                 -- the "Id" shown on the Users page
    first_name       text NOT NULL,
    last_name        text NOT NULL,
    email            text NOT NULL,                 -- "Username or Email"
    mobile           text NOT NULL DEFAULT '',
    password_hash    text NOT NULL,
    role             user_role NOT NULL DEFAULT 'user',
    status           user_status NOT NULL DEFAULT 'active',
    team_id          bigint REFERENCES teams (id) ON DELETE SET NULL,
    department_id    bigint REFERENCES departments (id) ON DELETE SET NULL,
    branch_id        bigint REFERENCES branches (id) ON DELETE SET NULL,
    manager_id       bigint REFERENCES users (id) ON DELETE SET NULL,
    time_zone        text NOT NULL DEFAULT 'Asia/Kolkata',
    track_attendance boolean NOT NULL DEFAULT true,
    last_active_at   timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    CHECK (manager_id IS NULL OR manager_id <> id)
);
CREATE UNIQUE INDEX users_email_key ON users (lower(email));
CREATE UNIQUE INDEX users_employee_code_key ON users (lower(employee_code));
CREATE INDEX users_team_id_idx ON users (team_id);
CREATE INDEX users_manager_id_idx ON users (manager_id);

-- Teams a user administers ("Admin Groups")
CREATE TABLE user_admin_groups (
    user_id bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    team_id bigint NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, team_id)
);

-- Sign-in sessions. Only a hash of the token is stored.
CREATE TABLE sessions (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    token_hash   bytea NOT NULL UNIQUE,
    user_id      bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    user_agent   text NOT NULL DEFAULT '',
    ip           text NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);
