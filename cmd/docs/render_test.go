package docs

import (
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/style"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/styletest"
	"github.com/muesli/termenv"
)

// unstyled pins lipgloss to the Ascii profile, which is what a piped `everyapi docs api > file` gets. Rendering is asserted against plain text so the expectations stay readable and don't encode ANSI byte sequences.
func unstyled(t *testing.T) {
	t.Helper()
	styletest.WithColorProfile(t, termenv.Ascii)
}

func TestRenderHeadingsDropTheirHashes(t *testing.T) {
	unstyled(t)
	got := render("# Title\n\n## Section\n\n### Detail\n", 60)
	for _, want := range []string{"Title", "Section", "Detail"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing heading %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "#") {
		t.Errorf("heading hashes survived rendering:\n%s", got)
	}
}

// TestRenderCodeBlockIsVerbatim is the rule that matters most for a docs command: a reader is about to copy that line into a shell. Indentation is added, nothing else may change — no wrapping, no inline markup, no backtick stripping.
func TestRenderCodeBlockIsVerbatim(t *testing.T) {
	unstyled(t)
	src := "```\neveryapi token create --name a-very-long-name --quota 1000000 --group byteplus\n```\n"
	got := render(src, 40)
	want := blockIndent + "everyapi token create --name a-very-long-name --quota 1000000 --group byteplus"
	if !strings.Contains(got, want) {
		t.Errorf("code line was altered.\ngot:\n%s\nwant to contain:\n%s", got, want)
	}
	if strings.Contains(got, "```") {
		t.Errorf("fence markers survived:\n%s", got)
	}
}

func TestRenderWrapsProseToWidth(t *testing.T) {
	unstyled(t)
	src := strings.Repeat("word ", 60)
	for _, line := range strings.Split(render(src, 40), "\n") {
		if style.Width(line) > 40 {
			t.Errorf("line is %d columns, want <= 40: %q", style.Width(line), line)
		}
	}
}

// TestRenderNeverSplitsAMarkedSpan is why wrap() operates on rendered atoms rather than on words. Splitting `everyapi token switch` at a space would leave a line ending in a lone backtick on an unstyled terminal — visible nonsense the reader would try to type.
func TestRenderNeverSplitsAMarkedSpan(t *testing.T) {
	unstyled(t)
	got := render("aaaa bbbb cccc `everyapi token switch` dddd\n", 24)
	if !strings.Contains(got, "`everyapi token switch`") {
		t.Errorf("code span was split across lines:\n%s", got)
	}
	for _, line := range strings.Split(got, "\n") {
		if n := strings.Count(line, "`"); n%2 != 0 {
			t.Errorf("line has an unbalanced backtick: %q", line)
		}
	}
}

// TestInlineCodeKeepsBackticksOnlyWhenUnstyled: bold carries the distinction where there is bold, and the backticks carry it where there is not. Dropping them in both cases would erase the only signal that --api-base is a flag.
func TestInlineCodeKeepsBackticksOnlyWhenUnstyled(t *testing.T) {
	unstyled(t)
	if got := inline("pass `--api-base` to it"); !strings.Contains(got, "`--api-base`") {
		t.Errorf("unstyled inline code lost its backticks: %q", got)
	}
	styletest.WithColorProfile(t, termenv.TrueColor)
	got := inline("pass `--api-base` to it")
	if strings.Contains(got, "`") {
		t.Errorf("styled inline code kept its backticks: %q", got)
	}
	if !strings.Contains(got, "--api-base") {
		t.Errorf("styled inline code lost its text: %q", got)
	}
}

func TestRenderBoldMarkersAreConsumed(t *testing.T) {
	unstyled(t)
	got := render("this is **important** text\n", 60)
	if strings.Contains(got, "**") {
		t.Errorf("bold markers survived:\n%s", got)
	}
	if !strings.Contains(got, "important") {
		t.Errorf("bold text was dropped:\n%s", got)
	}
}

func TestRenderBullets(t *testing.T) {
	unstyled(t)
	got := render("- first item\n- second item\n", 60)
	if strings.Count(got, "•") != 2 {
		t.Errorf("want two bullets:\n%s", got)
	}
	if strings.Contains(got, "- first") {
		t.Errorf("dash marker survived:\n%s", got)
	}
}

// TestRenderBulletContinuationAligns keeps a wrapped list item hanging under its own text rather than under the bullet, so the list structure survives narrow terminals.
func TestRenderBulletContinuationAligns(t *testing.T) {
	unstyled(t)
	lines := strings.Split(strings.TrimRight(render("- "+strings.Repeat("word ", 20)+"\n", 30), "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected the item to wrap:\n%v", lines)
	}
	for _, l := range lines[1:] {
		if !strings.HasPrefix(l, "  ") {
			t.Errorf("continuation %q is not indented under the item text", l)
		}
	}
}

