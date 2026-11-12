-- +goose Up
ALTER TABLE storage_connections ALTER COLUMN path_style SET DEFAULT false;
ALTER TABLE storage_connections ADD COLUMN bucket text NOT NULL DEFAULT '';

-- Mapping creation holds the connection row lock through its transaction.
-- +goose StatementBegin
CREATE FUNCTION check_directories_bucket() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE fixed_bucket text;
BEGIN
 SELECT bucket INTO fixed_bucket FROM storage_connections WHERE id=NEW.connection_id FOR SHARE;
 IF fixed_bucket<>'' AND NEW.bucket<>fixed_bucket THEN
  RAISE EXCEPTION 'bucket is fixed by the connection' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER directories_fixed_bucket BEFORE INSERT OR UPDATE OF connection_id,bucket ON directories FOR EACH ROW EXECUTE FUNCTION check_directories_bucket();

-- Existing mappings retain their physical buckets. Restricting a connection
-- is allowed only when every user's existing mappings are compatible.
-- +goose StatementBegin
CREATE FUNCTION check_connection_bucket() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.bucket<>'' AND EXISTS(SELECT 1 FROM directories WHERE connection_id=OLD.id AND bucket<>NEW.bucket) THEN
  RAISE EXCEPTION 'existing mappings use another bucket' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER connection_fixed_bucket BEFORE UPDATE OF bucket ON storage_connections FOR EACH ROW EXECUTE FUNCTION check_connection_bucket();

-- +goose Down
ALTER TABLE storage_connections ALTER COLUMN path_style DROP DEFAULT;
DROP TRIGGER connection_fixed_bucket ON storage_connections;
DROP FUNCTION check_connection_bucket();
DROP TRIGGER directories_fixed_bucket ON directories;
DROP FUNCTION check_directories_bucket();
ALTER TABLE storage_connections DROP COLUMN bucket;
