-- Employee profile (Profile page opened from the user menu): personal, contact, job and
-- address details beyond the Users form, a photo, and documents filed in three folders.

CREATE TABLE user_profiles (
    user_id    bigint PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    details    jsonb NOT NULL DEFAULT '{}'::jsonb,   -- see profileDetails in internal/api/profile.go
    photo_path text,                                 -- under DATA_DIR, NULL when none
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE user_documents (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id      bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    folder       text NOT NULL CHECK (folder IN ('degree', 'experience', 'identity')),
    details      jsonb NOT NULL DEFAULT '{}'::jsonb, -- the folder's fields
    file_name    text,                               -- attached file, NULL when none
    file_path    text,                               -- under DATA_DIR
    content_type text,
    size         bigint,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX user_documents_user_id_idx ON user_documents (user_id, folder, id);
