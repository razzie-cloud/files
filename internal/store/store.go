package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"hash/fnv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("configuration changed, name already exists, or backing prefixes overlap")

type Connection struct {
	ID, Owner, Name, Provider, Endpoint, Region, AccessMode, SecretRef string
	PathStyle                                                          bool
	Revision                                                           int64
}
type Directory struct {
	ID, Owner, ConnectionID, Name, Bucket, Prefix, EncryptionMode string
	FilenameEncryption, FilenameEncoding, Suffix, CryptSecretRef  string
	ReadOnly, DirectoryEncryption                                 bool
	Revision                                                      int64
}

func (d Directory) Writable(c Connection) bool { return c.AccessMode == "writable" && !d.ReadOnly }

type Repository interface {
	Ping(context.Context) error
	Connections(context.Context, string) ([]Connection, error)
	Connection(context.Context, string, string) (Connection, error)
	SaveConnection(context.Context, *Connection) error
	RemoveConnection(context.Context, string, string, int64) error
	Directories(context.Context, string) ([]Directory, error)
	Directory(context.Context, string, string) (Directory, error)
	SaveDirectory(context.Context, *Directory) error
	RemoveDirectory(context.Context, string, string, int64) error
	Lock(context.Context, string) (func(), error)
}
type Store struct{ DB *sql.DB }

func (s *Store) Ping(ctx context.Context) error { return s.DB.PingContext(ctx) }
func translate(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) && (pg.Code == "23505" || pg.Code == "23503") {
		return ErrConflict
	}
	return err
}

const connectionColumns = "id,owner_user_id,name,provider,endpoint,region,path_style,access_mode,secret_ref,revision"
const directoryColumns = "id,owner_user_id,connection_id,name,bucket,prefix,read_only,encryption_mode,filename_encryption,directory_encryption,filename_encoding,suffix,COALESCE(crypt_secret_ref,''),revision"

type scanner interface{ Scan(...any) error }

