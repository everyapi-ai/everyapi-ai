// everyapi-menubar is the GUI menu-bar surface of the EveryAPI tool-chain.
//
// Companion to the `everyapi` CLI (clients/cli/main.go) — same Go module,
// same internal packages, different runtime model: this binary stays
// resident, lives in the macOS status bar (no Dock icon), and wraps the
// CLI's HTTP client / OAuth flow / cert-pinning / sanitizer proxy via the
// same internal/* packages.
//
// V1.1 scope is documented in cmd/menubar/GOAL.md.
package main

import (
	"fyne.io/systray"

	"github.com/everyapi-ai/everyapi-ai/internal/menubar"
)

func main() {
	systray.Run(onReady, onExit)
}

// ctrl is the singleton controller. Captured at package scope so
// onExit can reach it for graceful shutdown (M3+ will use this once
// the sanitizer proxy is wired).
var ctrl *menubar.Controller

func onReady() {
	systray.SetTooltip("EveryAPI")
	// The icon is painted by menubar.New() → applyLoggedOut →
	// applyIconState — no explicit SetTemplateIcon here so a stale
	// hardcoded variant can't sneak into the first paint.
	ctrl = menubar.New()
	go ctrl.Run()
}

func onExit() {
	// systray.Quit (called from the controller's quit handler)
	// fires this. Reserved for future cleanup (sanitizer stop in M3).
}
