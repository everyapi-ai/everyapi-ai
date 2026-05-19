package sanitizer

import (
	"sync"
)

// DefaultMappingCap is the soft cap on how many distinct secrets the
// mapping table will hold before it starts evicting the oldest entry
// to make room for a new one. Real workloads are nowhere near this —
// a typical prompt has < 5 sensitive substrings, and even a long-
// running proxy serving a single buyer rarely exceeds a few hundred
// distinct values in a day — but a long-lived detached process with
// a high false-positive detector set could in theory grow unbounded.
// 10 000 is a comfortable headroom over realistic usage while still
// being trivially small in RAM.
const DefaultMappingCap = 10000

// Mapping is the in-memory real↔placeholder table that gives the
// sanitizer its trust-minimal property: the platform never sees real
// secret values, and it cannot inject a placeholder the proxy doesn't
// already know about (because the table is local-only).
//
// The mapping is process-scoped — there is no on-disk persistence —
// so a fresh proxy process starts with a clean table and a buyer's
// secrets can never leak between processes.
//
// Lookups are by exact byte equality. Two identical real values
// always map to the SAME placeholder id (spec §7-2: "Stable mapping
// 同一真实值 → 同一 placeholder string, 保证 prompt caching 仍命中").
// This is important: when an LLM provider does prompt-caching against
// the upstream EveryAPI gateway, the cache key includes the request
// body bytes — if the same secret rotated through a different
// placeholder on every call, the cache would miss and the user would
// be billed full cold-prompt prices.
//
// Eviction: once the table hits Cap distinct values, every new entry
// evicts the oldest one (by insertion order). The evicted secret's
// placeholder id becomes unresolvable — if it later appears in an
// upstream response, the streaming replacer will pass it through
// verbatim, which is harmless: the SDK will see literal placeholder
// text and the buyer notices something is off. The alternative
// (refusing to add new entries) would be worse — fresh secrets in
// the prompt would leak through unsanitised. FIFO eviction is also
// stable: a long-running proxy doesn't accumulate forever, and the
// most recently used secrets (which are typically what prompts cite
// repeatedly) stay in the table.
type Mapping struct {
	mu     sync.RWMutex
	byReal map[string]int // real value → id
	byID   map[int]string // id → real value (for the inbound restore pass)
	order  []int          // ids in insertion order; order[0] is the oldest
	cap    int            // 0 = unbounded
	nextID int
}

// NewMapping returns an empty mapping with the default cap. ids
// start at 1 so a printed `<<__EVERYAPI_SECRET_000__>>` is recognisably
// "test fixture" or "bug".
func NewMapping() *Mapping {
	return NewMappingWithCap(DefaultMappingCap)
}

// NewMappingWithCap returns an empty mapping with the given soft
// cap; pass 0 for "unbounded" (mainly useful in tests).
func NewMappingWithCap(cap int) *Mapping {
	return &Mapping{
		byReal: make(map[string]int),
		byID:   make(map[int]string),
		cap:    cap,
		nextID: 1,
	}
}

// PutOrGet returns the placeholder string for `real`, allocating a
// new id if this is the first sighting. Empty input returns "" — the
// sanitizer must never produce a placeholder for the empty string
// because that would corrupt any downstream text that happens to be
// empty in the original prompt.
//
// If the table is at its Cap when a new value arrives, the oldest
// entry is evicted to make room. See the type-level comment for why
// FIFO is the right eviction policy here.
func (m *Mapping) PutOrGet(real string) string {
	if real == "" {
		return ""
	}
	m.mu.RLock()
	if id, ok := m.byReal[real]; ok {
		m.mu.RUnlock()
		return MakePlaceholder(id)
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	// Recheck under the write lock — a concurrent goroutine may have
	// inserted between the RUnlock and the Lock above.
	if id, ok := m.byReal[real]; ok {
		return MakePlaceholder(id)
	}
	// Evict if at cap. Cap == 0 means unbounded (set by tests).
	if m.cap > 0 && len(m.byReal) >= m.cap {
		oldest := m.order[0]
		m.order = m.order[1:]
		if v, ok := m.byID[oldest]; ok {
			delete(m.byReal, v)
			delete(m.byID, oldest)
		}
	}
	id := m.nextID
	m.nextID++
	m.byReal[real] = id
	m.byID[id] = real
	m.order = append(m.order, id)
	return MakePlaceholder(id)
}

// Lookup returns the real value for an id, or "" + false if the id
// wasn't allocated by this process (or was evicted from the table
// after a cap-driven roll). The proxy uses this on the inbound
// (downstream) pass: it scans response bytes for placeholder matches,
// and for each match resolves the id back to the real value. A
// placeholder the upstream invented out of thin air (an attack trying
// to use the sanitizer as an oracle) will miss here and the
// placeholder text gets passed through to the SDK as-is — which is
// harmless because the SDK has no way to interpret a placeholder
// either, AND the buyer sees the literal placeholder which signals
// "something weird happened, don't trust this output".
func (m *Mapping) Lookup(id int) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.byID[id]
	return v, ok
}

// Size returns the number of distinct real values currently held.
// Used by `everyapi proxy status` for visibility.
func (m *Mapping) Size() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.byReal)
}

// Reset clears the table. Called by the server on shutdown; making it
// explicit (rather than relying on GC) helps with tests that want a
// fresh mapping per case.
func (m *Mapping) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.byReal = make(map[string]int)
	m.byID = make(map[int]string)
	m.order = nil
	m.nextID = 1
}
