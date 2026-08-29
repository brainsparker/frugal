package cache

import (
	"testing"
	"time"
)

func TestNilCacheIsNoOp(t *testing.T) {
	var c *Cache
	c.Put("k", "v", 0.01, time.Minute)
	if _, _, ok := c.Get("k"); ok {
		t.Fatal("nil cache returned a hit")
	}
	if s := c.Snapshot(); s != (Stats{}) {
		t.Fatalf("nil cache snapshot = %+v, want zero", s)
	}
	c.SetClock(time.Now) // must not panic
}

func TestPutGetRoundtrip(t *testing.T) {
	c := New(4)
	c.Put("k", "hello", 0.002, time.Minute)
	v, age, ok := c.Get("k")
	if !ok {
		t.Fatal("expected hit")
	}
	if v.(string) != "hello" {
		t.Fatalf("value = %v, want hello", v)
	}
	if age < 0 || age > time.Second {
		t.Fatalf("age = %v, want near zero", age)
	}
	s := c.Snapshot()
	if s.Hits != 1 || s.Misses != 0 || s.Entries != 1 {
		t.Fatalf("snapshot = %+v, want 1 hit / 0 misses / 1 entry", s)
	}
	if s.SavedUSD < 0.0019 || s.SavedUSD > 0.0021 {
		t.Fatalf("SavedUSD = %v, want ~0.002", s.SavedUSD)
	}
}

func TestTTLExpiry(t *testing.T) {
	c := New(4)
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	c.SetClock(func() time.Time { return now })

	c.Put("k", 42, 0, 5*time.Minute)
	if _, _, ok := c.Get("k"); !ok {
		t.Fatal("expected hit before expiry")
	}

	now = now.Add(5*time.Minute + time.Second)
	if _, _, ok := c.Get("k"); ok {
		t.Fatal("expected miss after expiry")
	}
	// The expired entry must be gone, not just skipped.
	if s := c.Snapshot(); s.Entries != 0 {
		t.Fatalf("entries after expiry = %d, want 0", s.Entries)
	}
}

func TestAgeReported(t *testing.T) {
	c := New(4)
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	c.SetClock(func() time.Time { return now })
	c.Put("k", "v", 0, time.Hour)

	now = now.Add(90 * time.Second)
	_, age, ok := c.Get("k")
	if !ok {
		t.Fatal("expected hit")
	}
	if age != 90*time.Second {
		t.Fatalf("age = %v, want 90s", age)
	}
}

func TestLRUEviction(t *testing.T) {
	c := New(2)
	c.Put("a", 1, 0, time.Minute)
	c.Put("b", 2, 0, time.Minute)
	// Touch a so b becomes least recently used.
	if _, _, ok := c.Get("a"); !ok {
		t.Fatal("expected hit on a")
	}
	c.Put("c", 3, 0, time.Minute)

	if _, _, ok := c.Get("b"); ok {
		t.Fatal("b should have been evicted")
	}
	if _, _, ok := c.Get("a"); !ok {
		t.Fatal("a should have survived")
	}
	if _, _, ok := c.Get("c"); !ok {
		t.Fatal("c should be present")
	}
}

func TestPutUpdatesExisting(t *testing.T) {
	c := New(2)
	c.Put("k", "old", 0, time.Minute)
	c.Put("k", "new", 0, time.Minute)
	v, _, ok := c.Get("k")
	if !ok || v.(string) != "new" {
		t.Fatalf("value = %v ok=%v, want new/true", v, ok)
	}
	if s := c.Snapshot(); s.Entries != 1 {
		t.Fatalf("entries = %d, want 1 (update, not insert)", s.Entries)
	}
}

func TestZeroTTLStoresNothing(t *testing.T) {
	c := New(2)
	c.Put("k", "v", 0, 0)
	if _, _, ok := c.Get("k"); ok {
		t.Fatal("ttl<=0 must not store")
	}
}

func TestNewClampsMaxEntries(t *testing.T) {
	c := New(0)
	if c.maxEntries != DefaultMaxEntries {
		t.Fatalf("maxEntries = %d, want %d", c.maxEntries, DefaultMaxEntries)
	}
}

func TestSearchKeyCanonicalization(t *testing.T) {
	base := SearchKey("", "python docs", 5, "")
	if SearchKey("auto", "  python docs  ", 5, "") != base {
		t.Fatal("auto pin and trimmed query must share a key")
	}
	if SearchKey("serper", "python docs", 5, "") == base {
		t.Fatal("provider pin must change the key")
	}
	if SearchKey("", "python docs", 10, "") == base {
		t.Fatal("max_results must change the key")
	}
	if SearchKey("", "python docs", 5, "day") == base {
		t.Fatal("freshness must change the key")
	}
	if SearchKey("", "Python docs", 5, "") == base {
		t.Fatal("query case must change the key (exact match means exact)")
	}
}

func TestExtractKeyCanonicalization(t *testing.T) {
	base := ExtractKey("", "https://example.com/a", []string{"markdown", "html"})
	if ExtractKey("auto", "https://example.com/a", []string{"HTML", "markdown", "html"}) != base {
		t.Fatal("format order, case, and duplicates must not change the key")
	}
	if ExtractKey("", "https://example.com/a", []string{"markdown"}) == base {
		t.Fatal("different format sets must not share a key")
	}
	if ExtractKey("firecrawl", "https://example.com/a", []string{"markdown", "html"}) == base {
		t.Fatal("provider pin must change the key")
	}
}
