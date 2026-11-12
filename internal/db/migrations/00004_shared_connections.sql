-- +goose Up
ALTER TABLE storage_connections ADD COLUMN shared boolean NOT NULL DEFAULT false;
ALTER TABLE storage_connections ADD COLUMN user_id_prefix boolean NOT NULL DEFAULT false;
ALTER TABLE storage_connections ADD CONSTRAINT connection_prefix_requires_shared CHECK (shared OR NOT user_id_prefix);
CREATE UNIQUE INDEX shared_connection_names ON storage_connections(name) WHERE shared;
ALTER TABLE directories DROP CONSTRAINT directories_connection_id_owner_user_id_fkey;
ALTER TABLE directories ADD CONSTRAINT directories_connection_fk FOREIGN KEY(connection_id) REFERENCES storage_connections(id);
CREATE INDEX directories_shared_connection ON directories(connection_id);

-- A mapping may reference its own personal connection or a shared connection.
-- Lock the policy row so mapping creation and location edits cannot race.
-- +goose StatementBegin
CREATE FUNCTION check_directories_connection() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE allowed boolean;
BEGIN
 SELECT (shared OR owner_user_id=NEW.owner_user_id) INTO allowed FROM storage_connections WHERE id=NEW.connection_id FOR SHARE;
 IF allowed IS DISTINCT FROM true THEN
  RAISE EXCEPTION 'connection not available' USING ERRCODE='23503';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER directories_connection_scope BEFORE INSERT OR UPDATE OF connection_id,owner_user_id ON directories FOR EACH ROW EXECUTE FUNCTION check_directories_connection();

-- Sharing is immutable. Prefix changes cannot redirect attached mappings.
-- +goose StatementBegin
CREATE FUNCTION check_shared_connection() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.shared<>OLD.shared OR NEW.owner_user_id<>OLD.owner_user_id THEN
  RAISE EXCEPTION 'connection ownership and sharing are immutable' USING ERRCODE='23514';
 END IF;
 IF NEW.user_id_prefix<>OLD.user_id_prefix AND EXISTS(SELECT 1 FROM directories WHERE connection_id=OLD.id) THEN
  RAISE EXCEPTION 'connection prefix is in use' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER shared_connection_policy BEFORE UPDATE ON storage_connections FOR EACH ROW EXECUTE FUNCTION check_shared_connection();

-- +goose Down
-- Refuse downgrade if it would change physical paths or break another user's mappings.
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS(SELECT 1 FROM directories m JOIN storage_connections c ON c.id=m.connection_id WHERE c.user_id_prefix) THEN
  RAISE EXCEPTION 'detach mappings with user ID prefixes before downgrading';
 END IF;
END;
$$;
-- +goose StatementEnd
ALTER TABLE directories ADD CONSTRAINT directories_connection_id_owner_user_id_fkey FOREIGN KEY(connection_id,owner_user_id) REFERENCES storage_connections(id,owner_user_id);
DROP TRIGGER shared_connection_policy ON storage_connections;
DROP FUNCTION check_shared_connection();
DROP TRIGGER directories_connection_scope ON directories;
DROP FUNCTION check_directories_connection();
ALTER TABLE directories DROP CONSTRAINT directories_connection_fk;
DROP INDEX directories_shared_connection;
DROP INDEX shared_connection_names;
ALTER TABLE storage_connections DROP CONSTRAINT connection_prefix_requires_shared;
ALTER TABLE storage_connections DROP COLUMN user_id_prefix;
ALTER TABLE storage_connections DROP COLUMN shared;
