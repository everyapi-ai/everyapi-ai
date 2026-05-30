package cliprompt

import "testing"

// TestDrainStdin_NonTTY confirms the drain is a clean no-op when stdin
// isn't a terminal — which is exactly the `go test` environment (stdin
// is a pipe / /dev/null). The real risk this guards against is a hang:
// if DrainStdin ever blocked on a non-TTY fd, this test would time out.
func TestDrainStdin_NonTTY(t *testing.T) {
	DrainStdin() // must return promptly without panicking
}
