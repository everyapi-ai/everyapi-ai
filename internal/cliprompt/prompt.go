// Package cliprompt holds the interactive helpers used by the
// onboarding / setup wizards: line / yes-no / list-choice prompts,
// plus the cross-platform OpenBrowser shell-out. They live here so the
// per-command subpackages (cmd/seller, cmd/proxy, cmd/login) can share
// one implementation instead of each rolling their own.
//
// Each prompt has two paths:
//
//   - TTY (both stdin and stdout are terminals): delegated to
//     charmbracelet/huh. Arrow keys, in-place rendering, validation
//     feedback, Ctrl-C cancels cleanly.
//   - Non-TTY (pipe / CI / redirected stdin): the original line-
//     based reader that prompts via cliout.Print + bufio.ReadString.
//     This keeps `printf "y\n" | everyapi seller setup` working and
//     keeps the test harness's stdin-pipe mocking unchanged.
//
// The two paths are kept behind one signature so each call site
// states intent (Line vs. Optional vs. YesNo vs. Choice) without
// having to know which path will run.
package cliprompt

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/huh"
	"golang.org/x/term"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
)

// runHuhField wraps every cliprompt huh widget in a form whose Quit
// binding accepts BOTH ctrl+c and esc. Huh's default keymap binds
// Quit to ctrl+c only — pressing Esc inside a prompt is a no-op
// out of the box, which contradicts the launcher's "Esc = go back"
// affordance everywhere else. Adding esc here makes every prompt
// (Select/Confirm/Input) cancel-on-Esc consistently.
//
// WithShowHelp(false) keeps the rendering compact — huh's default
// help line would steal a row under every prompt for a hint the
// launcher's own status line already conveys.
func runHuhField(field huh.Field) error {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"))
	return huh.NewForm(huh.NewGroup(field)).
		WithKeyMap(km).
		WithShowHelp(false).
		Run()
}

// isInteractive reports whether both stdin and stdout are TTYs. A
// package var so tests can flip the routing to exercise both
// branches without an actual terminal. Exported via IsInteractive
// for callers (main.go's launcher gate) that need the same check
// without re-implementing the TTY detection.
var isInteractive = func() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

// IsInteractive is the exported view of the package's TTY check.
// Used by main.go to decide whether bare `everyapi` should launch
// the TUI command picker or print the text usage.
func IsInteractive() bool { return isInteractive() }

// huhCancelled reports whether the error is huh's user-abort sentinel
// (Esc or Ctrl-C). Caller maps it to ErrPickCancelled, the package's
// unified "user wants out of this prompt" signal. Distinct from
// io.EOF, which means "non-TTY stdin ended" (scripted invocation
// with empty input) — same error type would conflate two cases the
// caller often needs to distinguish (e.g. topup proceeds on a
// piped empty stdin, but aborts on a TTY Esc).
func huhCancelled(err error) bool {
	return errors.Is(err, huh.ErrUserAborted)
}

// Optional asks for a value where empty is a legal answer (the caller
// treats "" as "user skipped"). The *bufio.Reader is consumed only on
// the non-TTY fallback path; TTY callers can pass any reader (typically
// bufio.NewReader(os.Stdin)) and huh ignores it.
func Optional(in *bufio.Reader, label string) (string, error) {
	if isInteractive() {
		var v string
		err := runHuhField(huh.NewInput().Title(label).Value(&v))
		if huhCancelled(err) {
			return "", ErrPickCancelled
		}
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(v), nil
	}
	cliout.Printf("%s: ", label)
	line, err := in.ReadString('\n')
	if err != nil && (err != io.EOF || line == "") {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// Line asks for a single value. Empty input is rejected unless a
// non-empty default is provided (then default applies).
func Line(in *bufio.Reader, label, def string) (string, error) {
	if isInteractive() {
		v := def
		title := label
		if def != "" {
			// huh's Input pre-loads Value as the current text; the
			// user can edit or accept it with Enter. Matches the
			// "[default]" hint the line-reader path shows.
			title = fmt.Sprintf("%s (Enter to keep default)", label)
		}
		input := huh.NewInput().Title(title).Value(&v)
		if def == "" {
			input = input.Validate(func(s string) error {
				if strings.TrimSpace(s) == "" {
					return errors.New("required")
				}
				return nil
			})
		}
		if err := runHuhField(input); err != nil {
			if huhCancelled(err) {
				return "", ErrPickCancelled
			}
			return "", err
		}
		v = strings.TrimSpace(v)
		if v == "" {
			return def, nil
		}
		return v, nil
	}
	suffix := ""
	if def != "" {
		suffix = fmt.Sprintf(" [%s]", def)
	}
	for {
		cliout.Printf("%s%s: ", label, suffix)
		line, err := in.ReadString('\n')
		if err != nil && (err != io.EOF || line == "") {
			return "", err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			if def != "" {
				return def, nil
			}
			cliout.Println("(value required)")
			continue
		}
		return line, nil
	}
}

// Choice asks for one of a fixed list of options. On a TTY this is
// an arrow-key picker (charmbracelet/huh); off TTY it loops on
// numbered input.
func Choice(in *bufio.Reader, label string, options []string) (string, error) {
	if isInteractive() {
		idx, err := Pick(label, options)
		if err != nil {
			return "", err
		}
		return options[idx], nil
	}
	cliout.Printf("%s — options:\n", label)
	for i, o := range options {
		cliout.Printf("  %d) %s\n", i+1, o)
	}
	for {
		cliout.Printf("Enter name or number: ")
		line, err := in.ReadString('\n')
		if err != nil && (err != io.EOF || line == "") {
			return "", err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		for i, o := range options {
			if strings.EqualFold(line, o) || line == strconv.Itoa(i+1) {
				return o, nil
			}
		}
		cliout.Printf("(unknown choice %q — try again)\n", line)
	}
}

// YesNo gates destructive operations. Default applies on empty input.
func YesNo(in *bufio.Reader, label string, defaultYes bool) (bool, error) {
	if isInteractive() {
		v := defaultYes
		err := runHuhField(huh.NewConfirm().
			Title(label).
			Affirmative("Yes").
			Negative("No").
			Value(&v))
		if huhCancelled(err) {
			return false, ErrPickCancelled
		}
		if err != nil {
			return false, err
		}
		return v, nil
	}
	suffix := "[y/N]"
	if defaultYes {
		suffix = "[Y/n]"
	}
	for {
		cliout.Printf("%s %s: ", label, suffix)
		line, err := in.ReadString('\n')
		if err != nil && (err != io.EOF || line == "") {
			return false, err
		}
		line = strings.TrimSpace(strings.ToLower(line))
		switch line {
		case "":
			return defaultYes, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		cliout.Println("(please answer y or n)")
	}
}

// OpenBrowser tries the platform's standard "open URL" helper. We
// intentionally use exec.Command + ignore stderr: a browser launcher
// that fails should not look like a CLI bug — every caller already
// prints the URL for the user to copy.
func OpenBrowser(url string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	default:
		cmd = "xdg-open"
		args = []string{url}
	}
	return exec.Command(cmd, args...).Start()
}
