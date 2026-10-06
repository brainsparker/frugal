package routing

import (
	"fmt"
	"time"
)

// Loop rail: the third guard rail, alongside daily budgets and the 429
// cooldown. It catches an agent stuck re-issuing the same tool call
// (same tool, same arguments) in a tight loop, and refuses the call
// before a provider is paid for it.
//
// Why this lives in the routing layer: a stuck agent is the one failure
// mode the other two rails cannot see coming. A daily budget only trips
// after the money is spent, and the cooldown only fires after a
// provider says 429. Free rungs (Marginalia, Wikipedia, go-readability)
// never hit either rail, so a loop on a free provider runs until the
// client gives up. The MCP spec gives clients no backoff convention, so
// the server has to be the one that says stop.
//
// The rail keys on a fingerprint the tool layer computes from the
// normalized input (tools/loop.go), never on query content stored
// anywhere: fingerprints are hashes, held in memory only, and the map is
// bounded so a long session cannot grow it without limit.

// DefaultLoopLimit and DefaultLoopWindow are the product defaults the
// CLI wires when the config does not say otherwise: a sixth identical
// call inside one minute is refused. Five identical calls in a minute is
// not a retry pattern any client exhibits on purpose; a loop reaches it
// in seconds.
const (
	DefaultLoopLimit  = 5
	DefaultLoopWindow = 60 * time.Second
)

// maxLoopEntries bounds the fingerprint map. Past this, expired entries
// are swept first and the least recently seen entry is evicted if the
// sweep was not enough. Each entry holds at most loopLimit+1 timestamps,
// so memory stays in the low hundreds of KB at the cap.
const maxLoopEntries = 1024

// loopEntry is one fingerprint's recent call times, oldest first.
type loopEntry struct {
	times []time.Time
}

// SetLoopGuard turns the loop rail on with the given limit and window.
// limit is the number of identical calls admitted per window; the call
// that would make limit+1 is refused. A limit of zero or less turns the
// rail off. A window of zero or less falls back to DefaultLoopWindow.
// Safe on a nil Guard (no-op) and safe to call before serving starts;
// it is not meant to be toggled while calls are in flight.
func (g *Guard) SetLoopGuard(limit int, window time.Duration) {
	if g == nil {
		return
	}
	if window <= 0 {
		window = DefaultLoopWindow
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if limit <= 0 {
		g.loopLimit = 0
		g.loops = make(map[string]*loopEntry)
		return
	}
	g.loopLimit = limit
	g.loopWindow = window
}

// LoopGuardEnabled reports whether the loop rail is on.
func (g *Guard) LoopGuardEnabled() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.loopLimit > 0
}

// Admit records one call of fingerprint fp under tool and reports
// whether it may proceed. It returns false, with a reason an agent can
// act on, once more than loopLimit calls with this fingerprint have
// landed inside the window. Refused calls are still recorded, so a
// client that keeps hammering stays refused until it stops for a full
// window. A nil Guard, or one with the rail off, always admits.
func (g *Guard) Admit(tool, fp string) (bool, string) {
	if g == nil {
		return true, ""
	}
	now := g.now()

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.loopLimit <= 0 {
		return true, ""
	}

	e, ok := g.loops[fp]
	if !ok {
		if len(g.loops) >= maxLoopEntries {
			g.evictLoopEntryLocked(now)
		}
		e = &loopEntry{}
		g.loops[fp] = e
	}
	e.prune(now, g.loopWindow)
	e.times = append(e.times, now)

	if len(e.times) <= g.loopLimit {
		return true, ""
	}
	// The window clears once the oldest in-window call ages out.
	retry := e.times[0].Add(g.loopWindow).Sub(now).Round(time.Second)
	if retry < time.Second {
		retry = time.Second
	}
	return false, fmt.Sprintf(
		"loop guard: this exact %s call has been made %d times in the last %s (limit %d); "+
			"the result will not change, so change the arguments or wait %s",
		tool, len(e.times), g.loopWindow, g.loopLimit, retry)
}

// prune drops call times that have aged out of the window. Cheap:
// times are appended in order, so this is a prefix cut, and the slice
// stays at most limit+1 long between calls.
func (e *loopEntry) prune(now time.Time, window time.Duration) {
	cutoff := now.Add(-window)
	i := 0
	for i < len(e.times) && !e.times[i].After(cutoff) {
		i++
	}
	if i > 0 {
		e.times = append(e.times[:0], e.times[i:]...)
	}
}

// evictLoopEntryLocked makes room in the fingerprint map: first drop
// every entry whose calls have all aged out, then, if the map is still
// at the cap, drop the entry seen least recently. Caller holds g.mu.
func (g *Guard) evictLoopEntryLocked(now time.Time) {
	for fp, e := range g.loops {
		e.prune(now, g.loopWindow)
		if len(e.times) == 0 {
			delete(g.loops, fp)
		}
	}
	if len(g.loops) < maxLoopEntries {
		return
	}
	var (
		victim string
		oldest time.Time
		found  bool
	)
	for fp, e := range g.loops {
		last := e.times[len(e.times)-1]
		if !found || last.Before(oldest) {
			victim, oldest, found = fp, last, true
		}
	}
	if found {
		delete(g.loops, victim)
	}
}
