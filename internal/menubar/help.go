package menubar

import (
	"log"
	"os/exec"
	"runtime"

	"github.com/everyapi-ai/everyapi-ai/internal/config"
	"github.com/everyapi-ai/everyapi-ai/internal/version"
)

// URLs surfaced by the help menu. Hard-coded by design: opening
// these in a browser is a trust-anchor surface, so we don't let
// runtime config redirect them (same posture as cmd/login.go's
// hardcoded DefaultAPIBase comment).
const (
	docsURL   = "https://github.com/everyapi-ai/everyapi-docs"
	issuesURL = "https://github.com/everyapi-ai/everyapi/issues/new"
	homeURL   = "https://everyapi.ai"
)

// handleAbout shows a native modal with version + commit + license.
// On non-darwin platforms the dialog falls back to whatever shim
// is wired (PowerShell MessageBox / zenity --info / log-and-return).
func (c *Controller) handleAbout() {
	body := "EveryAPI menubar app\n\n" +
		"Version: " + version.Version + "\n" +
		"Commit:  " + version.Commit + "\n\n" +
		"License: MIT\n" +
		"Website: " + homeURL + "\n" +
		"Docs:    " + docsURL
	// We deliberately ignore the dialog return — there's only one
	// button (OK) and no destructive action attached.
	_, err := confirmDialog("About EveryAPI", body, "OK", "")
	if err != nil {
		log.Printf("menubar: about dialog: %v", err)
	}
}

// handleOpenDocs jumps the user's default browser to the public
// documentation site.
func (c *Controller) handleOpenDocs() {
	if err := openBrowser(docsURL); err != nil {
		log.Printf("menubar: open docs: %v", err)
	}
}

// handleReportIssue jumps the user's default browser to the
// repository's issue tracker, ready to file a fresh bug report.
func (c *Controller) handleReportIssue() {
	if err := openBrowser(issuesURL); err != nil {
		log.Printf("menubar: open issues: %v", err)
	}
}

// handlePreferences ensures menubar-prefs.json exists with the
// seed template, then opens it in the user's default text editor.
// A notification reminds the user a restart is required for changes
// to take effect — live-reload isn't implemented (knobs are once-
// a-month settings, not high-frequency tweaks).
func (c *Controller) handlePreferences() {
	path, err := ensurePrefsFile()
	if err != nil {
		log.Printf("menubar: ensure prefs file: %v", err)
		notify("EveryAPI — couldn't open preferences", err.Error())
		return
	}
	if err := openInEditorFn(path); err != nil {
		log.Printf("menubar: open editor: %v", err)
		notify("EveryAPI — couldn't open preferences", err.Error())
		return
	}
	notify(
		"EveryAPI — preferences open in editor",
		"Save the file, then quit and relaunch EveryAPI for changes to apply.",
	)
}

// handleRevealConfig opens the menubar's config directory in the
// platform's file browser. Useful for editing credentials.json /
// menubar-state.json / menubar-prefs.json (M15) by hand and for
// reporting bugs ("zip these and attach"). The OS file browser is
// preferable to dumping a path into a notification — users can
// drag-drop, edit, share.
func (c *Controller) handleRevealConfig() {
	dir, err := config.ConfigDir()
	if err != nil {
		log.Printf("menubar: resolve config dir: %v", err)
		return
	}
	if err := revealInFileBrowser(dir); err != nil {
		log.Printf("menubar: reveal config: %v", err)
	}
}

// revealInFileBrowser is split out as a package var so tests can
// stub the shell-out. Platform dispatch matches openBrowser's
// pattern.
var revealInFileBrowser = realRevealInFileBrowser

func realRevealInFileBrowser(path string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", path).Start()
	case "windows":
		return exec.Command("explorer", path).Start()
	default:
		return exec.Command("xdg-open", path).Start()
	}
}
