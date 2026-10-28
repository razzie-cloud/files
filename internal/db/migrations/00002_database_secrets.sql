-- +goose Up
CREATE TABLE service_secrets (
    owner_user_id uuid NOT NULL,
    id text NOT NULL,
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (owner_user_id,id)
);

ALTER TABLE directories ALTER COLUMN crypt_secret_ref DROP NOT NULL;
ALTER TABLE directories ALTER COLUMN crypt_secret_ref DROP DEFAULT;
UPDATE directories SET crypt_secret_ref = NULL WHERE crypt_secret_ref = '';

-- Existing installations import their file-backed records before startup
-- validates these constraints. New writes are checked immediately.
ALTER TABLE storage_connections ADD CONSTRAINT storage_connections_secret_owner_fk
    FOREIGN KEY (owner_user_id,secret_ref) REFERENCES service_secrets(owner_user_id,id) NOT VALID;
ALTER TABLE directories ADD CONSTRAINT directories_secret_owner_fk
    FOREIGN KEY (owner_user_id,crypt_secret_ref) REFERENCES service_secrets(owner_user_id,id) NOT VALID;

-- +goose Down
-- Restore the legacy secret volume before running an older binary.
ALTER TABLE directories DROP CONSTRAINT directories_secret_owner_fk;
ALTER TABLE storage_connections DROP CONSTRAINT storage_connections_secret_owner_fk;
UPDATE directories SET crypt_secret_ref = '' WHERE crypt_secret_ref IS NULL;
ALTER TABLE directories ALTER COLUMN crypt_secret_ref SET DEFAULT '';
ALTER TABLE directories ALTER COLUMN crypt_secret_ref SET NOT NULL;
DROP TABLE service_secrets;
