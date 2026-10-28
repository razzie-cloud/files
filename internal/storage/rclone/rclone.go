package rclone

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/razzie-cloud/files/internal/secrets"
	"github.com/razzie-cloud/files/internal/storage"
	"github.com/razzie-cloud/files/internal/store"
	"github.com/rclone/rclone/backend/crypt"
	"github.com/rclone/rclone/backend/s3"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/cache"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/config/obscure"
	"github.com/rclone/rclone/fs/object"
)

var initOnce sync.Once
var initError error

func Initialize() error {
	initOnce.Do(func() {
		_, initError = obscure.Obscure("startup")
		// Keep raw upstream errors and remote paths out of service logs.
		fs.SetLogger(slog.NewTextHandler(io.Discard, nil))
	})
	return initError
}

type Factory struct {
	ProxyURL   string
	Policy     *Policy
	MaxEntries int
}

func defaults(backend string, overrides configmap.Simple) (configmap.Simple, error) {
	info, e := fs.Find(backend)
	if e != nil {
		return nil, e
	}
	m := configmap.Simple{}
	for _, opt := range info.Options {
		m[opt.Name] = fmt.Sprint(opt.Default)
	}
	for key, value := range overrides {
		m[key] = value
	}
	return m, nil
}
func (f *Factory) Open(ctx context.Context, c store.Connection, d store.Directory, credentials, keys secrets.Value) (storage.FS, error) {
	if e := Initialize(); e != nil {
		return nil, e
	}
	if c.Endpoint != "" {
		if e := f.Policy.Endpoint(c.Endpoint); e != nil {
			return nil, e
		}
	} else if c.Provider != "AWS" {
		return nil, errors.New("S3 endpoint required")
	}
	if credentials.AccessKey == "" || credentials.SecretKey == "" {
		return nil, errors.New("S3 credentials required")
	}
	ctx, ci := fs.AddConfig(ctx)
	ci.HTTPProxy = f.ProxyURL
	ci.ConnectTimeout = fs.Duration(10 * time.Second)
	ci.Timeout = fs.Duration(60 * time.Second)
	ci.LowLevelRetries = 3
	ci.Retries = 1
	// Handles are request-scoped. Avoid leaving an inaccessible idle transport
	// pool behind each constructor (the S3 client has no exported Close method).
	ci.DisableHTTPKeepAlives = true
	name := "files-" + uuid.NewString()
	provider := c.Provider
	if provider == "Backblaze" {
		provider = "Other"
	}
	m, e := defaults("s3", configmap.Simple{"provider": provider, "endpoint": c.Endpoint, "region": c.Region, "access_key_id": credentials.AccessKey, "secret_access_key": credentials.SecretKey, "session_token": credentials.SessionToken, "env_auth": "false", "force_path_style": strconv.FormatBool(c.PathStyle), "no_check_bucket": "true", "directory_markers": "true", "chunk_size": "8Mi", "upload_concurrency": "2", "leave_parts_on_error": "false"})
	if e != nil {
		return nil, e
	}
	root := d.Bucket
	if d.Prefix != "" {
		root += "/" + d.Prefix
	}
	// This is explicitly a directory. A trailing slash disables rclone's
	// file-root inference, which otherwise may return the parent filesystem.
	root += "/"
	underlying, e := s3.NewFs(ctx, name, root, m)
	if e != nil {
		return nil, fmt.Errorf("initialize storage client: %w", e)
	}
	return wrap(ctx, name, underlying, d, keys, f.MaxEntries)
}

// wrap uses rclone's exported cache API because crypt has no direct fs.Fs constructor.
// Each handle has an unguessable name, and construction is pinned against expiry.
func wrap(ctx context.Context, name string, underlying fs.Fs, d store.Directory, keys secrets.Value, limit int) (storage.FS, error) {
	result := &handle{fs: underlying, limit: limit, root: d.Prefix}
	if d.EncryptionMode == "rclone_crypt" {
		alias := name + ":" + underlying.Root()
		cache.Put(alias, underlying)
		cache.Pin(underlying)
		defer cache.Unpin(underlying)
		password, e := obscure.Obscure(keys.Password)
		if e != nil {
			return nil, e
		}
		salt := ""
		if keys.Password2 != "" {
			salt, e = obscure.Obscure(keys.Password2)
			if e != nil {
				return nil, e
			}
		}
		if keys.Password == "" {
			return nil, errors.New("crypt password required")
		}
		m, e := defaults("crypt", configmap.Simple{"remote": alias, "password": password, "password2": salt, "filename_encryption": d.FilenameEncryption, "directory_name_encryption": strconv.FormatBool(d.DirectoryEncryption), "filename_encoding": d.FilenameEncoding, "suffix": d.Suffix, "no_data_encryption": "false", "strict_names": "true", "pass_bad_blocks": "false", "show_mapping": "false"})
		if e != nil {
			return nil, e
		}
		wrapped, e := crypt.NewFs(ctx, name+"-crypt", "", m)
		if e != nil {
			cache.ClearConfig(name)
			return nil, storage.ErrCrypto
		}
		result.fs = wrapped
		result.crypt = wrapped.(*crypt.Fs)
		result.cacheName = name
	}
	return result, nil
}

type handle struct {
	fs              fs.Fs
	crypt           *crypt.Fs
	cacheName, root string
	limit           int
}

