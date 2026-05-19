//go:build !darwin && !windows

package menubar

import (
	"errors"
	"os/exec"
)

// confirmDialog on Linux / BSD tries the standard desktop helpers
// in order: zenity (GNOME), then kdialog (KDE). If neither is on
// PATH we fail closed — the anti-phishing modal must NOT be silently
// bypassed on a headless or minimal Linux box. The CLI's terminal
// Enter primitive can't be silently auto-confirmed either; the
// menubar matches that posture.
//
// Both helpers exit 0 on confirm and 1 on cancel — same shape as
// osascript on macOS, so the bool / error mapping is symmetric.
func realConfirmDialog(title, body, confirmLabel, cancelLabel string) (bool, error) {
	if confirmLabel == "" {
		confirmLabel = "OK"
	}
	if cancelLabel == "" {
		cancelLabel = "Cancel"
	}
	if path, err := exec.LookPath("zenity"); err == nil {
		return runConfirm(path,
			"--question", "--no-wrap",
			"--title="+title,
			"--text="+body,
			"--ok-label="+confirmLabel,
			"--cancel-label="+cancelLabel,
		)
	}
	if path, err := exec.LookPath("kdialog"); err == nil {
		return runConfirm(path,
			"--title", title,
			"--yesno", body,
			"--yes-label", confirmLabel,
			"--no-label", cancelLabel,
		)
	}
	return false, errConfirmDialogUnsupported
}

func runConfirm(path string, args ...string) (bool, error) {
	err := exec.Command(path, args...).Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		// Exit code 1 is the canonical "user clicked cancel" for both
		// zenity and kdialog. Treat any non-zero as cancel-or-failed
		// rather than propagating — it's a UI prompt, not an API.
		return false, nil
	}
	return false, err
}
