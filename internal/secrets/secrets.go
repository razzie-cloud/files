// Package secrets stores immutable, owner-scoped credentials in PostgreSQL.
// It implements no application encryption or separately managed key dependency.
package secrets

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
)

type Value struct{ AccessKey, SecretKey, SessionToken, Password, Password2 string }
type Store struct{ db *sql.DB }

var ErrNotFound = errors.New("secret not found")
var ErrLegacySecrets = errors.New("database references file-backed secrets; restart once with IMPORT_SECRETS_DIR pointing to the old secrets volume")

func New(database *sql.DB) *Store { return &Store{db: database} }

func validOwner(owner string) bool {
	id, err := uuid.Parse(owner)
	return err == nil && id != uuid.Nil && id.String() == owner
}
func validRef(owner, ref string) bool {
	if !validOwner(owner) || !strings.HasPrefix(ref, owner+"-") {
		return false
	}
	id, err := uuid.Parse(strings.TrimPrefix(ref, owner+"-"))
	return err == nil && id.String() == strings.TrimPrefix(ref, owner+"-")
}
func (s *Store) Put(ctx context.Context, owner string, value Value) (string, error) {
	if !validOwner(owner) {
		return "", errors.New("invalid secret owner")
	}
	ref := owner + "-" + uuid.NewString()
	data, err := json.Marshal(value)
	if err != nil || len(data) > 65536 {
		return "", errors.New("invalid secret record")
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO service_secrets(owner_user_id,id,payload) VALUES($1,$2,$3)`, owner, ref, data)
	if err != nil {
		return "", errors.New("cannot store secret record")
	}
	return ref, nil
}
func (s *Store) Get(ctx context.Context, owner, ref string) (Value, error) {
	var value Value
	if !validRef(owner, ref) {
		return value, ErrNotFound
	}
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM service_secrets WHERE owner_user_id=$1 AND id=$2`, owner, ref).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return value, ErrNotFound
	}
	if err != nil {
		return value, errors.New("cannot read secret record")
	}
	if json.Unmarshal(data, &value) != nil {
		return value, errors.New("invalid secret record")
	}
	return value, nil
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func checkReferences(ctx context.Context, database queryer) error {
	var missing bool
	err := database.QueryRowContext(ctx, `SELECT EXISTS (
        SELECT 1 FROM storage_connections c LEFT JOIN service_secrets s
        ON s.owner_user_id=c.owner_user_id AND s.id=c.secret_ref WHERE s.id IS NULL
        UNION ALL
        SELECT 1 FROM directories d LEFT JOIN service_secrets s
        ON s.owner_user_id=d.owner_user_id AND s.id=d.crypt_secret_ref
        WHERE d.crypt_secret_ref IS NOT NULL AND s.id IS NULL
    )`).Scan(&missing)
	if err != nil {
		return errors.New("cannot check secret references")
	}
	if missing {
		return ErrLegacySecrets
	}
	return nil
}

// Ready fails closed on an upgraded installation until all legacy references
// have been imported. NOT VALID constraints let the schema migration precede
// that one-time import while still checking every new write immediately.
func (s *Store) Ready(ctx context.Context) error {
	if err := checkReferences(ctx, s.db); err != nil {
		return err
	}
	for _, table := range []string{"storage_connections", "directories"} {
		var validated bool
		if err := s.db.QueryRowContext(ctx, `SELECT convalidated FROM pg_constraint WHERE conrelid=$1::regclass AND conname=$2`, table, table+"_secret_owner_fk").Scan(&validated); err != nil {
			return errors.New("cannot inspect secret constraints")
		}
		if !validated {
			if _, err := s.db.ExecContext(ctx, "ALTER TABLE "+table+" VALIDATE CONSTRAINT "+table+"_secret_owner_fk"); err != nil {
				return errors.New("cannot validate secret references")
			}
		}
	}
	return nil
}
