package sanitizer

import (
	"strings"
	"testing"
)

func newReplacerWithSecret(secret string) (*StreamingReplacer, string) {
	m := NewMapping()
	placeholder := m.PutOrGet(secret)
	return NewStreamingReplacer(m), placeholder
}

func TestStreamingReplacer_SingleChunk(t *testing.T) {
	r, ph := newReplacerWithSecret("sk-real-secret-001")
	chunk := []byte("data: " + ph + " here\n\n")
	got := string(r.Write(chunk)) + string(r.Final())
	if !strings.Contains(got, "sk-real-secret-001") {
		t.Errorf("placeholder not restored: %q", got)
	}
	if strings.Contains(got, "EVERYAPI_SECRET") {
		t.Errorf("placeholder leaked into output: %q", got)
	}
}

func TestStreamingReplacer_BoundarySplit_Prefix(t *testing.T) {
	// Split chunk boundary right inside the placeholder prefix.
	r, ph := newReplacerWithSecret("sk-real-secret-001")
	// Carve at any position 1..len(prefix)-1 and verify the split is reassembled.
	for cut := 1; cut < len(PlaceholderPrefix); cut++ {
		r2 := NewStreamingReplacer(r.mapping)
		full := "data: " + ph + " end\n\n"
		a := full[:6+cut]
		b := full[6+cut:]
		out := string(r2.Write([]byte(a))) + string(r2.Write([]byte(b))) + string(r2.Final())
		if !strings.Contains(out, "sk-real-secret-001") {
			t.Errorf("cut=%d: placeholder not restored from split, got %q", cut, out)
		}
		if strings.Contains(out, "EVERYAPI_SECRET") {
			t.Errorf("cut=%d: placeholder leaked, got %q", cut, out)
		}
	}
}

func TestStreamingReplacer_BoundarySplit_Digits(t *testing.T) {
	// Build a mapping with a 3-digit-id placeholder and split mid-digits.
	m := NewMapping()
	ph := m.PutOrGet("important-secret-XYZ")
	full := "data: " + ph + " end\n\n"
	// Cut after PlaceholderPrefix + "0" (one of the three digits)
	cut := strings.Index(full, PlaceholderPrefix) + len(PlaceholderPrefix) + 1
	r := NewStreamingReplacer(m)
	out := string(r.Write([]byte(full[:cut]))) + string(r.Write([]byte(full[cut:]))) + string(r.Final())
	if !strings.Contains(out, "important-secret-XYZ") {
		t.Errorf("digits split not reassembled: %q", out)
	}
}

func TestStreamingReplacer_BoundarySplit_Suffix(t *testing.T) {
	r, ph := newReplacerWithSecret("trailing-secret")
	full := "data: " + ph + " end\n\n"
	// Cut inside the closing `__>>` (between the two underscores or after first `>`).
	suffixStart := strings.LastIndex(full, PlaceholderSuffix)
	for off := 1; off < len(PlaceholderSuffix); off++ {
		r2 := NewStreamingReplacer(r.mapping)
		a := full[:suffixStart+off]
		b := full[suffixStart+off:]
		out := string(r2.Write([]byte(a))) + string(r2.Write([]byte(b))) + string(r2.Final())
		if !strings.Contains(out, "trailing-secret") {
			t.Errorf("suffix-cut at %d: not reassembled, got %q", off, out)
		}
	}
}

func TestStreamingReplacer_UnknownPlaceholderPassthrough(t *testing.T) {
	// Upstream injects a placeholder with an id we never allocated.
	// Trust-minimal: pass through verbatim, the SDK can see something
	// fishy.
	m := NewMapping()
	r := NewStreamingReplacer(m)
	rogue := PlaceholderPrefix + "999" + PlaceholderSuffix
	chunk := "data: " + rogue + " end\n\n"
	out := string(r.Write([]byte(chunk))) + string(r.Final())
	if !strings.Contains(out, rogue) {
		t.Errorf("unknown placeholder should pass through verbatim, got %q", out)
	}
}

func TestStreamingReplacer_NoPlaceholders(t *testing.T) {
	// Normal SSE chunks with no placeholders must pass through with
	// only the carryover-for-future-placeholder tail withheld.
	m := NewMapping()
	r := NewStreamingReplacer(m)
	full := "data: {\"id\":\"x\",\"delta\":{\"content\":\"hi there\"}}\n\n"
	out := string(r.Write([]byte(full))) + string(r.Final())
	if out != full {
		t.Errorf("clean chunk round-trip failed:\n got %q\nwant %q", out, full)
	}
}

