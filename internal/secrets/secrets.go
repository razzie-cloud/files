// Package secrets stores immutable, owner-scoped secrets on a protected volume.
// It deliberately implements no encryption; deployment owns encryption at rest.
package secrets

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/google/uuid"
)

type Value struct{ AccessKey, SecretKey, SessionToken, Password, Password2 string }
type Store struct{ root *os.Root }

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("secret directory must be a real directory")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &Store{root}, nil
}
func (s *Store) Close() error { return s.root.Close() }
func validOwner(owner string) bool {
	id, e := uuid.Parse(owner)
	return e == nil && id != uuid.Nil && id.String() == owner
}
func validRef(owner, ref string) bool {
	if !validOwner(owner) || !strings.HasPrefix(ref, owner+"-") {
		return false
	}
	id, e := uuid.Parse(strings.TrimPrefix(ref, owner+"-"))
	return e == nil && id.String() == strings.TrimPrefix(ref, owner+"-")
}
func (s *Store) Put(owner string, value Value) (string, error) {
	if !validOwner(owner) {
		return "", errors.New("invalid secret owner")
	}
	ref := owner + "-" + uuid.NewString()
	data, e := json.Marshal(value)
	if e != nil {
		return "", e
	}
	f, e := s.root.OpenFile(ref, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return "", e
	}
	_, e = f.Write(data)
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		s.root.Remove(ref)
		return "", e
	}
	directory, err := s.root.Open(".")
	if err != nil {
		return "", err
	}
	err = directory.Sync()
	directory.Close()
	if err != nil {
		return "", err
	}
	return ref, nil
}
func (s *Store) Get(owner, ref string) (Value, error) {
	var value Value
	if !validRef(owner, ref) {
		return value, errors.New("invalid secret reference")
	}
	f, e := s.root.Open(ref)
	if e != nil {
		return value, e
	}
	defer f.Close()
	data, e := io.ReadAll(io.LimitReader(f, 65537))
	if e != nil || len(data) > 65536 {
		return value, errors.New("invalid secret record")
	}
	e = json.Unmarshal(data, &value)
	return value, e
}
