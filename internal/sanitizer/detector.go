package sanitizer

import (
	"regexp"
	"sort"
)

// Match is one detector hit inside a string: byte range and the
// matched literal text. The proxy uses both — the literal goes into
// the mapping table, and the byte range tells the outbound rewrite
// where to splice in the placeholder.
type Match struct {
	Start int
	End   int
	Value string
	// DetectorName is the rule that fired. Surfaced in debug logs
	// and (eventually) the `everyapi proxy status` audit feed so a
	// curious buyer can see which detector caught what.
	DetectorName string
}

// Detector is the contract every built-in or user-defined sanitizer
// rule satisfies. It returns matches in source order; overlapping
// matches across detectors are resolved in Scan (longest-first wins
// when ranges overlap, which avoids `sk-ant-…` being detected as
// `sk-…` separately).
type Detector interface {
	Name() string
	// Find scans s and returns matches in source order. Implementations
	// must not return overlapping spans from a single detector — Scan
	// only de-overlaps across detectors, not within.
	Find(s string) []Match
}

// RegexDetector is the workhorse. Most sensitive-data formats are
// well captured by a single anchored regex, with optional secondary
// validation (Luhn for cards, ISO-7064 for CN IDs) layered on via
// Validate.
type RegexDetector struct {
	name string
	re   *regexp.Regexp
	// Validate, when non-nil, is called for each candidate match;
	// returning false drops the match. Used for detectors where a
	// regex finds the SHAPE but a checksum confirms the IDENTITY
	// (Luhn for credit cards, mod-11 for Chinese ID).
	Validate func(s string) bool
}

// NewRegexDetector returns a regex-based detector. The pattern is
// compiled once at construction; subsequent Find calls reuse the
// compiled object.
func NewRegexDetector(name, pattern string) *RegexDetector {
	return &RegexDetector{name: name, re: regexp.MustCompile(pattern)}
}

// Name implements Detector.
func (d *RegexDetector) Name() string { return d.name }

// Find implements Detector.
func (d *RegexDetector) Find(s string) []Match {
	hits := d.re.FindAllStringIndex(s, -1)
	if len(hits) == 0 {
		return nil
	}
	out := make([]Match, 0, len(hits))
	for _, h := range hits {
		val := s[h[0]:h[1]]
		if d.Validate != nil && !d.Validate(val) {
			continue
		}
		out = append(out, Match{
			Start:        h[0],
			End:          h[1],
			Value:        val,
			DetectorName: d.name,
		})
	}
	return out
}

// Scan runs every detector against s and returns the merged set of
// non-overlapping matches, sorted by Start.
//
// When two matches overlap, the LONGER one wins. This is critical for
// hierarchical formats — `sk-ant-foo` would otherwise be flagged by
// BOTH the OpenAI `sk-…` detector and the Anthropic `sk-ant-…`
// detector, and the proxy would try to replace the same bytes twice,
// producing garbled output. Longest-match-wins ensures the more
// specific detector takes precedence.
//
// On ties (same Start, same End) the detector registered earlier in
// the slice wins, so the registration order in detectors_builtin.go
// is deterministic.
func Scan(s string, detectors []Detector) []Match {
	var all []Match
	for _, d := range detectors {
		all = append(all, d.Find(s)...)
	}
	if len(all) == 0 {
		return nil
	}
	// Longest first; within equal-length, earlier Start first;
	// within equal-length-equal-Start, registration order (stable).
	sort.SliceStable(all, func(i, j int) bool {
		li := all[i].End - all[i].Start
		lj := all[j].End - all[j].Start
		if li != lj {
			return li > lj
		}
		return all[i].Start < all[j].Start
	})
	// Walk longest-first; keep a match only if its byte range doesn't
	// overlap any already-kept one.
	type span struct{ start, end int }
	kept := make([]Match, 0, len(all))
	var occupied []span
	for _, m := range all {
		overlap := false
		for _, sp := range occupied {
			if m.Start < sp.end && sp.start < m.End {
				overlap = true
				break
			}
		}
		if overlap {
			continue
		}
		kept = append(kept, m)
		occupied = append(occupied, span{m.Start, m.End})
	}
	// Final return must be in source order so callers can splice
	// from left to right without recomputing offsets.
	sort.Slice(kept, func(i, j int) bool { return kept[i].Start < kept[j].Start })
	return kept
}

// ReplaceWith applies the placeholder for each match in source order,
// returning the rewritten string. Matches are expected to be
// non-overlapping (Scan guarantees this).
func ReplaceWith(s string, matches []Match, m *Mapping) string {
	if len(matches) == 0 {
		return s
	}
	out := make([]byte, 0, len(s))
	cursor := 0
	for _, hit := range matches {
		out = append(out, s[cursor:hit.Start]...)
		out = append(out, m.PutOrGet(hit.Value)...)
		cursor = hit.End
	}
	out = append(out, s[cursor:]...)
	return string(out)
}
