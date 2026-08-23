package docs

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// TestTopicOrderMatchesEmbeddedFiles is the guard that keeps the registry and the directory in sync in BOTH directions. Adding topics/foo.md without listing it makes the page invisible — no error, just a page nobody can reach. Listing a slug with no file fails at first read for every user. Neither is caught by the compiler.
func TestTopicOrderMatchesEmbeddedFiles(t *testing.T) {
	t.Parallel()
	entries, err := fs.Glob(topicFS, "topics/*.md")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	onDisk := map[string]bool{}
	for _, e := range entries {
		onDisk[strings.TrimSuffix(strings.TrimPrefix(e, "topics/"), ".md")] = true
	}
	registered := map[string]bool{}
	for _, slug := range topicOrder {
		if registered[slug] {
			t.Errorf("topicOrder lists %q twice", slug)
		}
		registered[slug] = true
		if !onDisk[slug] {
			t.Errorf("topicOrder lists %q but topics/%s.md is not embedded", slug, slug)
		}
	}
	for slug := range onDisk {
		if !registered[slug] {
			t.Errorf("topics/%s.md is embedded but missing from topicOrder — it would be unreachable", slug)
		}
	}
}

func TestEveryTopicHasTitleAndSummary(t *testing.T) {
	t.Parallel()
	all, err := topics()
	if err != nil {
		t.Fatalf("topics: %v", err)
	}
	if len(all) != len(topicOrder) {
		t.Fatalf("loaded %d topics, want %d", len(all), len(topicOrder))
	}
	for _, top := range all {
		if top.Title == "" {
			t.Errorf("%s: no '# ' heading, so `docs list` would show a blank title", top.Slug)
		}
		if len(top.Summary) < 20 {
			t.Errorf("%s: summary %q is too short to be the first paragraph", top.Slug, top.Summary)
		}
		if strings.Contains(top.Title, "\n") {
			t.Errorf("%s: title spans lines", top.Slug)
		}
	}
}

// crossRefRe matches the handbook's own "see the `x` topic" convention. Every such reference must resolve, or a reader is sent to a page that does not exist.
var crossRefRe = regexp.MustCompile("the `([a-z-]+)` topic")

func TestCrossReferencesResolve(t *testing.T) {
	t.Parallel()
	all, err := topics()
	if err != nil {
		t.Fatalf("topics: %v", err)
	}
	known := map[string]bool{}
	for _, top := range all {
		known[top.Slug] = true
	}
	for _, top := range all {
		for _, m := range crossRefRe.FindAllStringSubmatch(top.Body, -1) {
			if !known[m[1]] {
				t.Errorf("%s references the %q topic, which does not exist", top.Slug, m[1])
			}
		}
	}
}

// TestNoUnclosedCodeFence catches the copy-paste failure that would swallow the rest of a page: render() runs an unterminated fence to EOF, so one stray ``` turns everything after it into a code block.
func TestNoUnclosedCodeFence(t *testing.T) {
	t.Parallel()
	all, err := topics()
	if err != nil {
		t.Fatalf("topics: %v", err)
	}
	for _, top := range all {
		n := 0
		for _, line := range strings.Split(top.Body, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				n++
			}
		}
		if n%2 != 0 {
			t.Errorf("%s: %d code fences — one is unclosed", top.Slug, n)
		}
	}
}

func TestResolve(t *testing.T) {
	t.Parallel()
	cases := []struct {
		query string
		want  string
	}{
		{"cli", "cli"},                      // exact, even though "cli" prefixes nothing else
		{"quick", "quickstart"},             // unique prefix
		{"QUICKSTART", "quickstart"},        // case-insensitive
		{"  api  ", "api"},                  // trimmed
		{"troubleshoot", "troubleshooting"}, // unique prefix
		{"hosting", "self-hosting"},         // substring, not a prefix
	}
	for _, c := range cases {
		got, err := resolve(c.query)
		if err != nil {
			t.Errorf("resolve(%q): %v", c.query, err)
			continue
		}
		if got.Slug != c.want {
			t.Errorf("resolve(%q) = %q, want %q", c.query, got.Slug, c.want)
		}
	}
}

// TestResolveRefusesToGuess is the point of the three-pass lookup: "se" is a legitimate prefix of both seller and self-hosting, and silently opening one of them would be worse than saying so.
func TestResolveRefusesToGuess(t *testing.T) {
	t.Parallel()
	_, err := resolve("se")
	if err == nil {
		t.Fatal("resolve(\"se\") picked a topic; want an ambiguity error")
	}
	for _, want := range []string{"seller", "self-hosting"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ambiguity error %q does not name %q", err, want)
		}
	}
}

func TestResolveUnknown(t *testing.T) {
	t.Parallel()
	_, err := resolve("kubernetes-operator")
	if err == nil {
		t.Fatal("resolve of an unknown topic returned no error")
	}
	if !strings.Contains(err.Error(), "everyapi docs list") {
		t.Errorf("unknown-topic error %q does not point at the list command", err)
	}
}

func TestSearchRanksByMatchCountAndCapsSamples(t *testing.T) {
	t.Parallel()
	hits, err := search("everyapi")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) < 5 {
		t.Fatalf("search(%q) found %d topics; the term is in nearly every page", "everyapi", len(hits))
	}
	for i := 1; i < len(hits); i++ {
		if hits[i].Count > hits[i-1].Count {
			t.Fatalf("hits are not ordered by match count: %s(%d) before %s(%d)",
				hits[i-1].Topic.Slug, hits[i-1].Count, hits[i].Topic.Slug, hits[i].Count)
		}
	}
	for _, h := range hits {
		if len(h.Lines) > maxHitLines {
			t.Errorf("%s: %d sampled lines exceeds the cap of %d", h.Topic.Slug, len(h.Lines), maxHitLines)
		}
		if h.Count < len(h.Lines) {
			t.Errorf("%s: count %d is below the %d sampled lines", h.Topic.Slug, h.Count, len(h.Lines))
		}
	}
}

func TestSearchIsCaseInsensitiveAndCanMiss(t *testing.T) {
	t.Parallel()
	upper, err := search("TRANSPARENT MODE")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	lower, _ := search("transparent mode")
	if len(upper) != len(lower) || len(upper) == 0 {
		t.Fatalf("case-insensitive search disagreed: %d vs %d", len(upper), len(lower))
	}
	none, err := search("zzzz-not-in-any-topic-zzzz")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("search for a nonsense term matched %d topics", len(none))
	}
}

func TestFrontMatter(t *testing.T) {
	t.Parallel()
	title, summary := frontMatter("# Title here\n\nFirst paragraph line one,\nline two.\n\nSecond paragraph.\n")
	if title != "Title here" {
		t.Errorf("title = %q", title)
	}
	if summary != "First paragraph line one, line two." {
		t.Errorf("summary = %q", summary)
	}
}
