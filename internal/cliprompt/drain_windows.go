//go:build windows

package cliprompt

// DrainStdin is a no-op on Windows. The OSC 11 background-color leak it guards against on Unix (see drain_unix.go) doesn't apply the same way here, and the non-blocking-fd dance it uses isn't portable to Windows console handles. `everyapi use` on Windows runs the tool via exec.Command (not execve) anyway, so there's no shared-fd handoff to protect.
func DrainStdin() {}