func TestStreamingReplacer_MultipleChunksInterleaved(t *testing.T) {
	r, ph1 := newReplacerWithSecret("secret-1")
	ph2 := r.mapping.PutOrGet("secret-2")
	// Two placeholders in two separate chunks.
	c1 := []byte("event: msg\ndata: prefix " + ph1 + " mid")
	c2 := []byte(" tail " + ph2 + " end\n\n")
	out := string(r.Write(c1)) + string(r.Write(c2)) + string(r.Final())
	if !strings.Contains(out, "secret-1") || !strings.Contains(out, "secret-2") {
		t.Errorf("interleaved replacement failed: %q", out)
	}
}

// TestStreamingReplacer_MultiPlaceholder_EveryBoundary is the
// definitive guard for the "chunk-split placeholder dropped when a
// later complete placeholder follows" concern. It builds a payload
// holding two KNOWN placeholders and one UNKNOWN (verbatim
// passthrough) placeholder interleaved with literal text that itself
// contains the placeholder-prefix bytes as a decoy, then replays it
// split at EVERY possible single byte boundary (two Writes + Final)
// and at every pair of boundaries (three Writes + Final). For each
// split the fully-restored output must be byte-identical to the
// single-shot result — i.e. no split may strand or drop a placeholder.
func TestStreamingReplacer_MultiPlaceholder_EveryBoundary(t *testing.T) {
	m := NewMapping()
	ph1 := m.PutOrGet("sk-real-secret-ONE")
	ph2 := m.PutOrGet("sk-real-secret-TWO")
	// An id the mapping never minted → must pass through verbatim.
	rogue := PlaceholderPrefix + "998" + PlaceholderSuffix

	// Decoy: literal text that *starts with* the prefix but isn't a
	// real placeholder, to exercise the false-positive carryover path.
	decoy := PlaceholderPrefix + "not-a-secret "
	full := "event: msg\n" +
		"data: {\"a\":\"" + ph1 + "\"} " + decoy +
		"mid " + rogue + " more " + ph2 + " tail\n\n"

	want := strings.ReplaceAll(full, ph1, "sk-real-secret-ONE")
	want = strings.ReplaceAll(want, ph2, "sk-real-secret-TWO")
	// rogue stays as-is (unknown id → verbatim passthrough).

	feed := func(chunks ...string) string {
		r := NewStreamingReplacer(m)
		var b strings.Builder
		for _, c := range chunks {
			b.Write(r.Write([]byte(c)))
		}
		b.Write(r.Final())
		return b.String()
	}

	// Sanity: single shot.
	if got := feed(full); got != want {
		t.Fatalf("single-shot mismatch:\n got %q\nwant %q", got, want)
	}

	// Every single split point.
	for i := 1; i < len(full); i++ {
		if got := feed(full[:i], full[i:]); got != want {
			t.Fatalf("2-way split at %d stranded/dropped a placeholder:\n got %q\nwant %q", i, got, want)
		}
	}

	// Every pair of split points (three chunks). O(n^2) but the
	// payload is short; this is the case the review flagged —
	// a partial plus a later complete placeholder in one buffer.
	for i := 1; i < len(full); i++ {
		for j := i + 1; j < len(full); j++ {
			if got := feed(full[:i], full[i:j], full[j:]); got != want {
				t.Fatalf("3-way split at (%d,%d) stranded/dropped a placeholder:\n got %q\nwant %q", i, j, got, want)
			}
		}
	}
}

func TestStreamingReplacer_PartialAtEOFStillFlushes(t *testing.T) {
	// If the upstream closes mid-placeholder, the partial bytes
	// emerge verbatim on Final() — better to show the user "we got
	// half a placeholder, upstream cut off" than silently swallow.
	m := NewMapping()
	r := NewStreamingReplacer(m)
	r.Write([]byte("data: " + PlaceholderPrefix + "01"))
	out := string(r.Final())
	if !strings.Contains(out, PlaceholderPrefix+"01") {
		t.Errorf("Final() must release partial bytes, got %q", out)
	}
}
