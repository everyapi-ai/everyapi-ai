package cliprompt

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
)

// ErrPickCancelled is returned by Pick when the user hits Ctrl-C
// during the arrow-key picker. Callers can errors.Is-match on it to
// suppress a generic "selection failed" wrapper and exit cleanly.
var ErrPickCancelled = errors.New("pick cancelled")

// Pick renders an arrow-navigable list and returns the chosen index
// (zero-based). Falls back to a number-entry prompt automatically
// when either stdin or stdout isn't a TTY — keeps the CLI scriptable
// in CI / piped contexts without each caller re-rolling the path.
//
// Interactive (TTY) path is delegated to charmbracelet/huh's Select;
// the fallback prints a numbered list and reads one line.
//
// Picker always opens with the first row highlighted. Use
// PickWithSelected when the caller wants to restore a previous
// selection — typical for a loop that re-shows the same menu after
// dispatching the chosen action.
func Pick(prompt string, items []string) (int, error) {
	return PickWithSelected(prompt, items, 0)
}

// PickWithSelected is the stateful variant of Pick: starts with row
// `initial` highlighted instead of always row 0. The launcher uses
// this so Esc-ing back into a menu restores the cursor to the row
// the user just visited, rather than punting them back to the top.
//
// Out-of-range initial values clamp to 0 — never returns an error
// just because the caller passed a stale index after the items
// slice shrank.
func PickWithSelected(prompt string, items []string, initial int) (int, error) {
	if len(items) == 0 {
		return -1, errors.New("nothing to pick from")
	}
	if !isInteractive() {
		return pickByNumber(prompt, items)
	}
	if initial < 0 || initial >= len(items) {
		initial = 0
	}
	return pickViaHuh(prompt, items, initial)
}

// readStdinLine reads one line from os.Stdin WITHOUT reading past the
// newline. The numeric pickers are throwaway, per-call readers, so a
// buffering bufio.Reader would read-ahead a whole chunk and then discard
// the bytes after the newline — corrupting any caller that reads
// os.Stdin again afterwards. That breaks scripted (piped) flows like
// `proxy configure`, which runs PickMany and then reads further answers
// (YesNo/Line) from its own reader. fmt.Scanln had this no-read-ahead
// property but stopped at the first whitespace; reading byte-by-byte
// keeps the whole line while leaving the rest of stdin intact. Returns
// io.EOF (with any partial line) at end of stream.
func readStdinLine() (string, error) {
	var b []byte
	var one [1]byte
	for {
		n, err := os.Stdin.Read(one[:])
		if n > 0 {
			if one[0] == '\n' {
				return string(b), nil
			}
			b = append(b, one[0])
		}
		if err != nil {
			return string(b), err
		}
	}
}

func pickByNumber(prompt string, items []string) (int, error) {
	cliout.Println(prompt)
	for i, n := range items {
		cliout.Printf("  %d) %s\n", i+1, n)
	}
	cliout.Printf("%s", i18n.T("cliprompt.pick_enter_name_number"))
	// Read the whole line (not fmt.Scanln, which stops at the first
	// whitespace) so multi-token answers survive. A non-empty line
	// terminated by EOF (no trailing newline) still parses; only a
	// genuinely empty EOF is a read failure.
	choice, err := readStdinLine()
	if err != nil && (err != io.EOF || choice == "") {
		return -1, fmt.Errorf("read selection: %w", err)
	}
	choice = strings.TrimSpace(choice)
	for i, n := range items {
		if choice == n || choice == strconv.Itoa(i+1) {
			return i, nil
		}
	}
	return -1, fmt.Errorf("unknown selection %q", choice)
}

