package proxy

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/internal/config"
	"github.com/everyapi-ai/everyapi-ai/internal/sanitizer"
)

// Proxy is the dispatcher for `everyapi proxy <subcommand>`.
//
// V1 ships `start` and `status`. `stop` is intentionally NOT here —
// `start` runs in the foreground in this MVP, so Ctrl+C is the stop
// signal. Daemonisation + PID-file management land in the follow-up
// session that also wires sanitizer auto-start into `everyapi use`.
func Run(args []string) error {
	if len(args) == 0 {
		return proxyUsageErr()
	}
	switch args[0] {
	case "start":
		return proxyStart(args[1:])
	case "stop":
		return proxyStop(args[1:])
	case "status":
		return proxyStatus(args[1:])
	case "configure":
		return proxyConfigure(args[1:])
	case "help", "--help", "-h":
		cliout.Println(proxyUsage)
		return nil
	default:
		return fmt.Errorf("unknown 'proxy' subcommand %q\n\n%s", args[0], proxyUsage)
	}
}

const proxyUsage = `everyapi proxy — local sanitizer proxy

USAGE
  everyapi proxy start [flags]      Run the sanitizer proxy
  everyapi proxy stop               Stop a running proxy (uses PID file)
  everyapi proxy status             Show running stats
  everyapi proxy configure          Interactive detector + custom-pattern setup
  everyapi proxy help               Show this message

START FLAGS
  --listen <addr>                 Bind address (default 127.0.0.1:8888)
  --upstream <url>                Upstream gateway (default from credentials, or
                                  https://api.everyapi.ai)
  --detach                        Re-exec self in the background and return.
                                  Writes ~/.config/everyapi/sanitizer.pid for
                                  later 'proxy stop'. Logs go to
                                  ~/.config/everyapi/sanitizer.log.

The proxy intercepts requests to the gateway, replaces sensitive substrings
(API keys, PEM private keys, credit cards, Chinese IDs, etc.) with stable
placeholders, forwards them upstream, and restores them on the way back.
The mapping table lives only in this process — it's dropped on exit, never
written to disk, never sent over the wire.

Detector toggles and custom regex patterns live in
~/.config/everyapi/sanitizer.json. Edit them with 'everyapi proxy configure'.

Point your SDK at http://localhost:8888 (or whatever --listen) and the
proxy handles the rest. 'everyapi use <tool>' wires this up automatically.
`

func proxyUsageErr() error {
	return fmt.Errorf("missing subcommand\n\n%s", proxyUsage)
}

