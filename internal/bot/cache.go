package bot

import (
	"sync"
	"time"
)

const defaultMediaCacheTTL = 7 * 24 * time.Hour

var globalMediaCache = NewMediaCache(defaultMediaCacheTTL)

type MediaCache struct {
	mu       sync.RWMutex
	ttl      time.Duration
	now      func() time.Time
	entries  map[string]mediaCacheEntry
	inflight map[string]struct{}
	waiters  map[string][]string
}

type MediaCacheStats struct {
	Entries  int
	Inflight int
	Waiters  int
}

type mediaCacheEntry struct {
	items     []CachedMedia
	expiresAt time.Time
}

type CachedMedia struct {
	Kind        MediaKind
	FileID      string
	ResultID    string
	Title       string
	Description string
	Duration    int64
	Width       int64
	Height      int64
}

func NewMediaCache(ttl time.Duration) *MediaCache {
	if ttl <= 0 {
		ttl = defaultMediaCacheTTL
	}
	return &MediaCache{
		ttl:      ttl,
		now:      time.Now,
		entries:  make(map[string]mediaCacheEntry),
		inflight: make(map[string]struct{}),
		waiters:  make(map[string][]string),
	}
}

func defaultMediaCache() *MediaCache {
	return globalMediaCache
}

func newMediaCacheWithClock(ttl time.Duration, now func() time.Time) *MediaCache {
	cache := NewMediaCache(ttl)
	cache.now = now
	return cache
}

func (c *MediaCache) Get(key string) ([]CachedMedia, bool) {
	if c == nil {
		return nil, false
	}
	now := c.now()

	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if !entry.expiresAt.After(now) {
		c.mu.Lock()
		if current, ok := c.entries[key]; ok && !current.expiresAt.After(now) {
			delete(c.entries, key)
		}
		c.mu.Unlock()
		return nil, false
	}
	cacheable := cacheableCachedMedia(entry.items)
	if len(cacheable) == 0 {
		c.mu.Lock()
		if current, ok := c.entries[key]; ok && len(cacheableCachedMedia(current.items)) == 0 {
			delete(c.entries, key)
		}
		c.mu.Unlock()
		return nil, false
	}
	return cacheable, true
}

func (c *MediaCache) Set(key string, items []CachedMedia) {
	cacheable := cacheableCachedMedia(items)
	if c == nil || len(cacheable) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = mediaCacheEntry{
		items:     cacheable,
		expiresAt: c.now().Add(c.ttl),
	}
}

func (c *MediaCache) StartPrepare(key string) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.inflight[key]; ok {
		return false
	}
	c.inflight[key] = struct{}{}
	return true
}

func (c *MediaCache) FinishPrepare(key string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.inflight, key)
}

func (c *MediaCache) AddWaiter(key string, inlineMessageID string) {
	if c == nil || inlineMessageID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.waiters[key] = append(c.waiters[key], inlineMessageID)
}

func (c *MediaCache) TakeWaiters(key string) []string {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	waiters := append([]string(nil), c.waiters[key]...)
	delete(c.waiters, key)
	return waiters
}

func (c *MediaCache) CleanupExpired() int {
	if c == nil {
		return 0
	}
	now := c.now()
	deleted := 0

	c.mu.Lock()
	defer c.mu.Unlock()
	for key, entry := range c.entries {
		if !entry.expiresAt.After(now) {
			delete(c.entries, key)
			deleted++
		}
	}
	return deleted
}

func (c *MediaCache) Stats() MediaCacheStats {
	if c == nil {
		return MediaCacheStats{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()

	waiters := 0
	for _, values := range c.waiters {
		waiters += len(values)
	}
	return MediaCacheStats{
		Entries:  len(c.entries),
		Inflight: len(c.inflight),
		Waiters:  waiters,
	}
}

func cloneCachedMedia(items []CachedMedia) []CachedMedia {
	cloned := make([]CachedMedia, len(items))
	copy(cloned, items)
	return cloned
}

func cacheableCachedMedia(items []CachedMedia) []CachedMedia {
	if len(items) == 0 {
		return nil
	}
	cacheable := make([]CachedMedia, 0, len(items))
	for _, item := range items {
		if item.Kind != MediaKindVideo {
			continue
		}
		cacheable = append(cacheable, item)
	}
	return cloneCachedMedia(cacheable)
}
