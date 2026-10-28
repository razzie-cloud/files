package storage

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/razzie-cloud/files/internal/secrets"
	"github.com/razzie-cloud/files/internal/store"
)

var ErrNotFound = errors.New("object not found")
var ErrTooLarge = errors.New("directory or upload exceeds configured limit")
var ErrInvalidPath = errors.New("invalid relative path")
var ErrConflict = errors.New("object exists or directory is not empty")
var ErrCrypto = errors.New("cannot decrypt this directory or file; check crypt settings")

type Entry struct {
	Name, Path string
	Size       int64
	Modified   time.Time
	Directory  bool
}
type Object struct {
	io.ReadCloser
	Size     int64
	Modified time.Time
}
type FS interface {
	List(context.Context, string) ([]Entry, error)
	Stat(context.Context, string) (Entry, error)
	Open(context.Context, string, int64, int64) (Object, error)
	Put(context.Context, string, io.Reader) error
	Mkdir(context.Context, string) error
	Remove(context.Context, string, bool) error
	PhysicalKey(string) string
	Close()
}
type Factory interface {
	Open(context.Context, store.Connection, store.Directory, secrets.Value, secrets.Value) (FS, error)
}

func Path(value string, allowRoot bool) (string, error) {
	if value == "" && allowRoot {
		return "", nil
	}
	if value == "" || !utf8.ValidString(value) || len(value) > 1024 || strings.ContainsAny(value, "\\\x00") || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") {
		return "", ErrInvalidPath
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return "", ErrInvalidPath
		}
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return "", ErrInvalidPath
		}
	}
	return value, nil
}
func Child(parent, name string) (string, error) {
	if _, e := Path(parent, true); e != nil {
		return "", e
	}
	if strings.Contains(name, "/") {
		return "", ErrInvalidPath
	}
	if _, e := Path(name, false); e != nil {
		return "", e
	}
	if parent == "" {
		return name, nil
	}
	return Path(parent+"/"+name, false)
}