// proxyStart parses flags, optionally re-execs itself in detached
// mode, and either blocks in the foreground or returns once the
// detached child is healthy.
func proxyStart(args []string) error {
	fs := flag.NewFlagSet("proxy start", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // we render our own errors
	listen := fs.String("listen", "127.0.0.1:8888", "address to bind")
	upstream := fs.String("upstream", "", "upstream gateway (default from credentials)")
	detach := fs.Bool("detach", false, "run in background; write PID file; return immediately")
	parentPID := fs.Int("parent-pid", 0, "shut down when this pid exits (used by `everyapi use` for auto-cleanup)")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w\n\n%s", err, proxyUsage)
	}
	if *listen == "" {
		return fmt.Errorf("--listen cannot be empty")
	}

	resolvedUpstream := *upstream
	if resolvedUpstream == "" {
		// Pull from saved credentials so a self-hoster's local API
		// base is respected. Fall back to the production default if
		// the user isn't logged in yet — `everyapi proxy start` is
		// useful even pre-login (e.g. for testing detector rules).
		creds, err := config.Load()
		switch {
		case err == nil && creds.APIBase != "":
			resolvedUpstream = creds.APIBase
		default:
			resolvedUpstream = config.DefaultAPIBase
		}
	}
	resolvedUpstream = strings.TrimRight(resolvedUpstream, "/")

	if *detach {
		return reexecDetached(*listen, resolvedUpstream, *parentPID)
	}

	// Refuse to start if another instance is already running. Both
	// fast checks: stale PID file (process dead) is cleared
	// transparently, but a live owner of the port returns an error
	// so the user knows.
	if pid, ok := readPIDFile(); ok {
		if processAlive(pid) {
			return fmt.Errorf("sanitizer proxy already running (pid=%d); use 'everyapi proxy stop' to stop it", pid)
		}
		// Stale; clear it before we claim ownership ourselves.
		_ = removePIDFile()
	}

	// Load on-disk detector overrides if any. Missing file → default
	// built-in set with no customs (LoadFileConfig returns an empty
	// FileConfig + nil err in that case).
	fc, err := sanitizer.LoadFileConfig()
	if err != nil {
		return err
	}
	detectors := fc.BuildDetectors()

	srv, err := sanitizer.New(sanitizer.Config{
		Listen:       *listen,
		UpstreamBase: resolvedUpstream,
		Detectors:    detectors,
		ParentPID:    *parentPID,
	})
	if err != nil {
		return err
	}

	if err := writePIDFile(os.Getpid()); err != nil {
		return fmt.Errorf("write pid file: %w", err)
	}
	defer func() { _ = removePIDFile() }()

	// Cancel on SIGINT / SIGTERM for graceful shutdown.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cliout.Printf("Sanitizer proxy listening on http://%s\n", *listen)
	cliout.Printf("Upstream: %s\n", resolvedUpstream)
	cliout.Printf("Active detectors: %d\n", len(detectors))
	cliout.Println("Point your SDK at http://" + *listen + " (or run 'everyapi use <tool>').")
	cliout.Println("Ctrl+C to stop. The mapping table lives only in memory.")
	cliout.Println("")

	return srv.Run(ctx)
}

