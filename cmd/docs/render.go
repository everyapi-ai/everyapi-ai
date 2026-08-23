package docs

import (
	"os"
	"strings"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/style"
	"golang.org/x/term"
)

// Wrap bounds. Prose is wrapped so a topic stays readable in a narrow pane, but never past maxRenderWidth — long measures are hard to read even when the window is 200 columns wide. A non-TTY (piped into `less`, a file, an agent) gets fallbackWidth so the output is stable and diffable rather than dependent on whoever's terminal happened to run it.
const (
	minRenderWidth = 40
	maxRenderWidth = 96
	fallbackWidth  = 80
	blockIndent    = "  "
)

// terminalWidth is the wrap width for the current stdout, clamped into [minRenderWidth, maxRenderWidth]. One column is left unused so a line that fills the measure doesn't trigger the terminal's own wrap (which would insert a hard break mid-word on some emulators).
func terminalWidth() int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 {
		return fallbackWidth
	}
	w--
	if w < minRenderWidth {
		return minRenderWidth
	}
	if w > maxRenderWidth {
		return maxRenderWidth
	}
	return w
}

// render turns one topic's markdown into terminal-ready text.
//
// This is deliberately a SMALL renderer, not a markdown engine: the input is not user content, it is the handbook under ./topics, which is written to the subset handled here (ATX headings, fenced code, `-`/`1.` lists, pipe tables, block quotes, rules, and the `**bold**` / `code` / [link](url) inline spans). Anything outside that subset falls through as literal text rather than being mangled — the failure mode of an unrecognised construct is "renders as written", never "renders wrong".
//
// Two rules drive the layout decisions:
//   - prose wraps, verbatim blocks do not. Code and tables are indented and passed through untouched, because re-flowing a command someone is about to copy is worse than letting it run past the right edge.
//   - a marked span is one unbreakable token. `everyapi token switch` never wraps in the middle, so an unstyled terminal can't end up showing a stray backtick at a line break.
func render(src string, width int) string {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	var out []string
	var para []string

	flushPara := func() {
		if len(para) == 0 {
			return
		}
		out = append(out, wrap(inlineAtoms(strings.Join(para, " ")), width, "", "")...)
		para = nil
	}
	// blank collapses runs of empty lines to one, and never opens the topic with one.
	blank := func() {
		if len(out) > 0 && out[len(out)-1] != "" {
			out = append(out, "")
		}
	}

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		switch {
		case strings.HasPrefix(trimmed, "```"):
			flushPara()
			blank()
			// Consume through the closing fence. An unterminated fence (the last block in the file) simply runs to EOF instead of erroring.
			for i++; i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "```"); i++ {
				out = append(out, strings.TrimRight(blockIndent+lines[i], " "))
			}
			blank()

		case trimmed == "":
			flushPara()
			blank()

		case isHeading(trimmed):
			flushPara()
			blank()
			out = append(out, heading(trimmed))
			out = append(out, "")

		case isRule(trimmed):
			flushPara()
			blank()
			out = append(out, style.Dim(strings.Repeat("─", width)))
			out = append(out, "")

		case strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|"):
			flushPara()
			blank()
			var rows []string
			for ; i < len(lines); i++ {
				t := strings.TrimSpace(lines[i])
				if !strings.HasPrefix(t, "|") || !strings.HasSuffix(t, "|") {
					break
				}
				rows = append(rows, t)
			}
			i-- // the loop's own i++ re-reads the first non-table line
			out = append(out, renderTable(rows)...)
			blank()

		case strings.HasPrefix(trimmed, "> "):
			flushPara()
			out = append(out, wrap(inlineAtoms(strings.TrimPrefix(trimmed, "> ")), width, style.Dim("│ "), style.Dim("│ "))...)

		case bulletPrefix(line) != "":
			flushPara()
			marker := bulletPrefix(line)
			lead := leadingSpaces(line)
			body := strings.TrimSpace(line)[len(strings.TrimSpace(marker)):]
			first := lead + "• "
			if !strings.HasPrefix(strings.TrimSpace(marker), "-") && !strings.HasPrefix(strings.TrimSpace(marker), "*") {
				first = lead + strings.TrimSpace(marker) + " "
			}
			// Continuation lines align under the item's text, not under its bullet.
			out = append(out, wrap(inlineAtoms(strings.TrimSpace(body)), width, first, strings.Repeat(" ", len([]rune(first))))...)

		default:
			para = append(para, trimmed)
		}
	}
	flushPara()

	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n") + "\n"
}

