// Package cliprompt holds the small interactive helpers used by the
// onboarding / setup wizards: line / yes-no / numbered choice prompts,
// plus the cross-platform OpenBrowser shell-out. They live here so the
// per-command subpackages (cmd/seller, cmd/proxy, cmd/login) can share
// one implementation instead of each rolling their own.
//
// Kept small on purpose — pulling in a full TUI library for four
// prompts isn't worth the dep. The functions write prompt labels via
// internal/cliout so test code can capture them by swapping cliout.Out.
package cliprompt

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
)

// Optional asks for a value where empty is a legal answer (the caller
// treats "" as "user skipped"). Distinct from Line, which loops on
// empty input — kept separate so the call site states intent instead
// of overloading the def parameter.
func Optional(in *bufio.Reader, label string) (string, error) {
	cliout.Printf("%s: ", label)
	line, err := in.ReadString('\n')
	if err != nil && (err != io.EOF || line == "") {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// Line asks for a single value. Empty input is rejected unless a
// non-empty default is provided.
func Line(in *bufio.Reader, label, def string) (string, error) {
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

// Choice asks for one of a fixed list of options (1-indexed or by
// name). Loops until the user picks something valid.
func Choice(in *bufio.Reader, label string, options []string) (string, error) {
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
