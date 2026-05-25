package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
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
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-sdk/config"
	"github.com/everyapi-ai/everyapi-sdk/sanitizer"
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

	// Track whether the user explicitly chose --listen (vs. fell
	// through to the default). On a port-conflict we auto-fall to
	// a free ephemeral port WHEN the default was used — picking
	// a kernel-assigned port behind a user's back when they typed
	// `--listen 127.0.0.1:8899` would be the opposite of what
	// they asked for.
	listenExplicit := false
	detachExplicit := false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "listen":
			listenExplicit = true
		case "detach":
			detachExplicit = true
		}
	})

	// If the user didn't pass --detach explicitly AND we're on a
	// TTY (so the launcher / a real shell, not a CI pipe), ask
	// whether to detach. Default is Yes — picking "start" from a
	// menu almost always means "set it up and forget it", and
	// foreground is the debugging affordance. Off-TTY callers
	// keep the historical default (foreground unless --detach).
	if !detachExplicit && cliprompt.IsInteractive() {
		bg, perr := cliprompt.YesNo(
			bufio.NewReader(os.Stdin),
			i18n.T("proxy.detach_prompt"),
			true,
		)
		if perr != nil {
			if !errors.Is(perr, cliprompt.ErrPickCancelled) && !errors.Is(perr, io.EOF) {
				return perr
			}
			// Treat cancel as "don't change the choice" — fall
			// through with the parsed *detach value (false by
			// default), which is the safer "stay in foreground"
			// option from a "didn't decide" standpoint.
			if errors.Is(perr, cliprompt.ErrPickCancelled) {
				return perr
			}
		} else {
			*detach = bg
		}
	}
	if portOccupied(*listen) {
		if listenExplicit {
			return fmt.Errorf("listen %s is held by another process; pick a different --listen or stop the foreign service ('lsof -iTCP:%s -sTCP:LISTEN' will name it)",
				*listen, portOfAddr(*listen))
		}
		// Default 8888 collision (typical: a SearXNG / dev tool
		// holding 8888). Quietly pick a free port instead of
		// crashing — the chosen address ends up in the PID file
		// so 'proxy status' / `use` find it automatically.
		port, err := pickFreePortLocal()
		if err != nil {
			return fmt.Errorf("default port %s is taken and no free fallback port found: %w", *listen, err)
		}
		*listen = fmt.Sprintf("127.0.0.1:%d", port)
	}

	// Override flag wins, else saved creds (so a self-hoster's local API
	// base is respected), else the production default — usable pre-login
	// for testing detector rules. Trailing slash trimmed.
	resolvedUpstream := config.ResolveAPIBase(*upstream)

	if *detach {
		return reexecDetached(*listen, resolvedUpstream, *parentPID)
	}

	// Refuse to start if another instance is already running. Both
	// fast checks: stale PID file (process dead) is cleared
	// transparently, but a live owner of the port returns an error
	// so the user knows.
	if pid, _, ok := readPIDFile(); ok {
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

	if err := writePIDFile(os.Getpid(), *listen); err != nil {
		return fmt.Errorf("write pid file: %w", err)
	}
	defer func() { _ = removePIDFile() }()

	// Cancel on SIGINT / SIGTERM for graceful shutdown.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cliout.Printf(i18n.T("proxy.start_listening")+"\n", *listen)
	cliout.Printf(i18n.T("proxy.start_upstream")+"\n", resolvedUpstream)
	cliout.Printf(i18n.T("proxy.start_detectors")+"\n", len(detectors))
	cliout.Printf(i18n.T("proxy.start_point_sdk")+"\n", *listen)
	cliout.Println(i18n.T("proxy.start_ctrl_c"))
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
	pid, _, ok := readPIDFile()
	if !ok {
		cliout.Println(i18n.T("proxy.stop_not_running"))
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
			cliout.Println(i18n.T("proxy.stop_was_not_running"))
			return nil
		}
		return fmt.Errorf("signal pid %d: %w", pid, err)
	}
	cliout.Printf(i18n.T("proxy.stop_sent_sigterm"), pid)
	// Wait briefly for the process to actually exit before
	// reporting success. The server uses an 5s shutdown grace, so
	// 3s here is "polled until done or report still-running".
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			_ = removePIDFile()
			cliout.Println(i18n.T("proxy.stop_stopped"))
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	cliout.Println(i18n.T("proxy.stop_still_alive"))
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

	cliout.Println(i18n.T("proxy.configure_header"))
	cliout.Printf(i18n.T("proxy.configure_config_file")+"\n", cfgPath)
	cliout.Println("")

	// Phase 1: built-in detectors. Replaces the previous "Toggle any?
	// → enter name → repeat" loop with a single multi-select that
	// shows every detector with its current state pre-checked. The
	// user space-bars to flip whichever rows they want and hits
	// Enter once.
	disabled := make(map[string]bool, len(fc.Disabled))
	for _, n := range fc.Disabled {
		disabled[n] = true
	}
	allDetectors := sanitizer.AllBuiltinNames()
	// Build "name  — description" labels so the user doesn't have
	// to memorise what each detector catches. Name width is fixed
	// to the longest name so the descriptions line up in a column.
	nameWidth := 0
	for _, name := range allDetectors {
		if len(name) > nameWidth {
			nameWidth = len(name)
		}
	}
	labels := make([]string, len(allDetectors))
	for i, name := range allDetectors {
		labels[i] = fmt.Sprintf("%-*s — %s", nameWidth, name, sanitizer.DescribeBuiltin(name))
	}
	preselected := make([]string, 0, len(allDetectors))
	for _, name := range allDetectors {
		if !disabled[name] {
			preselected = append(preselected, name)
		}
	}
	selectedEnabled, err := cliprompt.PickMany(
		i18n.T("proxy.configure_builtin_prompt"),
		labels, allDetectors, preselected)
	if err != nil {
		return err
	}
	// Rebuild the disabled set from the returned enabled set.
	disabled = make(map[string]bool, len(allDetectors))
	for _, name := range allDetectors {
		disabled[name] = true
	}
	for _, name := range selectedEnabled {
		delete(disabled, name)
	}

	// Phase 2: custom patterns. Repeated runs of the wizard should
	// not duplicate entries — a previously-named pattern gets its
	// regex overwritten in place (with confirmation), and the
	// existing inventory is shown for context. Users with complex
	// needs can still hand-edit the JSON.
	if len(fc.CustomPatterns) > 0 {
		cliout.Println("")
		cliout.Println(i18n.T("proxy.configure_existing_patterns"))
		for _, p := range fc.CustomPatterns {
			cliout.Printf("  %s = %s\n", p.Name, p.Regex)
		}
	}
	addCustom, err := cliprompt.YesNo(in, i18n.T("proxy.configure_add_replace_prompt"), false)
	if err != nil {
		return err
	}
	for addCustom {
		name, err := cliprompt.Line(in, i18n.T("proxy.configure_pattern_name_prompt"), "")
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
			cliout.Printf(i18n.T("proxy.configure_pattern_exists")+"\n", name, def)
		}
		expr, err := cliprompt.Line(in, i18n.T("proxy.configure_regex_prompt"), def)
		if err != nil {
			return err
		}
		if _, err := regexp.Compile(expr); err != nil {
			cliout.Printf(i18n.T("proxy.configure_invalid_regex")+"\n", expr, err)
		} else if existingIdx >= 0 {
			fc.CustomPatterns[existingIdx].Regex = expr
			cliout.Printf(i18n.T("proxy.configure_replaced")+"\n", name)
		} else {
			fc.CustomPatterns = append(fc.CustomPatterns, sanitizer.UserPattern{Name: name, Regex: expr})
			cliout.Printf(i18n.T("proxy.configure_added")+"\n", name)
		}
		addCustom, err = cliprompt.YesNo(in, i18n.T("proxy.configure_add_replace_another"), false)
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
	cliout.Printf(i18n.T("proxy.configure_saved")+"\n", cfgPath)
	// Hint: if a proxy is running, it won't pick up the new config
	// until restart. Detect that and tell the user.
	if pid, _, ok := readPIDFile(); ok && processAlive(pid) {
		cliout.Println("")
		cliout.Println(i18n.T("proxy.configure_restart_hint_1"))
		cliout.Println(i18n.T("proxy.configure_restart_hint_2"))
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

// readPIDFile parses the state file written by writePIDFile. The
// on-disk format is "<pid> <listen-addr>\n" — old single-PID files
// ("<pid>\n") are still accepted, with the returned listen empty so
// callers can fall back to the default port. Splitting on whitespace
// (rather than JSON-encoding) keeps the read path Sscanf-trivial and
// the file `cat`-friendly for ops debugging.
// IsRunning reports whether a sanitizer proxy we can actually signal
// is up — the pid file exists AND the process it points at is alive.
// A bare pid-file check would lie when the previous proxy crashed
// without cleanup; callers (`everyapi uninstall`'s plan-render block)
// rely on this returning false for stale-pid leftovers so the
// confirmation text isn't misleading.
func IsRunning() bool {
	pid, _, ok := readPIDFile()
	if !ok {
		return false
	}
	return processAlive(pid)
}

func readPIDFile() (pid int, listen string, ok bool) {
	path, err := pidFilePath()
	if err != nil {
		return 0, "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, "", false
	}
	trimmed := strings.TrimSpace(string(data))
	// "<pid> <listen>" — Sscanf with %d %s parses both. If listen is
	// missing (legacy single-int file), %s fails but the prior %d
	// succeeded, so n == 1 and we return the pid alone.
	var p int
	var l string
	n, _ := fmt.Sscanf(trimmed, "%d %s", &p, &l)
	if n < 1 || p <= 0 {
		return 0, "", false
	}
	return p, l, true
}

func writePIDFile(pid int, listen string) error {
	path, err := pidFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(strings.TrimSuffix(path, "/sanitizer.pid"), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(fmt.Sprintf("%d %s\n", pid, listen)), 0o600)
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
	//
	// Budget tightened from 5 s to 2 s after observing the worst-
	// case "port held by some other service" path eat the full
	// timeout for nothing — the parent's port-occupied pre-check
	// in cmd/use.go catches that case before we get here, but if
	// someone bypasses that path we still don't want to make every
	// failed start a 5-second pause. A healthy fresh sanitizer
	// usually answers in under 200 ms.
	deadline := time.Now().Add(2 * time.Second)
	client := &http.Client{Timeout: 250 * time.Millisecond}
	for time.Now().Before(deadline) {
		resp, err := client.Get("http://" + listen + "/__sanitizer/health")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				cliout.Printf(i18n.T("proxy.started_pid")+"\n", cmd.Process.Pid, listen)
				cliout.Printf(i18n.T("proxy.started_logs")+"\n", logPath)
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("detached proxy did not become healthy within 2s; check %s for errors", logPath)
}

// proxyStatus hits the local proxy's /__sanitizer/status endpoint and
// pretty-prints the result. Three terminal states:
//
//   - Connect refused / I/O error → no proxy listening; tell the
//     user to start it.
//   - HTTP non-200 with a non-JSON body → some OTHER server is on
//     that port (browsers / random local services / SearXNG / etc.);
//     tell the user that, suggest --listen <addr> with a different
//     port. Don't dump the foreign body — that body has been a
//     full HTML page in the wild and the resulting error message
//     was thousands of characters long.
//   - HTTP 200 with our sanitizer's JSON → render it.
func proxyStatus(args []string) error {
	fs := flag.NewFlagSet("proxy status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	// Sentinel default — empty means "look it up from the PID file's
	// stored listen address; only fall back to 127.0.0.1:8888 if
	// nothing is on disk." A user-supplied --listen wins over both.
	listen := fs.String("listen", "", "address to probe (default: from PID file, else 127.0.0.1:8888)")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w\n\n%s", err, proxyUsage)
	}
	addr := *listen
	if addr == "" {
		if _, recorded, ok := readPIDFile(); ok && recorded != "" {
			addr = recorded
		} else {
			addr = "127.0.0.1:8888"
		}
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + addr + "/__sanitizer/status")
	if err != nil {
		// Most likely: connection refused → proxy not running.
		fmt.Fprintln(os.Stderr, i18n.T("proxy.status_not_running"))
		fmt.Fprintln(os.Stderr, i18n.T("proxy.status_start_hint"))
		return nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !looksLikeSanitizerJSON(body, resp.Header.Get("Content-Type")) {
		ct := resp.Header.Get("Content-Type")
		fmt.Fprintf(os.Stderr, i18n.T("proxy.status_wrong_server_line1"),
			addr, resp.StatusCode, ct)
		fmt.Fprintln(os.Stderr, i18n.T("proxy.status_wrong_server_line2"))
		fmt.Fprintf(os.Stderr, i18n.T("proxy.status_wrong_server_line3")+"\n", portOf(addr))
		return nil
	}
	return renderSanitizerStatus(body)
}

// renderSanitizerStatus turns the sanitizer's /__sanitizer/status
// JSON into the same key-aligned table style 'edge status' uses.
// Falls back to printing the raw body if parsing fails so the
// user never gets less information than they used to.
//
// On-wire shape (cmd-stable):
//
//	{
//	  "listen": "127.0.0.1:8888",
//	  "upstream": "https://api.everyapi.ai",
//	  "uptime_seconds": 1234,
//	  "requests": 0,
//	  "sanitised_requests": 0,
//	  "bytes_in": 0,
//	  "bytes_out": 0,
//	  "mapping_size": 0
//	}
func renderSanitizerStatus(body []byte) error {
	var st struct {
		Listen            string `json:"listen"`
		Upstream          string `json:"upstream"`
		UptimeSeconds     int64  `json:"uptime_seconds"`
		Requests          int64  `json:"requests"`
		SanitisedRequests int64  `json:"sanitised_requests"`
		BytesIn           int64  `json:"bytes_in"`
		BytesOut          int64  `json:"bytes_out"`
		MappingSize       int    `json:"mapping_size"`
	}
	if err := json.Unmarshal(body, &st); err != nil {
		// Don't return — surface raw body so a sanitizer that grew a
		// new field doesn't black-hole on a stale CLI.
		cliout.Printf(i18n.T("proxy.status_raw_prefix")+"\n", string(body))
		return nil
	}
	cliout.Println(i18n.T("proxy.status_header"))
	cliout.Printf("  %-12s%s\n", "listen:", st.Listen)
	cliout.Printf("  %-12s%s\n", "upstream:", st.Upstream)
	cliout.Printf("  %-12s%s\n", "uptime:", humanDurationSec(st.UptimeSeconds))
	cliout.Printf("  %-12s%d\n", "requests:", st.Requests)
	cliout.Printf("  %-12s%d\n", "sanitised:", st.SanitisedRequests)
	cliout.Printf("  %-12s%s in / %s out\n", "transfer:", humanBytes(st.BytesIn), humanBytes(st.BytesOut))
	cliout.Printf("  %-12s%d entries\n", "mappings:", st.MappingSize)
	return nil
}

// humanDurationSec formats a duration-in-seconds into "Ns / Nm /
// Nh / Nd" the same way the edge tools render their last-seen
// gap, so a user reading both pages doesn't have to mentally
// switch unit conventions.
func humanDurationSec(s int64) string {
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm", s/60)
	case s < 86400:
		return fmt.Sprintf("%dh", s/3600)
	default:
		return fmt.Sprintf("%dd", s/86400)
	}
}

// humanBytes formats raw byte counts as 'NB / NKB / NMB / NGB'.
// IEC prefixes (Ki/Mi) are technically correct but overkill for a
// CLI status table; SI rounding is the convention every other
// network/storage UI uses, including 'curl -w' and 'du -h'.
func humanBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	default:
		return fmt.Sprintf("%.1f GB", float64(n)/(1024*1024*1024))
	}
}

