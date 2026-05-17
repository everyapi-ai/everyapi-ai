// Package cmd hosts the subcommand handlers. Each top-level command
// is one file; main.go dispatches by argv[1].
//
// Commands take (args []string) and return an error. Errors bubble to
// main.go which prints "Error: <msg>" and exits 1.
package cmd

import (
	"context"
	"fmt"
	"os"
)

// Out is the writer all commands print to. Tests swap it for a
// bytes.Buffer; production points at os.Stdout. Kept as a package
// var (not a parameter on every function) to keep handlers readable —
// commands are 99% of the time writing to stdout.
var Out = os.Stdout

// printf is a thin wrapper around fmt.Fprintf(Out, ...) so handlers
// don't repeat the Out arg.
func printf(format string, args ...any) {
	fmt.Fprintf(Out, format, args...)
}

func println(s string) {
	fmt.Fprintln(Out, s)
}

// withCtx returns the standard context for an API call — currently
// just context.Background, but factored so future cancellation
// (Ctrl+C handler) plugs in here without touching every command.
func withCtx() context.Context {
	return context.Background()
}