// proxyStop reads the PID file and SIGTERMs the recorded process.
// Silently succeeds when there's nothing running (a no-op is the
// expected behaviour after a `Ctrl+C` exit that already cleared the
// file). Surfaces an error only if the PID file points at a process
// we can't signal.
func proxyStop(args []string) error {
	fs := flag.NewFlagSet("proxy stop", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return err
	}
	pid, ok := readPIDFile()
	if !ok {
		cliout.Println("Sanitizer proxy is not running (no PID file).")
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		_ = removePIDFile()
		return fmt.Errorf("find process %d: %w", pid, err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		// Process probably already gone; clean up the stale file
		// either way so the next start doesn't hit "already running".
		_ = removePIDFile()
		if strings.Contains(err.Error(), "process already finished") ||
			strings.Contains(err.Error(), "no such process") {
			cliout.Println("Sanitizer proxy was not running; cleaned up stale PID file.")
			return nil
		}
		return fmt.Errorf("signal pid %d: %w", pid, err)
	}
	cliout.Printf("Sent SIGTERM to sanitizer proxy (pid=%d). Waiting up to 3s for it to exit…\n", pid)
	// Wait briefly for the process to actually exit before
	// reporting success. The server uses an 5s shutdown grace, so
	// 3s here is "polled until done or report still-running".
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			_ = removePIDFile()
			cliout.Println("Stopped.")
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	cliout.Println("Process still alive after 3s — it should exit shortly on its own.")
	return nil
}

// proxyConfigure walks the user through detector toggles + custom
// patterns interactively and writes the result to
// ~/.config/everyapi/sanitizer.json. Re-running it shows the current
// config and asks for the deltas.
func proxyConfigure(args []string) error {
	fs := flag.NewFlagSet("proxy configure", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return err
	}
	fc, err := sanitizer.LoadFileConfig()
	if err != nil {
		return err
	}
	cfgPath, err := sanitizer.ConfigPath()
	if err != nil {
		return err
	}
	in := bufio.NewReader(os.Stdin)

	cliout.Println("everyapi proxy — sanitizer configuration")
	cliout.Printf("Config file: %s\n", cfgPath)
	cliout.Println("")
	cliout.Println("Built-in detectors:")
	disabled := make(map[string]bool, len(fc.Disabled))
	for _, n := range fc.Disabled {
		disabled[n] = true
	}
	for _, name := range sanitizer.AllBuiltinNames() {
		status := "ENABLED "
		if disabled[name] {
			status = "DISABLED"
		}
		cliout.Printf("  [%s] %s\n", status, name)
	}
	cliout.Println("")

	// Phase 1: toggle built-ins.
	toggle, err := cliprompt.YesNo(in, "Toggle any built-in detector?", false)
	if err != nil {
		return err
	}
	if toggle {
		for {
			choice, err := cliprompt.Optional(in, "Detector name to toggle (blank to finish)")
			if err != nil {
				return err
			}
			if choice == "" {
				break
			}
			found := false
			for _, name := range sanitizer.AllBuiltinNames() {
				if name == choice {
					found = true
					if disabled[name] {
						delete(disabled, name)
						cliout.Printf("→ %s now ENABLED\n", name)
					} else {
						disabled[name] = true
						cliout.Printf("→ %s now DISABLED\n", name)
					}
					break
				}
			}
			if !found {
				cliout.Printf("Unknown detector %q. Known: %s\n", choice, strings.Join(sanitizer.AllBuiltinNames(), ", "))
			}
		}
	}

	// Phase 2: custom patterns. Repeated runs of the wizard should
	// not duplicate entries — a previously-named pattern gets its
	// regex overwritten in place (with confirmation), and the
	// existing inventory is shown for context. Users with complex
	// needs can still hand-edit the JSON.
	if len(fc.CustomPatterns) > 0 {
		cliout.Println("")
		cliout.Println("Existing custom patterns:")
		for _, p := range fc.CustomPatterns {
			cliout.Printf("  %s = %s\n", p.Name, p.Regex)
		}
	}
	addCustom, err := cliprompt.YesNo(in, "Add or replace a custom regex pattern?", false)
	if err != nil {
		return err
	}
	for addCustom {
		name, err := cliprompt.Line(in, "Pattern name (no spaces)", "")
		if err != nil {
			return err
		}
		// Find existing index, if any.
		existingIdx := -1
		for i, p := range fc.CustomPatterns {
			if p.Name == name {
				existingIdx = i
				break
			}
		}
		def := ""
		if existingIdx >= 0 {
			def = fc.CustomPatterns[existingIdx].Regex
			cliout.Printf("(pattern %q already exists with regex %q — entering a new value will replace it)\n", name, def)
		}
		expr, err := cliprompt.Line(in, "Regex (Go syntax)", def)
		if err != nil {
			return err
		}
		if _, err := regexp.Compile(expr); err != nil {
			cliout.Printf("Invalid regex %q: %v — skipped.\n", expr, err)
		} else if existingIdx >= 0 {
			fc.CustomPatterns[existingIdx].Regex = expr
			cliout.Printf("→ replaced pattern %q.\n", name)
		} else {
			fc.CustomPatterns = append(fc.CustomPatterns, sanitizer.UserPattern{Name: name, Regex: expr})
			cliout.Printf("→ added pattern %q.\n", name)
		}
		addCustom, err = cliprompt.YesNo(in, "Add or replace another?", false)
		if err != nil {
			return err
		}
	}

	// Rebuild the disabled list from the map and persist.
	fc.Disabled = fc.Disabled[:0]
	for name := range disabled {
		fc.Disabled = append(fc.Disabled, name)
	}

	if err := sanitizer.SaveFileConfig(fc); err != nil {
		return err
	}
	cliout.Printf("Saved → %s\n", cfgPath)
	// Hint: if a proxy is running, it won't pick up the new config
	// until restart. Detect that and tell the user.
	if pid, ok := readPIDFile(); ok && processAlive(pid) {
		cliout.Println("")
		cliout.Println("A sanitizer proxy is currently running. Restart it to pick up the new config:")
		cliout.Println("  everyapi proxy stop && everyapi proxy start --detach")
	}
	return nil
}

// ----------------------------------------------------------------------------
// PID file helpers — small, no exported surface. Stored next to the
// other CLI state files under ~/.config/everyapi so `proxy stop`
// (started later, possibly from a different shell) can find the
// running process.
// ----------------------------------------------------------------------------

func pidFilePath() (string, error) {
	dir, err := config.ConfigDir()
	if err != nil {
		return "", err
	}
	return strings.TrimRight(dir, "/") + "/sanitizer.pid", nil
}

func readPIDFile() (int, bool) {
	path, err := pidFilePath()
	if err != nil {
		return 0, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid); err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

func writePIDFile(pid int) error {
	path, err := pidFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(strings.TrimSuffix(path, "/sanitizer.pid"), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(fmt.Sprintf("%d\n", pid)), 0o600)
}

func removePIDFile() error {
	path, err := pidFilePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// processAlive returns true if `pid` is a running process we can
// signal. os.FindProcess on Unix always succeeds — the real liveness
// check is Signal(0). We treat "operation not permitted" as alive
// (another user's PID; not us, but it IS alive — better to refuse to
// start a second instance than to overwrite their port).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		if strings.Contains(err.Error(), "process already finished") ||
			strings.Contains(err.Error(), "no such process") {
			return false
		}
		// Other errors (permission denied, etc.) → assume alive,
		// don't overwrite.
		return true
	}
	return true
}

// reexecDetached spawns ourselves in foreground mode without the
// --detach flag, redirecting stdout/stderr to ~/.config/everyapi/sanitizer.log
// and detaching from the calling terminal. Returns once the child
// reports healthy via /__sanitizer/health (so callers like `everyapi
// use` know the proxy is actually ready to take requests, not just
// spawned).
func reexecDetached(listen, upstream string, parentPID int) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate self for detach: %w", err)
	}
	dir, err := config.ConfigDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	logPath := strings.TrimRight(dir, "/") + "/sanitizer.log"
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open sanitizer log %s: %w", logPath, err)
	}
	// Note: do not close logFile in this process — the child
	// inherits it via ExtraFiles redirection. The OS handles the
	// reference count.

	childArgs := []string{
		"proxy", "start",
		"--listen", listen,
		"--upstream", upstream,
	}
	if parentPID > 0 {
		childArgs = append(childArgs, "--parent-pid", strconv.Itoa(parentPID))
	}
	cmd := startDetachedCommand(exe, childArgs, logFile)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn detached proxy: %w", err)
	}
	// Don't Wait — we want the child to outlive us. The Go runtime
	// will reap it via SIGCHLD anyway once we exit.

	// Poll /__sanitizer/health until ready or timeout. Avoids
	// returning to the caller before the proxy is actually serving;
	// users would race their next request and see "connection
	// refused".
	deadline := time.Now().Add(5 * time.Second)
	client := &http.Client{Timeout: 250 * time.Millisecond}
	for time.Now().Before(deadline) {
		resp, err := client.Get("http://" + listen + "/__sanitizer/health")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				cliout.Printf("Sanitizer proxy started (pid=%d, listen=%s).\n", cmd.Process.Pid, listen)
				cliout.Printf("Logs: %s\n", logPath)
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("detached proxy did not become healthy within 5s; check %s for errors", logPath)
}

// proxyStatus hits the local proxy's /__sanitizer/status endpoint and
// pretty-prints the result. If the proxy isn't running, we say so —
// no scary stack trace.
func proxyStatus(args []string) error {
	fs := flag.NewFlagSet("proxy status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	listen := fs.String("listen", "127.0.0.1:8888", "address to probe")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w\n\n%s", err, proxyUsage)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + *listen + "/__sanitizer/status")
	if err != nil {
		// Most likely: connection refused → proxy not running.
		fmt.Fprintln(os.Stderr, "Sanitizer proxy is not running.")
		fmt.Fprintln(os.Stderr, "Start it with: everyapi proxy start")
		return nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("unexpected response: %d %s", resp.StatusCode, body)
	}
	// The status body is a single-line JSON. Pretty-print as
	// human-readable key:value lines without pulling in an extra
	// JSON-formatting dependency.
	cliout.Printf("Sanitizer proxy: %s\n", string(body))
	return nil
}