func (h *handle) Close() {
	if h.cacheName != "" {
		cache.ClearConfig(h.cacheName)
		cache.ClearMappingsPrefix(h.cacheName + ":")
	}
	if shutdown := h.fs.Features().Shutdown; shutdown != nil {
		if e := shutdown(context.Background()); e != nil {
			log.Print("storage client shutdown failed")
		}
	}
}
func (h *handle) PhysicalKey(value string) string {
	if h.crypt != nil {
		value = h.crypt.EncryptFileName(value)
	}
	if h.root != "" {
		return h.root + "/" + value
	}
	return value
}
func cleanError(e error) error {
	if e == nil {
		return nil
	}
	if errors.Is(e, fs.ErrorObjectNotFound) || errors.Is(e, fs.ErrorDirNotFound) {
		return storage.ErrNotFound
	}
	if errors.Is(e, fs.ErrorDirectoryNotEmpty) || errors.Is(e, fs.ErrorDirExists) {
		return storage.ErrConflict
	}
	return errors.New("storage operation failed; check endpoint, permissions, and encryption settings")
}
func entry(value fs.DirEntry) storage.Entry {
	_, dir := value.(fs.Directory)
	return storage.Entry{Name: path.Base(value.Remote()), Path: value.Remote(), Directory: dir, Size: value.Size()}
}
func (h *handle) List(ctx context.Context, dir string) ([]storage.Entry, error) {
	if _, e := storage.Path(dir, true); e != nil {
		return nil, e
	}
	out := []storage.Entry{}
	callback := func(entries fs.DirEntries) error {
		for _, v := range entries {
			if len(out) >= h.limit {
				return storage.ErrTooLarge
			}
			if _, e := storage.Path(v.Remote(), false); e != nil {
				return storage.ErrInvalidPath
			}
			out = append(out, entry(v))
		}
		return nil
	}
	var e error
	if list := h.fs.Features().ListP; list != nil {
		e = list(ctx, dir, callback)
	} else {
		var entries fs.DirEntries
		entries, e = h.fs.List(ctx, dir)
		if e == nil {
			e = callback(entries)
		}
	}
	if e != nil {
		if errors.Is(e, storage.ErrTooLarge) || errors.Is(e, storage.ErrInvalidPath) {
			return nil, e
		}
		if errors.Is(e, fs.ErrorDirNotFound) || errors.Is(e, fs.ErrorObjectNotFound) {
			return nil, storage.ErrNotFound
		}
		if h.crypt != nil {
			return nil, storage.ErrCrypto
		}
		return nil, cleanError(e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Directory != out[j].Directory {
			return out[i].Directory
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}
func (h *handle) Stat(ctx context.Context, key string) (storage.Entry, error) {
	if _, e := storage.Path(key, false); e != nil {
		return storage.Entry{}, e
	}
	o, e := h.fs.NewObject(ctx, key)
	if e != nil {
		return storage.Entry{}, cleanError(e)
	}
	out := entry(o)
	out.Modified = o.ModTime(ctx)
	return out, nil
}
func (h *handle) Open(ctx context.Context, key string, start, end int64) (storage.Object, error) {
	if _, e := storage.Path(key, false); e != nil {
		return storage.Object{}, e
	}
	o, e := h.fs.NewObject(ctx, key)
	if e != nil {
		return storage.Object{}, cleanError(e)
	}
	var options []fs.OpenOption
	if start >= 0 {
		options = append(options, &fs.RangeOption{Start: start, End: end})
	}
	in, e := o.Open(ctx, options...)
	if e != nil {
		if h.crypt != nil {
			return storage.Object{}, storage.ErrCrypto
		}
		return storage.Object{}, cleanError(e)
	}
	return storage.Object{ReadCloser: in, Size: o.Size(), Modified: o.ModTime(ctx)}, nil
}
func (h *handle) Put(ctx context.Context, key string, in io.Reader) error {
	if _, e := storage.Path(key, false); e != nil {
		return e
	}
	source := object.NewStaticObjectInfo(key, time.Now(), -1, true, nil, h.fs)
	put := h.fs.Features().PutStream
	if put == nil {
		return errors.New("streaming upload unavailable")
	}
	_, e := put(ctx, in, source)
	return cleanError(e)
}
func (h *handle) Mkdir(ctx context.Context, key string) error {
	if _, e := storage.Path(key, false); e != nil {
		return e
	}
	return cleanError(h.fs.Mkdir(ctx, key))
}
func (h *handle) Remove(ctx context.Context, key string, dir bool) error {
	if _, e := storage.Path(key, false); e != nil {
		return e
	}
	if dir {
		return cleanError(h.fs.Rmdir(ctx, key))
	}
	o, e := h.fs.NewObject(ctx, key)
	if e != nil {
		return cleanError(e)
	}
	return cleanError(o.Remove(ctx))
}

// ValidateBucket forbids remote syntax, separators, and implicit bucket discovery.
func ValidateBucket(bucket string) bool {
	return len(bucket) >= 3 && len(bucket) <= 63 && !strings.ContainsAny(bucket, "/:\\\x00 ") && bucket == strings.ToLower(bucket) && strings.Trim(bucket, "abcdefghijklmnopqrstuvwxyz0123456789.-") == ""
}
