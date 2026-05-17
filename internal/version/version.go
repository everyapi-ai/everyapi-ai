// Version is overwritten at build time via -ldflags. Local `go run` /
// `go build` leaves the placeholder so callers can tell a dev build
// apart from a tagged release.
package version

var (
	Version = "dev"
	Commit  = "unknown"
)
