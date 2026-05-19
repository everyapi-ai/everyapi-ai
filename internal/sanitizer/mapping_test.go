package sanitizer

import (
	"fmt"
	"sync"
	"testing"
)

func TestMapping_PutOrGet_StableForSameValue(t *testing.T) {
	m := NewMapping()
	a := m.PutOrGet("sk-secret-aaa")
	b := m.PutOrGet("sk-secret-aaa")
	if a != b {
		t.Errorf("same input → different placeholders: %q vs %q", a, b)
	}
	if a == "" {
		t.Errorf("empty placeholder for non-empty input")
	}
}

func TestMapping_PutOrGet_DistinctValuesGetDistinctIds(t *testing.T) {
	m := NewMapping()
	a := m.PutOrGet("sk-secret-aaa")
	b := m.PutOrGet("sk-secret-bbb")
	if a == b {
		t.Errorf("distinct inputs → same placeholder: %q", a)
	}
	if m.Size() != 2 {
		t.Errorf("Size() = %d, want 2", m.Size())
	}
}

func TestMapping_EmptyInput(t *testing.T) {
	m := NewMapping()
	if got := m.PutOrGet(""); got != "" {
		t.Errorf("PutOrGet(\"\") = %q, want empty", got)
	}
	if m.Size() != 0 {
		t.Errorf("empty input must not consume an id (size=%d)", m.Size())
	}
}

func TestMapping_Lookup(t *testing.T) {
	m := NewMapping()
	m.PutOrGet("sk-aaa") // id 1
	m.PutOrGet("sk-bbb") // id 2

	if v, ok := m.Lookup(1); !ok || v != "sk-aaa" {
		t.Errorf("Lookup(1) = (%q, %v), want (%q, true)", v, ok, "sk-aaa")
	}
	if v, ok := m.Lookup(2); !ok || v != "sk-bbb" {
		t.Errorf("Lookup(2) = (%q, %v), want (%q, true)", v, ok, "sk-bbb")
	}
	if _, ok := m.Lookup(999); ok {
		t.Errorf("Lookup(999) succeeded; should miss for unknown id")
	}
}

func TestMapping_Reset(t *testing.T) {
	m := NewMapping()
	m.PutOrGet("sk-aaa")
	m.Reset()
	if m.Size() != 0 {
		t.Errorf("Size() after Reset() = %d, want 0", m.Size())
	}
	// Subsequent value starts from id 1 again.
	got := m.PutOrGet("sk-bbb")
	if got != MakePlaceholder(1) {
		t.Errorf("first PutOrGet after Reset() = %q, want %q", got, MakePlaceholder(1))
	}
}

func TestMapping_CapEviction(t *testing.T) {
	// At cap, every new value evicts the oldest by insertion order
	// (FIFO). The evicted id stops resolving on Lookup; the new
	// value gets a fresh id.
	m := NewMappingWithCap(3)
	m.PutOrGet("a") // id 1
	m.PutOrGet("b") // id 2
	m.PutOrGet("c") // id 3
	if m.Size() != 3 {
		t.Fatalf("Size = %d, want 3", m.Size())
	}
	if _, ok := m.Lookup(1); !ok {
		t.Errorf("id 1 should still resolve before cap pressure")
	}
	// Adding "d" evicts "a" (id 1).
	m.PutOrGet("d") // id 4
	if m.Size() != 3 {
		t.Errorf("Size after eviction = %d, want 3 (cap)", m.Size())
	}
	if _, ok := m.Lookup(1); ok {
		t.Errorf("id 1 (oldest) should have been evicted")
	}
	if _, ok := m.Lookup(2); !ok {
		t.Errorf("id 2 should still resolve after eviction")
	}
	if v, ok := m.Lookup(4); !ok || v != "d" {
		t.Errorf("new entry id=4 v=%q ok=%v, want (\"d\", true)", v, ok)
	}
	// Putting "a" again is a NEW entry (the evicted id is gone —
	// we deliberately do NOT recycle ids, so no two distinct
	// values ever share one placeholder).
	got := m.PutOrGet("a")
	if got == MakePlaceholder(1) {
		t.Errorf("re-inserted value got the same id as the evicted entry; want a fresh id")
	}
}

func TestMapping_CapUnboundedWhenZero(t *testing.T) {
	m := NewMappingWithCap(0)
	for i := 0; i < 100; i++ {
		m.PutOrGet(fmt.Sprintf("v-%d", i))
	}
	if m.Size() != 100 {
		t.Errorf("Size with cap=0 = %d, want 100 (unbounded)", m.Size())
	}
}

func TestMapping_Concurrent(t *testing.T) {
	// Race-detector check: many goroutines hammering the same value
	// must collapse to a single id without panicking.
	m := NewMapping()
	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	results := make([]string, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			results[i] = m.PutOrGet("shared-secret")
		}(i)
	}
	wg.Wait()
	first := results[0]
	for i, r := range results {
		if r != first {
			t.Errorf("concurrent results diverged at %d: %q vs %q", i, r, first)
		}
	}
	if m.Size() != 1 {
		t.Errorf("Size() = %d, want 1 (deduplicated)", m.Size())
	}
}
