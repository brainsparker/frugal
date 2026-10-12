// Package cache implements Frugal's exact-match result cache: a
// bounded, in-memory TTL store that lets the routed read tools
// (frugal__search, frugal__extract, and frugal__execute when it lands
// on those capabilities) answer a repeated call without paying a
// provider again.
//
// Scope is deliberately narrow for v1:
//
//   - Exact-match only. The key is a canonical string of the call's
//     routing-relevant arguments. "python docs" and "Python docs" are
//     different entries. The roadmap's semantic cache builds on top of
//     this layer later; it does not replace it.
//   - Read capabilities only. Browse is never cached: rendering a page
//     is exactly the case where the caller wants the live DOM.
//   - In-memory, per-process. Nothing is written to disk, so cached
//     provider payloads never outlive the server process.
//
// Thread-safety: one mutex around a map plus an LRU list. The routed
// tools are network-bound; a single lock on the memory path is not the
// bottleneck and keeps eviction logic obviously correct.
package cache

import (
	"container/list"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultMaxEntries bounds the cache when the operator enables caching
// without setting a size. Entries hold search snippets or extracted
// article text, so hundreds (not millions) is the sane default order.
const DefaultMaxEntries = 512

// Cache is a bounded TTL + LRU store. Construct with New; the zero
// value is not usable, and a nil *Cache is a safe no-op on every
// method, mirroring the routing.Guard convention so call sites need no
// conditionals.
type Cache struct {
	mu         sync.Mutex
	entries    map[string]*list.Element
	lru        *list.List // front = most recently used
	maxEntries int
	now        func() time.Time

	hits       int64
	misses     int64
	savedMicro int64 // micro-USD saved by hits (original call cost, x1e6)
}

type entry struct {
	key      string
	value    any
	costUSD  float64
	storedAt time.Time
	expires  time.Time
}

// New builds a cache holding at most maxEntries entries. Values at or
// below zero fall back to DefaultMaxEntries: a cache the operator
// enabled should never be silently disabled by a config typo.
func New(maxEntries int) *Cache {
	if maxEntries <= 0 {
		maxEntries = DefaultMaxEntries
	}
	return &Cache{
		entries:    make(map[string]*list.Element),
		lru:        list.New(),
		maxEntries: maxEntries,
		now:        time.Now,
	}
}

// SetClock overrides the time source. Test hook only.
func (c *Cache) SetClock(now func() time.Time) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

// Get returns the stored value and its age when key is present and not
// expired. Expired entries are removed on the spot, so a steady stream
// of misses cannot hold dead payloads alive until eviction. A hit books
// the original call's cost as savings.
func (c *Cache) Get(key string) (value any, age time.Duration, ok bool) {
	if c == nil {
		return nil, 0, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, found := c.entries[key]
	if !found {
		c.misses++
		return nil, 0, false
	}
	en := el.Value.(*entry)
	now := c.now()
	if now.After(en.expires) {
		c.removeLocked(el)
		c.misses++
		return nil, 0, false
	}
	c.lru.MoveToFront(el)
	c.hits++
	c.savedMicro += int64(en.costUSD * 1e6)
	return en.value, now.Sub(en.storedAt), true
}

// Put stores value under key for ttl. costUSD is what the original call
// paid; it is what a later hit records as saved. A ttl at or below zero
// stores nothing: the operator turned that capability's cache off.
func (c *Cache) Put(key string, value any, costUSD float64, ttl time.Duration) {
	if c == nil || ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if el, found := c.entries[key]; found {
		en := el.Value.(*entry)
		en.value = value
		en.costUSD = costUSD
		en.storedAt = now
		en.expires = now.Add(ttl)
		c.lru.MoveToFront(el)
		return
	}
	el := c.lru.PushFront(&entry{
		key:      key,
		value:    value,
		costUSD:  costUSD,
		storedAt: now,
		expires:  now.Add(ttl),
	})
	c.entries[key] = el
	for c.lru.Len() > c.maxEntries {
		c.removeLocked(c.lru.Back())
	}
}

func (c *Cache) removeLocked(el *list.Element) {
	if el == nil {
		return
	}
	en := el.Value.(*entry)
	delete(c.entries, en.key)
	c.lru.Remove(el)
}

// Stats is a point-in-time snapshot of cache effectiveness.
type Stats struct {
	Hits     int64
	Misses   int64
	SavedUSD float64
	Entries  int
}

// Snapshot returns the current counters. Safe on a nil cache (all
// zeros), so status surfaces can render unconditionally.
func (c *Cache) Snapshot() Stats {
	if c == nil {
		return Stats{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return Stats{
		Hits:     c.hits,
		Misses:   c.misses,
		SavedUSD: float64(c.savedMicro) / 1e6,
		Entries:  c.lru.Len(),
	}
}

// SearchKey canonicalizes a search call into a cache key. provider is
// the caller's pin ("" or "auto" both mean auto-routing and must map to
// the same key). The query text is trimmed but case is preserved:
// exact-match means exact.
func SearchKey(provider, query string, maxResults int, freshness string) string {
	return strings.Join([]string{
		"search",
		normalizeProvider(provider),
		strconv.Itoa(maxResults),
		strings.ToLower(strings.TrimSpace(freshness)),
		strings.TrimSpace(query),
	}, "\x1f")
}

// ExtractKey canonicalizes an extract call into a cache key. Formats
// are lowercased, deduplicated, and sorted so ["markdown","html"] and
// ["HTML","markdown"] share an entry: drivers treat formats as a set.
func ExtractKey(provider, url string, formats []string) string {
	canon := make([]string, 0, len(formats))
	seen := make(map[string]bool, len(formats))
	for _, f := range formats {
		f = strings.ToLower(strings.TrimSpace(f))
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		canon = append(canon, f)
	}
	sortStrings(canon)
	return strings.Join([]string{
		"extract",
		normalizeProvider(provider),
		strings.Join(canon, ","),
		strings.TrimSpace(url),
	}, "\x1f")
}

func normalizeProvider(p string) string {
	p = strings.ToLower(strings.TrimSpace(p))
	if p == "auto" {
		return ""
	}
	return p
}

// sortStrings is insertion sort: format lists have at most three
// entries, so pulling in sort for this would be noise.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