func TestRenderTableAlignsAndDropsTheSeparator(t *testing.T) {
	unstyled(t)
	got := render("| A | Long header |\n| --- | --- |\n| x | y |\n", 80)
	if strings.Contains(got, "|") {
		t.Errorf("pipes survived:\n%s", got)
	}
	if strings.Contains(got, "---") {
		t.Errorf("separator row survived:\n%s", got)
	}
	var body string
	for _, l := range strings.Split(got, "\n") {
		if strings.Contains(l, "x") {
			body = l
		}
	}
	// "A" is padded to the width of "Long header"'s column neighbour, so the y column starts where "Long header" does.
	header := ""
	for _, l := range strings.Split(got, "\n") {
		if strings.Contains(l, "Long header") {
			header = l
		}
	}
	if header == "" || body == "" {
		t.Fatalf("missing header or body row:\n%s", got)
	}
	if strings.Index(header, "Long header") != strings.Index(body, "y") {
		t.Errorf("columns are not aligned:\n%q\n%q", header, body)
	}
}

func TestRenderRule(t *testing.T) {
	unstyled(t)
	got := render("above\n\n---\n\nbelow\n", 20)
	if !strings.Contains(got, strings.Repeat("─", 20)) {
		t.Errorf("horizontal rule not rendered at width:\n%s", got)
	}
}

func TestRenderBlockquote(t *testing.T) {
	unstyled(t)
	got := render("> careful here\n", 60)
	if !strings.Contains(got, "│ careful here") {
		t.Errorf("blockquote marker missing:\n%s", got)
	}
}

func TestParseLink(t *testing.T) {
	t.Parallel()
	text, url, n, ok := parseLink("[docs](https://everyapi.ai/) rest")
	if !ok {
		t.Fatal("did not parse a well-formed link")
	}
	if text != "docs" || url != "https://everyapi.ai/" {
		t.Errorf("text=%q url=%q", text, url)
	}
	if n != len("[docs](https://everyapi.ai/)") {
		t.Errorf("consumed %d bytes", n)
	}
	// A bare bracket is not a link and must fall through as literal text.
	if _, _, _, ok := parseLink("[not a link] here"); ok {
		t.Error("parsed a non-link as a link")
	}
}

func TestRenderLinkKeepsTheURL(t *testing.T) {
	unstyled(t)
	got := render("see [the site](https://everyapi.ai)\n", 80)
	if !strings.Contains(got, "https://everyapi.ai") {
		t.Errorf("URL was dropped — a terminal has no click target:\n%s", got)
	}
	// A link whose text is the URL renders once, not twice.
	if strings.Count(render("[https://everyapi.ai](https://everyapi.ai)\n", 80), "https://everyapi.ai") != 1 {
		t.Error("a self-titled link rendered its URL twice")
	}
}

func TestBulletPrefix(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"- item":               "- ",
		"  * item":             "* ",
		"1. item":              "1. ",
		"12. item":             "12. ",
		"**bold** at line":     "",
		"not a list":           "",
		"-no space after dash": "",
	}
	for in, want := range cases {
		if got := bulletPrefix(in); got != want {
			t.Errorf("bulletPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestEveryTopicRendersWithoutMarkupLeaking is the end-to-end guard: every shipped page, at a narrow measure, must come out with no stray heading hashes, fences, pipes, or bold markers. It is what catches a new page written in a construct the renderer does not handle.
func TestEveryTopicRendersWithoutMarkupLeaking(t *testing.T) {
	unstyled(t)
	all, err := topics()
	if err != nil {
		t.Fatalf("topics: %v", err)
	}
	for _, top := range all {
		out := render(top.Body, 60)
		for _, bad := range []string{"```", "**", "| ---"} {
			if strings.Contains(out, bad) {
				t.Errorf("%s: %q leaked into rendered output", top.Slug, bad)
			}
		}
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, "#") {
				t.Errorf("%s: unrendered heading %q", top.Slug, line)
			}
		}
	}
}

// TestEveryTopicFitsTheFallbackMeasure guards the one class of layout defect the renderer cannot fix for you. Prose wraps and tables are re-aligned, but a fenced code block is emitted verbatim by design — so an over-wide command line in a topic soft-wraps in the reader's terminal and the block stops looking like a block. fallbackWidth is the measure a piped or non-TTY run gets, and the narrowest a real terminal is likely to be.
func TestEveryTopicFitsTheFallbackMeasure(t *testing.T) {
	unstyled(t)
	all, err := topics()
	if err != nil {
		t.Fatalf("topics: %v", err)
	}
	for _, top := range all {
		for _, line := range strings.Split(render(top.Body, fallbackWidth), "\n") {
			if w := style.Width(line); w > fallbackWidth {
				t.Errorf("%s: line is %d columns, want <= %d — shorten the code block or table row:\n%s",
					top.Slug, w, fallbackWidth, line)
			}
		}
	}
}

func TestTerminalWidthIsClamped(t *testing.T) {
	t.Parallel()
	// In `go test` stdout is not a terminal, so this exercises the non-TTY path.
	w := terminalWidth()
	if w < minRenderWidth || w > maxRenderWidth {
		t.Errorf("terminalWidth() = %d, outside [%d, %d]", w, minRenderWidth, maxRenderWidth)
	}
}
