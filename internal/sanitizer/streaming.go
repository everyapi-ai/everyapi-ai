package sanitizer

import (
	"strconv"
	"strings"
)

// StreamingReplacer is a stateful, byte-stream transformer that
// walks SSE / chunked response bytes and rewrites every complete
// placeholder it sees back into the original real value. It's safe to
// feed arbitrary chunk boundaries — placeholders split across chunks
// are reassembled by buffering a small carryover at the tail.
//
// Why a state machine and not a regex over the whole response: SSE
// responses can run for minutes, the proxy must flush bytes promptly
// (model latency reasons), AND a chunk boundary can fall anywhere
// inside a placeholder. A naive "match-on-each-chunk" misses
// placeholders that straddle two reads. A "buffer the whole response
// then replace" defeats streaming.
//
// Algorithm per chunk:
//
//  1. Append chunk to carryover.
//  2. Run the placeholder regex against the combined buffer; for each
//     complete match, splice in the real value via Mapping.Lookup.
//     Placeholders the proxy doesn't recognise (unknown id) are passed
//     through verbatim — they're either from a future protocol version
//     or, more importantly, from a malicious upstream trying to inject
//     a placeholder we'd resolve to attacker-controlled text.
//  3. Find the longest suffix of the post-substitution buffer that
//     could still grow into a placeholder (PartialAtTail). Hold that
//     suffix as carryover; emit everything before it.
//
// Final flush: when the upstream connection closes, call Final() to
// emit the carryover unchanged (it's now confirmed not to be a
// partial placeholder — there is no "next chunk").
type StreamingReplacer struct {
	mapping *Mapping
	buf     strings.Builder
}

// NewStreamingReplacer returns a fresh replacer wired to the given
// mapping. The mapping is read-only from the replacer's perspective.
func NewStreamingReplacer(m *Mapping) *StreamingReplacer {
	return &StreamingReplacer{mapping: m}
}

// Write feeds a chunk into the replacer and returns the bytes that
// are now safe to forward to the SDK. Caller must NOT discard the
// replacer between chunks — internal state carries forward.
//
// The returned slice is owned by the caller (a fresh string) — the
// replacer's internal buffer may be mutated on the next Write.
func (r *StreamingReplacer) Write(p []byte) []byte {
	r.buf.WriteString(string(p))
	out := r.flush(false)
	return []byte(out)
}

// Final returns any remaining buffered bytes after the upstream has
// closed. It's safe to call exactly once; further Write calls are
// permitted but lose the "no more data" assumption.
func (r *StreamingReplacer) Final() []byte {
	return []byte(r.flush(true))
}

// flush is the core pass: substitute complete placeholders, then
// either hold a partial-at-tail carryover (eof=false) or release
// everything (eof=true).
func (r *StreamingReplacer) flush(eof bool) string {
	if r.buf.Len() == 0 {
		return ""
	}
	s := r.buf.String()
	// Phase 1: substitute every complete placeholder.
	hits := FindPlaceholders(s)
	if len(hits) > 0 {
		var rebuilt strings.Builder
		rebuilt.Grow(len(s))
		cursor := 0
		for _, h := range hits {
			rebuilt.WriteString(s[cursor:h[0]])
			placeholder := s[h[0]:h[1]]
			// Extract the numeric id between PlaceholderPrefix and
			// PlaceholderSuffix.
			idStr := placeholder[len(PlaceholderPrefix) : len(placeholder)-len(PlaceholderSuffix)]
			id, err := strconv.Atoi(idStr)
			if err != nil {
				// Should never happen — placeholderRE only matches
				// digit bodies. Pass through verbatim if it does.
				rebuilt.WriteString(placeholder)
				cursor = h[1]
				continue
			}
			real, ok := r.mapping.Lookup(id)
			if !ok {
				// Trust-minimal: upstream injected a placeholder
				// we never minted. Pass through. The SDK will see
				// the literal placeholder string and the buyer can
				// detect tampering.
				rebuilt.WriteString(placeholder)
			} else {
				rebuilt.WriteString(real)
			}
			cursor = h[1]
		}
		rebuilt.WriteString(s[cursor:])
		s = rebuilt.String()
	}
	// Phase 2: decide how much of the tail to hold back.
	if eof {
		// Flush everything; reset the buffer.
		r.buf.Reset()
		return s
	}
	if cutoff := PartialAtTail(s); cutoff >= 0 {
		emit := s[:cutoff]
		carry := s[cutoff:]
		r.buf.Reset()
		r.buf.WriteString(carry)
		return emit
	}
	// No partial — flush everything, reset buffer.
	r.buf.Reset()
	return s
}
