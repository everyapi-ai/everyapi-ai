package docs

import (
	"embed"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// topicFS carries the handbook into the binary, so `everyapi docs` answers on a plane, behind a firewall, or before the user has ever logged in. Editing a topic is editing the .md file — there is no second copy of the text in Go.
//
//go:embed topics/*.md
var topicFS embed.FS

// topicOrder is the reading order of the handbook — an intended path from "what is this" to "run it yourself", not an alphabetical dump. `docs list` and the interactive picker both render it in this order.
//
// A slug listed here with no matching file (or a file with no entry here) fails TestTopicOrderMatchesFiles rather than silently disappearing from the list.
var topicOrder = []string{
	"overview",
	"quickstart",
	"api",
	"models",
	"tokens",
	"cli",
	"use",
	"billing",
	"mcp",
	"proxy",
	"computer",
	"artifacts",
	"desktop",
	"dashboard",
	"seller",
	"edge",
	"self-hosting",
	"troubleshooting",
}

// Topic is one handbook page: its slug (what the user types), the title and summary parsed out of the markdown, and the raw body.
type Topic struct {
	Slug    string
	Title   string
	Summary string
	Body    string
}

var (
	loadOnce sync.Once
	loaded   []Topic
	loadErr  error
)

// topics returns the parsed handbook. Parsing is one pass over embedded bytes with no I/O, done once per process.
func topics() ([]Topic, error) {
	loadOnce.Do(func() {
		for _, slug := range topicOrder {
			raw, err := topicFS.ReadFile("topics/" + slug + ".md")
			if err != nil {
				loadErr = fmt.Errorf("docs topic %q is registered but not embedded: %w", slug, err)
				return
			}
			title, summary := frontMatter(string(raw))
			loaded = append(loaded, Topic{Slug: slug, Title: title, Summary: summary, Body: string(raw)})
		}
	})
	return loaded, loadErr
}

// frontMatter pulls the display title and one-line summary out of a topic without a metadata header: the title is the leading `# ` heading, the summary the first paragraph under it, collapsed to a single line. Keeping both IN the markdown means the file renders correctly on GitHub and in the terminal from one source.
func frontMatter(body string) (title, summary string) {
	lines := strings.Split(body, "\n")
	i := 0
	for ; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "# ") {
			title = strings.TrimSpace(strings.TrimPrefix(lines[i], "# "))
			i++
			break
		}
	}
	var para []string
	for ; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if t == "" {
			if len(para) > 0 {
				break
			}
			continue
		}
		para = append(para, t)
	}
	return title, strings.Join(para, " ")
}

// resolve maps what the user typed to exactly one topic. Three passes, narrowest first: exact slug, then unique prefix, then unique substring across slug and title. An ambiguous match reports the candidates instead of guessing — `everyapi docs se` should not silently pick between `seller` and `self-hosting`.
func resolve(query string) (Topic, error) {
	all, err := topics()
	if err != nil {
		return Topic{}, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	for _, t := range all {
		if t.Slug == q {
			return t, nil
		}
	}
	if m := matching(all, func(t Topic) bool { return strings.HasPrefix(t.Slug, q) }); len(m) == 1 {
		return m[0], nil
	} else if len(m) > 1 {
		return Topic{}, ambiguous(query, m)
	}
	m := matching(all, func(t Topic) bool {
		return strings.Contains(t.Slug, q) || strings.Contains(strings.ToLower(t.Title), q)
	})
	switch len(m) {
	case 1:
		return m[0], nil
	case 0:
		return Topic{}, fmt.Errorf("no docs topic matches %q — run 'everyapi docs list' to see them all", query)
	default:
		return Topic{}, ambiguous(query, m)
	}
}

func matching(all []Topic, pred func(Topic) bool) []Topic {
	var out []Topic
	for _, t := range all {
		if pred(t) {
			out = append(out, t)
		}
	}
	return out
}

func ambiguous(query string, m []Topic) error {
	slugs := make([]string, len(m))
	for i, t := range m {
		slugs[i] = t.Slug
	}
	return fmt.Errorf("%q matches %d topics (%s) — be more specific", query, len(m), strings.Join(slugs, ", "))
}

// hit is one topic's search result: the number of matching lines and a bounded sample of them, each with its 1-based line number so `docs <topic>` output can be scanned for it.
type hit struct {
	Topic Topic
	Count int
	Lines []hitLine
}

type hitLine struct {
	Number int
	Text   string
}

// maxHitLines bounds the sample per topic. A query like "api" legitimately matches a hundred lines; printing all of them buries the one topic the user wanted under the one they didn't.
const maxHitLines = 4

// search runs a case-insensitive substring scan over every topic body and returns the topics that matched, most matches first. Fenced-code markers and heading hashes are kept in the sampled text — the line is shown as it appears in the topic, so it can be found again.
func search(query string) ([]hit, error) {
	all, err := topics()
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(query)
	var hits []hit
	for _, t := range all {
		h := hit{Topic: t}
		for n, line := range strings.Split(t.Body, "\n") {
			if !strings.Contains(strings.ToLower(line), q) {
				continue
			}
			h.Count++
			if len(h.Lines) < maxHitLines {
				h.Lines = append(h.Lines, hitLine{Number: n + 1, Text: strings.TrimSpace(line)})
			}
		}
		if h.Count > 0 {
			hits = append(hits, h)
		}
	}
	// Stable sort on count keeps topicOrder as the tie-break, so equally-relevant topics still come back in reading order.
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Count > hits[j].Count })
	return hits, nil
}

// Topics returns the parsed handbook. Exported for the root package's test that every registered command is documented — see TestEveryCommandIsDocumented in main_test.go. The handbook has no other reason to be readable from outside this package.
func Topics() ([]Topic, error) { return topics() }
