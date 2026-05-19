// Package cmd hosts the subcommand handlers. Each top-level command
// is one file; main.go dispatches by argv[1].
//
// Commands take (args []string) and return an error. Errors bubble to
// main.go which prints "Error: <msg>" and exits 1.
package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// Out is the writer all commands print to. Tests swap it for a
// bytes.Buffer / io.Discard; production points at os.Stdout. Kept as
// a package var (not a parameter on every function) to keep handlers
// readable — commands are 99% of the time writing to stdout.
//
// Typed as io.Writer (not *os.File) so tests can plug in anything
// satisfying the interface without an os.Pipe round-trip.
var Out io.Writer = os.Stdout

// printf is a thin wrapper around fmt.Fprintf(Out, ...) so handlers
// don't repeat the Out arg.
func printf(format string, args ...any) {
	fmt.Fprintf(Out, format, args...)
}

func println(s string) {
	fmt.Fprintln(Out, s)
}

// withCtx returns the standard context for a short, fire-and-forget
// API call. Long-running INTERACTIVE commands (login poll, OAuth wait)
// must use signalCtx instead so Ctrl+C unwinds cleanly.
func withCtx() context.Context {
	return context.Background()
}

// signalCtx returns a context canceled on the first SIGINT/SIGTERM.
// Use it (with `defer stop()`) for long-running interactive commands
// so Ctrl+C cancels the in-flight network call and unwinds cleanly
// instead of hard-killing the process mid-exchange. Mirrors the
// pattern proxyStart already uses for the proxy server.
func signalCtx() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
