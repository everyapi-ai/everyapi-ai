package menubar

import (
	"log"

	"github.com/gen2brain/beeep"
)

// notify is a package-level variable so tests can swap in a stub —
// the production path uses beeep.Notify, which would actually fire
// macOS Notification Center / Windows toast / Linux libnotify
// during `go test`. The indirection costs nothing at runtime.
var notify = beeepNotify

// beeepNotify is the production notification dispatcher. beeep picks
// the right primitive per OS (osascript on macOS, toast on Windows,
// libnotify on Linux); a failure is logged but never propagated —
// notifications are nice-to-have and shouldn't ever surface as user-
// visible errors.
//
// Title shows in the notification banner; body is the smaller line
// below. Both render fine with emoji on all three platforms.
func beeepNotify(title, body string) {
	if err := beeep.Notify(title, body, ""); err != nil {
		log.Printf("menubar: desktop notify failed: %v", err)
	}
}