// looksLikeSanitizerJSON sniffs whether the response actually came
// from our sanitizer (which serves application/json) vs. some other
// service that happens to be bound to the probed port. Either the
// Content-Type announces JSON OR the body opens with '{' / '['. Cheap
// and correct enough — the sanitizer always returns a JSON object,
// and an HTML 404 page never opens with a brace.
func looksLikeSanitizerJSON(body []byte, contentType string) bool {
	if strings.Contains(strings.ToLower(contentType), "json") {
		return true
	}
	trimmed := bytes.TrimSpace(body)
	return len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[')
}

// portOccupied — local-to-package duplicate of cmd/use.go's
// helper. Cheap 250 ms TCP dial; returns true iff something
// accepts the connection. Kept here rather than imported so
// cmd/proxy stays leaf-importable from cmd/use's spawn path
// without an import cycle.
func portOccupied(listen string) bool {
	conn, err := net.DialTimeout("tcp", listen, 250*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// pickFreePortLocal — kernel-assigned port via bind(127.0.0.1:0).
// Named "Local" to leave the bare "pickFreePort" name available
// in case a future refactor lifts the helper into a shared package.
func pickFreePortLocal() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// portOfAddr returns the trailing port from "host:port" for the
// human-facing lsof hint in the port-conflict error message.
// Falls back to the full address if no colon is present.
func portOfAddr(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 && i+1 < len(addr) {
		return addr[i+1:]
	}
	return addr
}

// portOf extracts the trailing port from "host:port" so the
// lsof hint in the foreign-server error path is copy-paste-ready.
// Falls back to the full listen string if no colon is present so
// the hint still works for the user.
func portOf(listen string) string {
	if i := strings.LastIndex(listen, ":"); i >= 0 && i+1 < len(listen) {
		return listen[i+1:]
	}
	return listen
}