func isHeading(trimmed string) bool {
	h := strings.TrimLeft(trimmed, "#")
	return len(h) < len(trimmed) && strings.HasPrefix(h, " ")
}

// heading renders an ATX heading. A terminal has one strong affordance (bold) and one weak one (dim), so the three levels the handbook uses map to: h1 bold + underline rule, h2 bold, h3 bold-dim. Deeper levels reuse h3 rather than inventing an invisible distinction.
func heading(trimmed string) string {
	level := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
	text := inline(strings.TrimSpace(strings.TrimLeft(trimmed, "#")))
	switch level {
	case 1:
		return style.Bold(text)
	case 2:
		return style.Bold(text)
	default:
		return style.Dim(style.Bold(text))
	}
}

func isRule(trimmed string) bool {
	if len(trimmed) < 3 {
		return false
	}
	return strings.Trim(trimmed, "-") == "" || strings.Trim(trimmed, "*") == "" || strings.Trim(trimmed, "_") == ""
}

func leadingSpaces(line string) string {
	return line[:len(line)-len(strings.TrimLeft(line, " \t"))]
}

// bulletPrefix returns the list marker at the head of line ("- ", "* ", "1. "), or "" when the line does not start a list item.
func bulletPrefix(line string) string {
	t := strings.TrimSpace(line)
	if strings.HasPrefix(t, "- ") {
		return "- "
	}
	if strings.HasPrefix(t, "* ") && !strings.HasPrefix(t, "**") {
		return "* "
	}
	digits := 0
	for digits < len(t) && t[digits] >= '0' && t[digits] <= '9' {
		digits++
	}
	if digits > 0 && digits+1 < len(t) && t[digits] == '.' && t[digits+1] == ' ' {
		return t[:digits+2]
	}
	return ""
}

// wrap lays rendered atoms into lines of at most width display columns, prefixing the first line with `first` and every continuation with `rest`. An atom wider than the remaining measure gets its own line rather than being split — see the "unbreakable span" rule on render.
func wrap(atoms []string, width int, first, rest string) []string {
	if len(atoms) == 0 {
		return []string{strings.TrimRight(first, " ")}
	}
	prefix := first
	line := prefix
	used := style.Width(prefix)
	var out []string
	for _, a := range atoms {
		w := style.Width(a)
		if used > style.Width(prefix) && used+1+w > width {
			out = append(out, strings.TrimRight(line, " "))
			prefix = rest
			line = prefix
			used = style.Width(prefix)
		}
		if used > style.Width(prefix) {
			line += " "
			used++
		}
		line += a
		used += w
	}
	return append(out, strings.TrimRight(line, " "))
}

