package cliprompt

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
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
func Pick(prompt string, items []string) (int, error) {
	if len(items) == 0 {
		return -1, errors.New("nothing to pick from")
	}
	if !isInteractive() {
		return pickByNumber(prompt, items)
	}
	return pickViaHuh(prompt, items)
}

func pickByNumber(prompt string, items []string) (int, error) {
	cliout.Println(prompt)
	for i, n := range items {
		cliout.Printf("  %d) %s\n", i+1, n)
	}
	cliout.Printf("Enter name or number: ")
	var choice string
	if _, err := fmt.Scanln(&choice); err != nil {
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
// huh.ErrUserAborted is the sentinel huh returns on Ctrl-C; we
// re-surface it as our package-local ErrPickCancelled so callers
// don't have to import huh just to detect cancel.
func pickViaHuh(prompt string, items []string) (int, error) {
	opts := make([]huh.Option[int], len(items))
	for i, item := range items {
		opts[i] = huh.NewOption(item, i)
	}
	var sel int
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
		Description("space to toggle · enter to confirm").
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
	cliout.Printf("Enter comma-separated names or numbers to TOGGLE (blank = no change): ")
	var line string
	if _, err := fmt.Scanln(&line); err != nil {
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