// pickViaHuh renders the arrow-key picker. huh's Select widget
// handles ↑/↓ / j/k / 1..9 / Enter / Ctrl-C natively; we just map
// the result back to an index.
//
// initial seeds the highlighted row — PickWithSelected uses this
// to restore the cursor across re-entries of the same menu.
//
// huh.ErrUserAborted is the sentinel huh returns on Ctrl-C; we
// re-surface it as our package-local ErrPickCancelled so callers
// don't have to import huh just to detect cancel.
func pickViaHuh(prompt string, items []string, initial int) (int, error) {
	opts := make([]huh.Option[int], len(items))
	for i, item := range items {
		opts[i] = huh.NewOption(item, i)
	}
	sel := initial
	err := runHuhField(huh.NewSelect[int]().
		Title(prompt).
		Options(opts...).
		Value(&sel))
	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return -1, ErrPickCancelled
		}
		return -1, fmt.Errorf("picker: %w", err)
	}
	return sel, nil
}

// PickMany renders a multi-select picker (space to toggle each row,
// Enter to confirm). Labels are what the user sees in the picker;
// values are the parallel slice of identifiers returned (and matched
// against the preselected set on entry). The two slices MUST be the
// same length; same-text labels and values can pass the same slice
// twice when no separation is needed.
//
// Same TTY/non-TTY split as Pick: TTY → huh.MultiSelect with Esc
// bound to cancel via runHuhField; non-TTY → degrades to a numeric
// "comma-separated list" reader so scripted invocations stay
// possible.
func PickMany(prompt string, labels, values []string, preselected []string) ([]string, error) {
	if len(labels) != len(values) {
		return nil, fmt.Errorf("PickMany: labels (%d) and values (%d) must have equal length", len(labels), len(values))
	}
	if len(values) == 0 {
		return nil, nil
	}
	if !isInteractive() {
		return pickManyByNumber(prompt, labels, values, preselected)
	}
	opts := make([]huh.Option[string], len(values))
	for i := range values {
		opts[i] = huh.NewOption(labels[i], values[i])
	}
	// huh's MultiSelect mutates the slice we hand it; copy the
	// caller's preselected set so we don't surprise them.
	selected := append([]string(nil), preselected...)
	err := runHuhField(huh.NewMultiSelect[string]().
		Title(prompt).
		Description(i18n.T("cliprompt.pick_toggle_hint")).
		Options(opts...).
		Value(&selected))
	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return nil, ErrPickCancelled
		}
		return nil, fmt.Errorf("picker: %w", err)
	}
	return selected, nil
}

// pickManyByNumber is the non-TTY fallback for PickMany. Prints the
// items as a numbered list with a [x] marker next to currently-
// preselected rows, asks the user for a comma-separated index list
// (or names), and returns the resulting checked set. Empty input
// keeps the preselection as-is — matches the "no change" affordance
// huh's Enter gives on the TTY path.
func pickManyByNumber(prompt string, labels, values, preselected []string) ([]string, error) {
	pre := map[string]bool{}
	for _, v := range preselected {
		pre[v] = true
	}
	cliout.Println(prompt)
	for i, v := range values {
		marker := " "
		if pre[v] {
			marker = "x"
		}
		cliout.Printf("  [%s] %d) %s\n", marker, i+1, labels[i])
	}
	cliout.Printf("%s", i18n.T("cliprompt.pick_toggle_csv"))
	// Read the whole line (not fmt.Scanln, which stops at the first
	// whitespace and errors on the rest), so "1, 2, 3" / "1 2 3" parse
	// the same as "1,2,3". A genuinely empty EOF means "no change"; a
	// populated line is split on commas below and never dropped.
	line, err := readStdinLine()
	if err != nil && (err != io.EOF || line == "") {
		// EOF on empty stdin → no change. Same shape as Pick.
		return preselected, nil
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return preselected, nil
	}
	for _, raw := range strings.Split(line, ",") {
		tok := strings.TrimSpace(raw)
		if tok == "" {
			continue
		}
		// Match by value first, then by 1-based index.
		matched := false
		for i, v := range values {
			if tok == v || tok == strconv.Itoa(i+1) {
				if pre[v] {
					delete(pre, v)
				} else {
					pre[v] = true
				}
				matched = true
				break
			}
		}
		if !matched {
			cliout.Printf("  (unknown selector %q — skipped)\n", tok)
		}
	}
	out := make([]string, 0, len(pre))
	for _, v := range values {
		if pre[v] {
			out = append(out, v)
		}
	}
	return out, nil
}