func scanConnection(row scanner) (c Connection, err error) {
	err = row.Scan(&c.ID, &c.Owner, &c.Name, &c.Provider, &c.Endpoint, &c.Region, &c.PathStyle, &c.AccessMode, &c.SecretRef, &c.Revision)
	return c, translate(err)
}
func scanDirectory(row scanner) (d Directory, err error) {
	err = row.Scan(&d.ID, &d.Owner, &d.ConnectionID, &d.Name, &d.Bucket, &d.Prefix, &d.ReadOnly, &d.EncryptionMode, &d.FilenameEncryption, &d.DirectoryEncryption, &d.FilenameEncoding, &d.Suffix, &d.CryptSecretRef, &d.Revision)
	return d, translate(err)
}
func (s *Store) Connections(ctx context.Context, owner string) ([]Connection, error) {
	rows, e := s.DB.QueryContext(ctx, "SELECT "+connectionColumns+" FROM storage_connections WHERE owner_user_id=$1 ORDER BY name", owner)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Connection{}
	for rows.Next() {
		c, e := scanConnection(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *Store) Connection(ctx context.Context, owner, id string) (Connection, error) {
	if _, e := uuid.Parse(id); e != nil {
		return Connection{}, ErrNotFound
	}
	return scanConnection(s.DB.QueryRowContext(ctx, "SELECT "+connectionColumns+" FROM storage_connections WHERE id=$1 AND owner_user_id=$2", id, owner))
}
func (s *Store) SaveConnection(ctx context.Context, c *Connection) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
		_, e := s.DB.ExecContext(ctx, `INSERT INTO storage_connections(id,owner_user_id,name,provider,endpoint,region,path_style,access_mode,secret_ref) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, c.ID, c.Owner, c.Name, c.Provider, c.Endpoint, c.Region, c.PathStyle, c.AccessMode, c.SecretRef)
		c.Revision = 1
		return translate(e)
	}
	res, e := s.DB.ExecContext(ctx, `UPDATE storage_connections SET name=$3,provider=$4,endpoint=$5,region=$6,path_style=$7,access_mode=$8,secret_ref=$9,revision=revision+1,updated_at=now() WHERE id=$1 AND owner_user_id=$2 AND revision=$10`, c.ID, c.Owner, c.Name, c.Provider, c.Endpoint, c.Region, c.PathStyle, c.AccessMode, c.SecretRef, c.Revision)
	if e != nil {
		return translate(e)
	}
	n, e := res.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrConflict
	}
	c.Revision++
	return nil
}
func remove(ctx context.Context, db *sql.DB, table, owner, id string, revision int64) error {
	if _, e := uuid.Parse(id); e != nil {
		return ErrNotFound
	}
	res, e := db.ExecContext(ctx, "DELETE FROM "+table+" WHERE id=$1 AND owner_user_id=$2 AND revision=$3", id, owner, revision)
	if e != nil {
		return translate(e)
	}
	n, e := res.RowsAffected()
	if e != nil {
		return e
	}
	if n == 0 {
		return ErrConflict
	}
	return nil
}
func (s *Store) RemoveConnection(ctx context.Context, owner, id string, revision int64) error {
	return remove(ctx, s.DB, "storage_connections", owner, id, revision)
}
func (s *Store) RemoveDirectory(ctx context.Context, owner, id string, revision int64) error {
	return remove(ctx, s.DB, "directories", owner, id, revision)
}
func (s *Store) Directories(ctx context.Context, owner string) ([]Directory, error) {
	rows, e := s.DB.QueryContext(ctx, "SELECT "+directoryColumns+" FROM directories WHERE owner_user_id=$1 ORDER BY name", owner)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Directory{}
	for rows.Next() {
		d, e := scanDirectory(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *Store) Directory(ctx context.Context, owner, id string) (Directory, error) {
	if _, e := uuid.Parse(id); e != nil {
		return Directory{}, ErrNotFound
	}
	return scanDirectory(s.DB.QueryRowContext(ctx, "SELECT "+directoryColumns+" FROM directories WHERE id=$1 AND owner_user_id=$2", id, owner))
}
func (s *Store) SaveDirectory(ctx context.Context, d *Directory) error {
	// A saved directory's physical location and crypt options are immutable.
	// Renaming the view and making it read-only never rewrite stored objects.
	if d.ID != "" {
		res, e := s.DB.ExecContext(ctx, `UPDATE directories SET name=$3,read_only=$4,revision=revision+1,updated_at=now() WHERE id=$1 AND owner_user_id=$2 AND revision=$5`, d.ID, d.Owner, d.Name, d.ReadOnly, d.Revision)
		if e != nil {
			return translate(e)
		}
		n, e := res.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return ErrConflict
		}
		d.Revision++
		return nil
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", lockID(d.ConnectionID+"/"+d.Bucket)); e != nil {
		return e
	}
	var owner string
	if e = tx.QueryRowContext(ctx, "SELECT owner_user_id FROM storage_connections WHERE id=$1 AND owner_user_id=$2 FOR KEY SHARE", d.ConnectionID, d.Owner).Scan(&owner); e != nil {
		return translate(e)
	}
	rows, e := tx.QueryContext(ctx, "SELECT prefix FROM directories WHERE connection_id=$1 AND bucket=$2 AND owner_user_id=$3", d.ConnectionID, d.Bucket, d.Owner)
	if e != nil {
		return e
	}
	overlap := false
	for rows.Next() {
		var prefix string
		if e = rows.Scan(&prefix); e != nil {
			rows.Close()
			return e
		}
		if prefix == d.Prefix || prefix == "" || d.Prefix == "" || strings.HasPrefix(prefix, d.Prefix+"/") || strings.HasPrefix(d.Prefix, prefix+"/") {
			overlap = true
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	if overlap {
		return ErrConflict
	}
	d.ID = uuid.NewString()
	_, e = tx.ExecContext(ctx, `INSERT INTO directories(id,owner_user_id,connection_id,name,bucket,prefix,read_only,encryption_mode,filename_encryption,directory_encryption,filename_encoding,suffix,crypt_secret_ref) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,NULLIF($13,''))`, d.ID, d.Owner, d.ConnectionID, d.Name, d.Bucket, d.Prefix, d.ReadOnly, d.EncryptionMode, d.FilenameEncryption, d.DirectoryEncryption, d.FilenameEncoding, d.Suffix, d.CryptSecretRef)
	if e != nil {
		return translate(e)
	}
	d.Revision = 1
	return tx.Commit()
}
func lockID(key string) int64 { h := fnv.New64a(); h.Write([]byte(key)); return int64(h.Sum64()) }
func (s *Store) Lock(ctx context.Context, key string) (func(), error) {
	conn, e := s.DB.Conn(ctx)
	if e != nil {
		return nil, e
	}
	if _, e = conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", lockID(key)); e != nil {
		// Cancellation can race with acquiring the lock. Never return a possibly
		// locked session to the pool; closing the physical connection releases it.
		conn.Raw(func(any) error { return driver.ErrBadConn })
		conn.Close()
		return nil, e
	}
	return func() { // Unlock even if the request context has been canceled.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", lockID(key))
		if err != nil {
			conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		conn.Close()
	}, nil
}
