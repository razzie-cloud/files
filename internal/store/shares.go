package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"
)

// Shares are immutable capabilities bound to a directory ID, never a display
// name or physical S3 location. Changing scope/password requires a new link.
type Share struct {
	ID, Owner, DirectoryID, Path, PasswordHash string
	IsDirectory                                bool
	SessionKey                                 []byte
	CreatedAt                                  time.Time
	ExpiresAt                                  *time.Time
}

func (v Share) Expired(now time.Time) bool {
	return v.ExpiresAt != nil && !now.Before(*v.ExpiresAt)
}

const shareColumns = "id,owner_user_id,directory_id,path,is_directory,password_hash,session_key,created_at,expires_at"

func ValidShareID(id string) bool {
	if len(id) != 64 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func scanShare(row scanner) (v Share, err error) {
	err = row.Scan(&v.ID, &v.Owner, &v.DirectoryID, &v.Path, &v.IsDirectory, &v.PasswordHash, &v.SessionKey, &v.CreatedAt, &v.ExpiresAt)
	return v, translate(err)
}
func (s *Store) Shares(ctx context.Context, owner, directory string) ([]Share, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+shareColumns+" FROM shares WHERE owner_user_id=$1 AND directory_id=$2 ORDER BY created_at DESC", owner, directory)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Share{}
	for rows.Next() {
		v, err := scanShare(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// PublicShare is the only unscoped capability lookup. Its random ID authorizes
// discovery of the owner; all subsequent metadata/secret reads use that owner.
func (s *Store) PublicShare(ctx context.Context, id string) (Share, error) {
	if !ValidShareID(id) {
		return Share{}, ErrNotFound
	}
	return scanShare(s.DB.QueryRowContext(ctx, "SELECT "+shareColumns+" FROM shares WHERE id=$1 AND (expires_at IS NULL OR expires_at > now())", id))
}
func (s *Store) CreateShare(ctx context.Context, v *Share) error {
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return err
	}
	v.ID = hex.EncodeToString(token[:])
	v.SessionKey = make([]byte, 32)
	if _, err := rand.Read(v.SessionKey); err != nil {
		return err
	}
	return translate(s.DB.QueryRowContext(ctx, `INSERT INTO shares(id,owner_user_id,directory_id,path,is_directory,password_hash,session_key,expires_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING created_at`, v.ID, v.Owner, v.DirectoryID, v.Path, v.IsDirectory, v.PasswordHash, v.SessionKey, v.ExpiresAt).Scan(&v.CreatedAt))
}
func (s *Store) RemoveShare(ctx context.Context, owner, directory, id string) error {
	if !ValidShareID(id) {
		return ErrNotFound
	}
	res, err := s.DB.ExecContext(ctx, "DELETE FROM shares WHERE id=$1 AND owner_user_id=$2 AND directory_id=$3", id, owner, directory)
	if err != nil {
		return translate(err)
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}