// renderTable re-aligns a markdown pipe table to its own widest cell per column. The source alignment is not trusted: a table stays readable after an edit even when nobody re-padded the pipes by hand.
func renderTable(rows []string) []string {
	var cells [][]string
	for _, r := range rows {
		body := strings.TrimSuffix(strings.TrimPrefix(r, "|"), "|")
		var row []string
		for _, c := range strings.Split(body, "|") {
			row = append(row, inline(strings.TrimSpace(c)))
		}
		cells = append(cells, row)
	}
	// Drop the |---|---| separator, identified structurally (every cell is dashes/colons) rather than by position.
	kept := cells[:0]
	sepAt := -1
	for i, row := range cells {
		if isSeparatorRow(row) {
			sepAt = i
			continue
		}
		kept = append(kept, row)
	}
	cells = kept
	if len(cells) == 0 {
		return nil
	}
	widths := map[int]int{}
	for _, row := range cells {
		for i, c := range row {
			if w := style.Width(c); w > widths[i] {
				widths[i] = w
			}
		}
	}
	var out []string
	for i, row := range cells {
		var b strings.Builder
		b.WriteString(blockIndent)
		for j, c := range row {
			if j > 0 {
				b.WriteString("  ")
			}
			b.WriteString(c)
			if j < len(row)-1 {
				b.WriteString(strings.Repeat(" ", widths[j]-style.Width(c)))
			}
		}
		out = append(out, strings.TrimRight(b.String(), " "))
		// A header row (the one the separator followed) gets an underline so the table reads as a table and not as four ragged lines.
		if sepAt == 1 && i == 0 {
			total := 0
			for j := range row {
				total += widths[j]
				if j > 0 {
					total += 2
				}
			}
			out = append(out, blockIndent+style.Dim(strings.Repeat("─", total)))
		}
	}
	return out
}

func isSeparatorRow(row []string) bool {
	for _, c := range row {
		if c == "" || strings.Trim(c, "-: ") != "" {
			return false
		}
	}
	return len(row) > 0
}

// inline renders a markdown span with no wrapping — for headings, table cells, and anywhere the caller owns the line breaks.
func inline(s string) string { return strings.Join(inlineAtoms(s), " ") }

// inlineAtoms splits s at whitespace and renders each token's inline markup, returning tokens that are already terminal-ready (so style.Width measures what the reader sees). A `**bold**`, `code`, or [link](url) span never spans two atoms, which is what makes it unbreakable in wrap.
func inlineAtoms(s string) []string {
	var atoms []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			atoms = append(atoms, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(s); {
		switch {
		case s[i] == ' ' || s[i] == '\t':
			flush()
			i++
		case strings.HasPrefix(s[i:], "**"):
			if end := strings.Index(s[i+2:], "**"); end >= 0 {
				cur.WriteString(style.Bold(s[i+2 : i+2+end]))
				i += 2 + end + 2
				continue
			}
			cur.WriteByte(s[i])
			i++
		case s[i] == '`':
			if end := strings.IndexByte(s[i+1:], '`'); end >= 0 {
				cur.WriteString(renderCode(s[i+1 : i+1+end]))
				i += 1 + end + 1
				continue
			}
			cur.WriteByte(s[i])
			i++
		case s[i] == '[':
			if text, url, n, ok := parseLink(s[i:]); ok {
				cur.WriteString(renderLink(text, url))
				i += n
				continue
			}
			cur.WriteByte(s[i])
			i++
		default:
			cur.WriteByte(s[i])
			i++
		}
	}
	flush()
	return atoms
}

// renderCode styles an inline code span. On a styled terminal the backticks are dropped and bold carries the distinction; on an unstyled one (piped, NO_COLOR) they are KEPT, because dropping them there would erase the only signal that `--api-base` is a flag and not prose.
func renderCode(s string) string {
	if !style.Enabled() {
		return "`" + s + "`"
	}
	return style.Bold(s)
}

// renderLink renders [text](url) as "text (url)" — the URL is kept because a terminal has no click target, and a docs page that says "see the dashboard" without the address is useless in a scrollback. A link whose text already IS the URL renders once.
func renderLink(text, url string) string {
	if text == "" || text == url {
		return url
	}
	return text + " (" + url + ")"
}

// parseLink matches a leading [text](url) and reports the byte length consumed. Nested brackets in the text are not supported — the handbook does not use them, and treating the construct as literal text is the correct fallback.
func parseLink(s string) (text, url string, n int, ok bool) {
	closeIdx := strings.IndexByte(s, ']')
	if closeIdx < 0 || closeIdx+1 >= len(s) || s[closeIdx+1] != '(' {
		return "", "", 0, false
	}
	end := strings.IndexByte(s[closeIdx+2:], ')')
	if end < 0 {
		return "", "", 0, false
	}
	return s[1:closeIdx], s[closeIdx+2 : closeIdx+2+end], closeIdx + 2 + end + 1, true
}
