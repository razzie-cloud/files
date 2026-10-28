-- +goose Up
CREATE TABLE storage_connections (
    id uuid PRIMARY KEY,
    owner_user_id uuid NOT NULL,
    name text NOT NULL,
    provider text NOT NULL CHECK (provider IN ('AWS','Backblaze','Other')),
    endpoint text NOT NULL DEFAULT '',
    region text NOT NULL,
    path_style boolean NOT NULL,
    access_mode text NOT NULL CHECK (access_mode IN ('read_only','writable')),
    secret_ref text NOT NULL,
    revision bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (id,owner_user_id),
    UNIQUE (owner_user_id,name)
);
CREATE TABLE directories (
    id uuid PRIMARY KEY,
    owner_user_id uuid NOT NULL,
    connection_id uuid NOT NULL,
    name text NOT NULL,
    bucket text NOT NULL,
    prefix text NOT NULL DEFAULT '',
    read_only boolean NOT NULL,
    encryption_mode text NOT NULL CHECK (encryption_mode IN ('none','rclone_crypt')),
    filename_encryption text NOT NULL DEFAULT 'standard',
    directory_encryption boolean NOT NULL DEFAULT true,
    filename_encoding text NOT NULL DEFAULT 'base32',
    suffix text NOT NULL DEFAULT '.bin',
    crypt_secret_ref text NOT NULL DEFAULT '',
    revision bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (owner_user_id,name),
    FOREIGN KEY (connection_id,owner_user_id) REFERENCES storage_connections(id,owner_user_id)
);
CREATE INDEX directories_owner_connection ON directories(owner_user_id,connection_id);

-- +goose Down
DROP TABLE directories;
DROP TABLE storage_connections;
