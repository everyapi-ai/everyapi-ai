// Package version exposes build-time metadata stamped by the release
// pipeline.
//
// Version + Commit are overwritten at build time via -ldflags injecting
// into `github.com/everyapi-ai/everyapi-ai/internal/version.{Version,Commit}`.
// Local `go run` / `go build` leaves the placeholders so callers can
// tell a dev build apart from a tagged release.
package version

var (
	Version = "dev"
	Commit  = "unknown"
)
