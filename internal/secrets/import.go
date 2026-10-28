package secrets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
)

// Import copies all records, including retained recovery records, in one
// transaction. It never rewrites or removes the legacy files and is idempotent.
func (s *Store) Import(ctx context.Context, dir string) error {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("legacy secret directory is unavailable or not a real directory")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return errors.New("cannot open legacy secret directory")
	}
	defer root.Close()
	directory, err := root.Open(".")
	if err != nil {
		return errors.New("cannot read legacy secret directory")
	}
	entries, err := directory.ReadDir(-1)
	directory.Close()
	if err != nil {
		return errors.New("cannot list legacy secrets")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("cannot begin secret import")
	}
	defer tx.Rollback()
	for _, entry := range entries {
		ref := entry.Name()
		if len(ref) != 73 || ref[36] != '-' || !validRef(ref[:36], ref) {
			return errors.New("legacy directory contains an unrecognized secret record")
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("legacy secret record must be a regular file")
		}
		file, err := root.Open(ref)
		if err != nil {
			return errors.New("cannot open legacy secret record")
		}
		data, err := io.ReadAll(io.LimitReader(file, 65537))
		file.Close()
		if err != nil || len(data) > 65536 {
			return errors.New("cannot read legacy secret record")
		}
		var value Value
		if !bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) || json.Unmarshal(data, &value) != nil {
			return errors.New("invalid legacy secret record")
		}
		canonical, err := json.Marshal(value)
		if err != nil {
			return errors.New("invalid legacy secret record")
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO service_secrets(owner_user_id,id,payload) VALUES($1,$2,$3) ON CONFLICT (owner_user_id,id) DO NOTHING`, ref[:36], ref, canonical)
		if err != nil {
			return errors.New("cannot import secret record")
		}
		count, err := result.RowsAffected()
		if err != nil {
			return errors.New("cannot confirm secret import")
		}
		if count == 0 {
			var old []byte
			if tx.QueryRowContext(ctx, `SELECT payload FROM service_secrets WHERE owner_user_id=$1 AND id=$2`, ref[:36], ref).Scan(&old) != nil {
				return errors.New("cannot check existing secret record")
			}
			var existing Value
			if json.Unmarshal(old, &existing) != nil || existing != value {
				return errors.New("legacy secret conflicts with an existing database record; nothing was imported")
			}
		}
	}
	if err = checkReferences(ctx, tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return errors.New("cannot commit secret import")
	}
	return nil
}
