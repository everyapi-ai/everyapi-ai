package sanitizer

import (
	"strings"
	"testing"
)

func TestMakePlaceholder(t *testing.T) {
	tests := []struct {
		id   int
		want string
	}{
		{1, "<<__EVERYAPI_SECRET_001__>>"},
		{42, "<<__EVERYAPI_SECRET_042__>>"},
		{999, "<<__EVERYAPI_SECRET_999__>>"},
		{1000, "<<__EVERYAPI_SECRET_1000__>>"},
		{12345, "<<__EVERYAPI_SECRET_12345__>>"},
	}
	for _, tc := range tests {
		got := MakePlaceholder(tc.id)
		if got != tc.want {
			t.Errorf("MakePlaceholder(%d) = %q, want %q", tc.id, got, tc.want)
		}
	}
}

func TestFindPlaceholders(t *testing.T) {
	s := "hello <<__EVERYAPI_SECRET_001__>> and <<__EVERYAPI_SECRET_042__>> world"
	got := FindPlaceholders(s)
	if len(got) != 2 {
		t.Fatalf("want 2 placeholders, got %d (%v)", len(got), got)
	}
	if want := "<<__EVERYAPI_SECRET_001__>>"; s[got[0][0]:got[0][1]] != want {
		t.Errorf("first span = %q, want %q", s[got[0][0]:got[0][1]], want)
	}
	if want := "<<__EVERYAPI_SECRET_042__>>"; s[got[1][0]:got[1][1]] != want {
		t.Errorf("second span = %q, want %q", s[got[1][0]:got[1][1]], want)
	}
}

func TestFindPlaceholders_None(t *testing.T) {
	got := FindPlaceholders("nothing to see here")
	if got != nil {
		t.Errorf("want nil, got %v", got)
	}
}

func TestPartialAtTail_NoMatch(t *testing.T) {
	cases := []string{
		"",
		"hello world",
		"<<__EVERYAPI_SECRET_001__>> done",        // complete, not partial
		"this is not the prefix at all",
		"<<__EVERYAPI_NOTSECRET_001__>>",         // literal text using our prefix-ish — not a real partial
	}
	for _, s := range cases {
		if got := PartialAtTail(s); got != -1 {
			t.Errorf("PartialAtTail(%q) = %d, want -1", s, got)
		}
	}
}

func TestPartialAtTail_PrefixPartial(t *testing.T) {
	// Streaming has only delivered the start of the prefix.
	full := PlaceholderPrefix + "001" + PlaceholderSuffix
	for end := 1; end < len(PlaceholderPrefix); end++ {
		prefix := full[:end]
		s := "lorem ipsum " + prefix
		idx := PartialAtTail(s)
		if idx < 0 {
			t.Errorf("PartialAtTail(%q) should detect partial prefix, got -1", s)
			continue
		}
		// Index points to where the prefix starts
		if got := s[idx:]; got != prefix {
			t.Errorf("PartialAtTail(%q) idx=%d gives %q, want %q", s, idx, got, prefix)
		}
	}
}

func TestPartialAtTail_DigitsOnly(t *testing.T) {
	s := "lorem " + PlaceholderPrefix + "12"
	idx := PartialAtTail(s)
	if idx != 6 {
		t.Errorf("PartialAtTail(%q) = %d, want 6", s, idx)
	}
}

func TestPartialAtTail_SuffixPartial(t *testing.T) {
	// Prefix + digits + half of the closing `__>>`.
	for end := 1; end < len(PlaceholderSuffix); end++ {
		s := "lorem " + PlaceholderPrefix + "42" + PlaceholderSuffix[:end]
		idx := PartialAtTail(s)
		if idx != 6 {
			t.Errorf("PartialAtTail(%q) = %d, want 6", s, idx)
		}
	}
}

func TestPartialAtTail_LiteralAfterPrefixIsNotPartial(t *testing.T) {
	// Our prefix appears but the body is non-digit non-suffix text:
	// it's just an unrelated literal, no carryover needed.
	s := "lorem " + PlaceholderPrefix + "hello world"
	if got := PartialAtTail(s); got != -1 {
		t.Errorf("PartialAtTail(%q) = %d, want -1 (literal, not a partial)", s, got)
	}
}

func TestPartialAtTail_CompletePlaceholderAtTailIsNotPartial(t *testing.T) {
	// If the whole placeholder is there, it's a complete match — not
	// a partial. The placeholderRE pass handles complete matches.
	s := "lorem " + PlaceholderPrefix + "001" + PlaceholderSuffix
	if got := PartialAtTail(s); got != -1 {
		t.Errorf("PartialAtTail(%q) = %d, want -1 (complete, not partial)", s, got)
	}
}

func TestPlaceholderPrefixSuffixDistinct(t *testing.T) {
	// Sanity: chosen brackets are sufficiently unusual that they
	// don't appear in normal JSON or chat text.
	if strings.Contains("{}\"\\n", PlaceholderPrefix) || strings.Contains("{}\"\\n", PlaceholderSuffix) {
		t.Errorf("placeholder brackets collide with common JSON chars")
	}
}
