package docs

import (
	"bytes"
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/styletest"
	"github.com/muesli/termenv"
)

// capture redirects cliout for one test and returns what the command printed. cliout.Out is process-global, so these tests do not run in parallel with each other.
func capture(t *testing.T) *bytes.Buffer {
	t.Helper()
	styletest.WithColorProfile(t, termenv.Ascii)
	var buf bytes.Buffer
	prev := cliout.Out
	cliout.Out = &buf
	t.Cleanup(func() { cliout.Out = prev })
	return &buf
}

func TestRunHelp(t *testing.T) {
	for _, arg := range []string{"help", "--help", "-h"} {
		out := capture(t)
		if err := Run([]string{arg}); err != nil {
			t.Fatalf("docs %s: %v", arg, err)
		}
		if !strings.Contains(out.String(), "everyapi docs") {
			t.Errorf("docs %s printed no usage: %q", arg, out.String())
		}
	}
}

func TestRunListShowsEveryTopic(t *testing.T) {
	out := capture(t)
	if err := Run([]string{"list"}); err != nil {
		t.Fatalf("docs list: %v", err)
	}
	for _, slug := range topicOrder {
		if !strings.Contains(out.String(), slug) {
			t.Errorf("docs list omitted %q", slug)
		}
	}
}

// TestRunListOrderMatchesTopicOrder: the list is a reading path, not an alphabetical dump, so its order is part of the contract.
func TestRunListOrderMatchesTopicOrder(t *testing.T) {
	out := capture(t)
	if err := Run([]string{"list"}); err != nil {
		t.Fatalf("docs list: %v", err)
	}
	body := out.String()
	prev := -1
	for _, slug := range topicOrder {
		at := strings.Index(body, "\n  "+slug)
		if at < 0 {
			t.Fatalf("no row for %q", slug)
		}
		if at < prev {
			t.Errorf("%q appears out of reading order", slug)
		}
		prev = at
	}
}

// TestRunTopicNeedsNoShowVerb: `everyapi docs billing` is the common case; requiring `docs show billing` would be a verb nobody would guess.
func TestRunTopicNeedsNoShowVerb(t *testing.T) {
	out := capture(t)
	if err := Run([]string{"billing"}); err != nil {
		t.Fatalf("docs billing: %v", err)
	}
	if !strings.Contains(out.String(), "quota") {
		t.Errorf("docs billing did not render the topic:\n%s", out.String())
	}
}

func TestRunTopicAcceptsAnAbbreviation(t *testing.T) {
	out := capture(t)
	if err := Run([]string{"quick"}); err != nil {
		t.Fatalf("docs quick: %v", err)
	}
	if !strings.Contains(out.String(), "Quickstart") {
		t.Errorf("abbreviation did not resolve:\n%s", out.String())
	}
}

func TestRunUnknownTopicErrors(t *testing.T) {
	capture(t)
	err := Run([]string{"nonexistent-topic-name"})
	if err == nil {
		t.Fatal("unknown topic returned no error")
	}
	if !strings.Contains(err.Error(), "docs list") {
		t.Errorf("error %q does not tell the user how to find the real topics", err)
	}
}

func TestRunSearch(t *testing.T) {
	out := capture(t)
	if err := Run([]string{"search", "routing", "group"}); err != nil {
		t.Fatalf("docs search: %v", err)
	}
	body := out.String()
	if !strings.Contains(body, "models") && !strings.Contains(body, "tokens") {
		t.Errorf("search for a multi-word query found nothing plausible:\n%s", body)
	}
}

// TestRunSearchWithNoQueryIsNotAHang: piped or scripted, an empty query has to fail with usage rather than block on a prompt nobody can answer.
func TestRunSearchWithNoQueryIsNotAHang(t *testing.T) {
	capture(t)
	err := Run([]string{"search"})
	if err == nil {
		t.Fatal("empty search returned no error in a non-interactive run")
	}
	if !strings.Contains(err.Error(), "everyapi docs search") {
		t.Errorf("error %q is not the usage line", err)
	}
}

func TestRunSearchMissReportsCleanly(t *testing.T) {
	out := capture(t)
	if err := Run([]string{"search", "zzzz-not-in-any-topic-zzzz"}); err != nil {
		t.Fatalf("a search miss should not be an error: %v", err)
	}
	if !strings.Contains(out.String(), "zzzz-not-in-any-topic-zzzz") {
		t.Errorf("miss message does not quote the query:\n%s", out.String())
	}
}

// TestRunBareIsListWhenNotInteractive: `everyapi docs | head` must produce the list, not sit on a picker.
func TestRunBareIsListWhenNotInteractive(t *testing.T) {
	out := capture(t)
	if err := Run(nil); err != nil {
		t.Fatalf("docs: %v", err)
	}
	if !strings.Contains(out.String(), "quickstart") {
		t.Errorf("bare docs did not fall back to the list:\n%s", out.String())
	}
}

func TestTruncate(t *testing.T) {
	t.Parallel()
	if got := truncate("short", 40); got != "short" {
		t.Errorf("truncate padded or cut a short string: %q", got)
	}
	got := truncate(strings.Repeat("x", 100), 20)
	if len([]rune(got)) > 20 {
		t.Errorf("truncate returned %d runes for a 20-column budget: %q", len([]rune(got)), got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncate did not mark the cut: %q", got)
	}
	// A CJK string is measured in columns, not runes: 20 columns is 10 wide characters at most.
	cjk := truncate(strings.Repeat("文", 40), 20)
	if width := len([]rune(cjk)); width > 11 {
		t.Errorf("truncate measured CJK by rune count, not display width: %q", cjk)
	}
}

// TestOpenIsAVerbAgain: `docs open` must dispatch, not fall through to topic resolution and fail as an unknown topic.
func TestOpenIsAVerbAgain(t *testing.T) {
	out := capture(t)
	// runOpen prints the URL and then best-effort hands it to the OS launcher; a
	// missing launcher is deliberately not an error, so this asserts the printed
	// URL rather than the browser call.
	if err := Run([]string{"open"}); err != nil {
		t.Fatalf("docs open: %v", err)
	}
	if !strings.Contains(out.String(), SiteURL) {
		t.Errorf("docs open did not print the site URL:\n%s", out.String())
	}
}

// TestSiteURLIsTheDeployedHost pins the value rather than its shape. The
// previous version of this test only checked the prefix was https, which is
// exactly how a hostname that never resolved rode along for six commits. The
// cross-check that this matches what is actually deployed lives in
// infra/docs-site, which can read both this constant and the wrangler config;
// the CLI must not depend on that directory existing.
func TestSiteURLIsTheDeployedHost(t *testing.T) {
	t.Parallel()
	if SiteURL != "https://docs.everyapi.ai/" {
		t.Errorf("SiteURL = %q; if the docs deployment moved, update infra/docs-site/wrangler.jsonc with it", SiteURL)
	}
}
