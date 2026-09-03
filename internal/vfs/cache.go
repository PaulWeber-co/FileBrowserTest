package vfs

import (
	"strings"
	"sync"
	"time"
)

type cacheItem struct {
	entries []Entry
	expires time.Time
}

// listCache hält Verzeichnisauflistungen kurz vor. Bei Schreibzugriffen wird
// gezielt invalidiert, damit die UI nie veraltete Stände zeigt.
type listCache struct {
	ttl time.Duration
	mu  sync.RWMutex
	m   map[string]cacheItem
}

func newListCache(ttl time.Duration) *listCache {
	return &listCache{ttl: ttl, m: make(map[string]cacheItem)}
}

func (c *listCache) get(p string) ([]Entry, bool) {
	if c.ttl <= 0 {
		return nil, false
	}
	c.mu.RLock()
	it, ok := c.m[p]
	c.mu.RUnlock()
	if !ok || time.Now().After(it.expires) {
		return nil, false
	}
	out := make([]Entry, len(it.entries))
	copy(out, it.entries)
	return out, true
}

func (c *listCache) put(p string, e []Entry) {
	if c.ttl <= 0 {
		return
	}
	cp := make([]Entry, len(e))
	copy(cp, e)
	c.mu.Lock()
	if len(c.m) > 512 { // simple Obergrenze statt LRU-Buchhaltung
		c.m = make(map[string]cacheItem, 64)
	}
	c.m[p] = cacheItem{entries: cp, expires: time.Now().Add(c.ttl)}
	c.mu.Unlock()
}

func (c *listCache) del(p string) {
	c.mu.Lock()
	delete(c.m, p)
	c.mu.Unlock()
}

func (c *listCache) delPrefix(p string) {
	c.mu.Lock()
	for k := range c.m {
		if k == p || strings.HasPrefix(k, p+"/") {
			delete(c.m, k)
		}
	}
	c.mu.Unlock()
}

func (c *listCache) purge() {
	c.mu.Lock()
	c.m = make(map[string]cacheItem)
	c.mu.Unlock()
}
