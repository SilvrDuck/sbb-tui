// Package querycache holds the on-disk persistent cache of SBB locations-API
// responses, keyed by normalised query string. The cache lets the fuzzy
// popover resolve cross-language inputs ("genf" → Genève) without paying
// the network round-trip after the first encounter.
package querycache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Hit is one station returned by the API for a given query.
type Hit struct {
	UIC  string `json:"uic"`
	Name string `json:"name"`
	Icon string `json:"icon,omitempty"`
}

// Entry is one cached API response.
type Entry struct {
	Hits      []Hit     `json:"hits"`
	FetchedAt time.Time `json:"fetched_at"`
}

// file is the on-disk JSON schema. version lets us evolve without crashes.
type file struct {
	Version int              `json:"version"`
	Entries map[string]Entry `json:"entries"`
}

// Cache is an in-memory, disk-backed query→hits store.
type Cache struct {
	path string

	mu      sync.RWMutex
	entries map[string]Entry
	dirty   int // count of unsaved inserts; we save when this crosses flushEvery
}

// flushEvery is the in-flight write threshold; we also persist on Save().
const flushEvery = 5

// TTL is how long a cache entry is considered fresh. Older entries still
// serve immediately but the caller may refresh in background.
const TTL = 30 * 24 * time.Hour

const fileVersion = 1

// DefaultPath returns the OS-conventional cache file location.
func DefaultPath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sbb-tui", "api-queries.json"), nil
}

// Load reads the cache file from path. A missing or unparseable file returns
// an empty cache — no error, so the picker never breaks because of a bad
// cache.
func Load(path string) *Cache {
	c := &Cache{path: path, entries: make(map[string]Entry)}
	data, err := os.ReadFile(path)
	if err != nil {
		return c
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return c
	}
	if f.Version != fileVersion {
		// Future-proof: bump fileVersion if we ever change schema and want
		// to invalidate older caches.
		return c
	}
	for k, e := range f.Entries {
		c.entries[k] = e
	}
	return c
}

// Save persists the cache to disk atomically. Safe to call frequently; no-op
// when nothing has changed.
func (c *Cache) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dirty == 0 {
		return nil
	}
	if c.path == "" {
		c.dirty = 0
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(file{Version: fileVersion, Entries: c.entries}, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, c.path); err != nil {
		return err
	}
	c.dirty = 0
	return nil
}

// Normalize maps a raw user query to the canonical cache key. Lowercase +
// NFC, trimmed. Keep behaviour aligned with the fuzzy matcher's folding so
// "Genf", "genf", " GENF " all share one entry.
func Normalize(q string) string {
	return strings.ToLower(strings.TrimSpace(q))
}

// Lookup returns the cached hits for query (if any) and whether the entry is
// fresh (within TTL).
func (c *Cache) Lookup(query string) (hits []Hit, fresh bool, ok bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[Normalize(query)]
	if !ok {
		return nil, false, false
	}
	return e.Hits, time.Since(e.FetchedAt) < TTL, true
}

// Insert stores hits for query. Empty result lists are not cached (they
// teach the index nothing and would bloat it on noise queries).
func (c *Cache) Insert(query string, hits []Hit) {
	if len(hits) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[Normalize(query)] = Entry{Hits: hits, FetchedAt: time.Now().UTC()}
	c.dirty++
	if c.dirty >= flushEvery {
		// Best-effort background flush; ignore errors — caller can Save on
		// shutdown for the authoritative version.
		go func(p string, snap map[string]Entry) {
			f := file{Version: fileVersion, Entries: snap}
			data, err := json.MarshalIndent(f, "", "  ")
			if err != nil {
				return
			}
			tmp := p + ".tmp"
			if err := os.WriteFile(tmp, data, 0o644); err != nil {
				return
			}
			_ = os.Rename(tmp, p)
		}(c.path, snapshotEntries(c.entries))
		c.dirty = 0
	}
}

// Size returns the number of cached entries (for diagnostics).
func (c *Cache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// Clear empties the cache.
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]Entry)
	c.dirty++
}

// snapshotEntries returns a defensive copy used by the background save
// goroutine, so we don't race on the map.
func snapshotEntries(src map[string]Entry) map[string]Entry {
	dst := make(map[string]Entry, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
