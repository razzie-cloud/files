package app

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/razzie-cloud/files/internal/secrets"
	"github.com/razzie-cloud/files/internal/store"
	"golang.org/x/sync/singleflight"
)

const shareCacheTTL = 30 * time.Second
const shareCacheLimit = 1024

type shareSnapshot struct {
	Share              store.Share
	Directory          store.Directory
	Connection         store.Connection
	Credentials, Crypt secrets.Value
}
type shareCacheEntry struct {
	value shareSnapshot
	err   error
	until time.Time
}
type shareCache struct {
	mu         sync.Mutex
	entries    map[string]shareCacheEntry
	generation uint64
	flights    singleflight.Group
}

func (c *shareCache) invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = nil
	c.generation++
}
func (c *shareCache) get(ctx context.Context, id string, load func(context.Context, string) (shareSnapshot, error)) (shareSnapshot, error) {
	c.mu.Lock()
	if v, ok := c.entries[id]; ok && time.Now().Before(v.until) {
		c.mu.Unlock()
		return v.value, v.err
	}
	generation := c.generation
	c.mu.Unlock()
	// Coalesce misses without holding the cache mutex across database I/O.
	ch := c.flights.DoChan(strconv.FormatUint(generation, 10)+":"+id, func() (any, error) {
		c.mu.Lock()
		if v, ok := c.entries[id]; ok && time.Now().Before(v.until) {
			c.mu.Unlock()
			return v, nil
		}
		c.mu.Unlock()
		loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		started := time.Now()
		value, err := load(loadCtx, id)
		ttl := shareCacheTTL
		if errors.Is(err, store.ErrNotFound) {
			ttl = 5 * time.Second
		}
		entry := shareCacheEntry{value: value, err: err, until: started.Add(ttl)}
		if err == nil || errors.Is(err, store.ErrNotFound) {
			c.mu.Lock()
			if generation == c.generation {
				if c.entries == nil {
					c.entries = make(map[string]shareCacheEntry)
				}
				for key, v := range c.entries {
					if !time.Now().Before(v.until) {
						delete(c.entries, key)
					}
				}
				if len(c.entries) >= shareCacheLimit {
					for key := range c.entries {
						delete(c.entries, key)
						break
					}
				}
				c.entries[id] = entry
			}
			c.mu.Unlock()
		}
		return entry, nil
	})
	select {
	case <-ctx.Done():
		return shareSnapshot{}, ctx.Err()
	case result := <-ch:
		entry := result.Val.(shareCacheEntry)
		return entry.value, entry.err
	}
}
func (s *Server) loadShare(ctx context.Context, id string) (v shareSnapshot, err error) {
	v.Share, err = s.Repo.PublicShare(ctx, id)
	if err != nil {
		return v, err
	}
	v.Directory, err = s.Repo.Directory(ctx, v.Share.Owner, v.Share.DirectoryID)
	if err != nil {
		return v, err
	}
	v.Connection, err = s.Repo.Connection(ctx, v.Share.Owner, v.Directory.ConnectionID)
	if err != nil {
		return v, err
	}
	v.Credentials, err = s.Secrets.Get(ctx, v.Connection.Owner, v.Connection.SecretRef)
	if err == nil && v.Directory.EncryptionMode == "rclone_crypt" {
		v.Crypt, err = s.Secrets.Get(ctx, v.Share.Owner, v.Directory.CryptSecretRef)
	}
	return v, err
}
