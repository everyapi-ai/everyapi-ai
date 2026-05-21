// Package version exposes build-time metadata stamped by the release
// pipeline.
//
// Version + Commit are overwritten at build time via -ldflags injecting
// into `github.com/everyapi-ai/everyapi-ai/internal/version.{Version,Commit}`.
// Local `go run` / `go build` leaves the placeholders, but Resolve()
// fills them in from runtime/debug.ReadBuildInfo when present — so a
// developer rebuilding from a git checkout sees their actual commit
// SHA instead of the literal "unknown".
package version

import (
	"runtime/debug"
	"strings"
)

var (
	Version = "dev"
	Commit  = "unknown"
)

// Resolve returns the effective version + commit, falling back to
// Go's own build info when the ldflag vars are at their defaults.
// commit gets a "-dirty" suffix if the build was made from a
// working tree with uncommitted changes — matches `git describe
// --dirty`'s convention so the user can tell a clean rebuild from
// a "I edited a file and `go build`'d" build apart.
//
// Used by the version command and the launcher's startup info, so
// neither path has to duplicate the BuildInfo plumbing.
func Resolve() (ver, commit string) {
	ver, commit = Version, Commit
	if ver != "dev" && commit != "unknown" {
		return ver, commit
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ver, commit
	}
	var rev, modified string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value
		}
	}
	if commit == "unknown" && rev != "" {
		// Short SHA — long forms aren't useful in a status line.
		short := rev
		if len(short) > 12 {
			short = short[:12]
		}
		if modified == "true" {
			short += "-dirty"
		}
		commit = short
	}
	if ver == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		// `go install module@vX.Y.Z` puts the requested version
		// here even without our -ldflags stamping. Helps `go install`
		// users see a real version on the version command.
		ver = strings.TrimPrefix(info.Main.Version, "v")
	}
	return ver, commit
}
