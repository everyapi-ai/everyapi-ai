//go:build !darwin && !windows

package menubar

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
)

// realTextPrompt on Linux / BSD tries zenity first (GNOME and most
// desktops have it), kdialog second (KDE), and returns
// errTextInputUnsupported when neither is on PATH. Same return
// shape as macOS osascript / Windows InputBox.
//
// Both helpers exit 0 on confirm and 1 on cancel, with the entered
// text on stdout for zenity (`--entry`) and kdialog (`--inputbox`).
func realTextPrompt(title, body, defaultValue string) (string, bool, error) {
	if path, err := exec.LookPath("zenity"); err == nil {
		return runEntry(path,
			"--entry",
			"--title="+title,
			"--text="+body,
			"--entry-text="+defaultValue,
		)
	}
	if path, err := exec.LookPath("kdialog"); err == nil {
		return runEntry(path,
			"--title", title,
			"--inputbox", body, defaultValue,
		)
	}
	return "", false, errTextInputUnsupported
}

func runEntry(path string, args ...string) (string, bool, error) {
	var stdout bytes.Buffer
	cmd := exec.Command(path, args...)
	cmd.Stdout = &stdout
	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", false, nil // user cancelled
		}
		return "", false, err
	}
	return strings.TrimRight(stdout.String(), "\r\n"), true, nil
}

// errTextInputUnsupported surfaces when neither zenity nor kdialog
// is available. The OAuth path catches it and tells the user to
// install one — better than silently auto-failing the flow.
var errTextInputUnsupported = errors.New("no zenity or kdialog on PATH — install one for the in-menubar OAuth flow")
