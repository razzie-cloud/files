-- +goose Up
ALTER TABLE directories ADD CONSTRAINT directories_id_owner_unique UNIQUE (id, owner_user_id);
CREATE TABLE shares (
    id text PRIMARY KEY CHECK (id ~ '^[0-9a-f]{64}$'),
    owner_user_id uuid NOT NULL,
    directory_id uuid NOT NULL,
    path text NOT NULL DEFAULT '',
    is_directory boolean NOT NULL,
    password_hash text NOT NULL DEFAULT '',
    session_key bytea NOT NULL CHECK (octet_length(session_key) = 32),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz,
    FOREIGN KEY (directory_id, owner_user_id) REFERENCES directories(id, owner_user_id) ON DELETE CASCADE,
    CHECK (is_directory OR path <> '')
);
CREATE INDEX shares_owner_directory ON shares(owner_user_id, directory_id);

-- +goose Down
DROP TABLE shares;
ALTER TABLE directories DROP CONSTRAINT directories_id_owner_unique;
