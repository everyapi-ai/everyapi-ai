// Package cliout holds the small set of helpers shared by every CLI
// subcommand: the stdout writer (swappable from tests), formatted
// print wrappers, and the context constructors used by interactive
// vs. fire-and-forget commands. These used to live unexported in the
// `cmd` package, but lifting them to a separate package lets the
// per-command subpackages (cmd/seller, cmd/proxy) reach them without
// the awkward cmd → cmd/* import direction.
package cliout

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// Out is the writer every command prints to. Tests swap it for a
// bytes.Buffer / io.Discard; production points at os.Stdout. Kept as
// a package var (not a parameter on every function) to keep handlers
// readable — commands are 99% of the time writing to stdout.
//
// Typed as io.Writer (not *os.File) so tests can plug in anything
// satisfying the interface without an os.Pipe round-trip.
var Out io.Writer = os.Stdout

// Printf is a thin wrapper around fmt.Fprintf(Out, ...) so handlers
// don't repeat the Out arg.
func Printf(format string, args ...any) {
	fmt.Fprintf(Out, format, args...)
}

// Println writes a single line to Out (with a trailing newline).
func Println(s string) {
	fmt.Fprintln(Out, s)
}

// WithCtx returns the standard context for a short, fire-and-forget
// API call. Long-running INTERACTIVE commands (login poll, OAuth wait)
// must use SignalCtx instead so Ctrl+C unwinds cleanly.
func WithCtx() context.Context {
	return context.Background()
}

// SignalCtx returns a context canceled on the first SIGINT/SIGTERM.
// Use it (with `defer stop()`) for long-running interactive commands
// so Ctrl+C cancels the in-flight network call and unwinds cleanly
// instead of hard-killing the process mid-exchange.
func SignalCtx() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
