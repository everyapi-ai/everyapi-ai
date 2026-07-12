package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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
// `start` runs a standalone sanitizer proxy (foreground, or --detach'd
// with a PID file for `stop`/`status`) for pointing your own SDK at it.
// `everyapi use <tool>` does NOT drive this command — it runs its own
// sanitizer in-process for the lifetime of the launched tool.
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
  --upstream <url>                Upstream gateway (default from credentials /
                                  settings, or https://api.everyapi.ai)
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
proxy handles the rest. ('everyapi use <tool>' does NOT use this command —
it runs its own sanitizer in-process for the launched tool's lifetime.)
`

func proxyUsageErr() error {
	return fmt.Errorf("missing subcommand\n\n%s", proxyUsage)
}

func rejectProxyPositionals(fs *flag.FlagSet) error {
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", fs.Args())
	}
	return nil
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
	parentPID := fs.Int("parent-pid", 0, "shut down when this pid exits (auto-cleanup for a script that --detach'd this proxy)")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w\n\n%s", err, proxyUsage)
	}
	if err := rejectProxyPositionals(fs); err != nil {
		return err
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

	// Refuse to start if another instance is already running — checked
	// BEFORE the port-conflict fallback and the --detach re-exec so a
	// redundant `proxy start` (foreground or --detach) reports the clean
	// "already running" message instead of auto-falling to an ephemeral
	// port and spawning a doomed child that then times out unhealthy.
	// A stale PID file (previous proxy died without cleanup) is cleared
	// transparently; a live PID returns an error so the user knows. We
	// use processAlive (not the health probe) here on purpose: refusing
	// while *any* live process holds that recorded PID is the
	// conservative choice against double-binding the port.
	if pid, _, ok := readPIDFile(); ok {
		if processAlive(pid) {
			return fmt.Errorf("sanitizer proxy already running (pid=%d); use 'everyapi proxy stop' to stop it", pid)
		}
		// Stale; clear it before we claim ownership ourselves.
		_ = removePIDFile()
	}

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
			// Any error here — including an Esc/cancel — aborts `proxy
			// start`, the same as every other interactive prompt in the
			// CLI. (This branch is interactive-only, where YesNo returns
			// nil or ErrPickCancelled; io.EOF arises solely on the non-TTY
			// ReadString path, which is gated out here, so there is no EOF
			// "fall through to foreground" case to handle.)
			return perr
		}
		*detach = bg
	}
	if portOccupied(*listen) {
		if listenExplicit {
			return fmt.Errorf("listen %s is held by another process; pick a different --listen or stop the foreign service ('lsof -iTCP:%s -sTCP:LISTEN' will name it)",
				*listen, portOfAddr(*listen))
		}
		// Default 8888 collision (typical: a SearXNG / dev tool
		// holding 8888). Quietly pick a free port instead of
		// crashing — the chosen address ends up in the PID file
		// so 'proxy status' / 'proxy stop' find it automatically.
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

	// Bind the listener BEFORE advertising the PID. writePIDFile records
	// our listen address so proxyStop/IsRunning can health-probe it; if we
	// wrote the PID first (the old srv.Run path bound inside Run, after the
	// file existed) a probe landing in the gap would see no listener, read
	// that as "dead", and destructively remove a live proxy's PID file.
	// With the socket already in LISTEN state, any probe arriving after the
	// file exists connects and gets answered once Serve's Accept loop runs
	// microseconds later. srv.Serve re-checks the loopback invariant against
	// the actual listener (see sanitizer.Server.Serve).
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", *listen, err)
	}
	// Record the ACTUAL bound address, not the requested one: with a
	// kernel-assigned port (`--listen 127.0.0.1:0`) *listen still says
	// ":0", so persisting it would leave proxyStop/IsRunning probing a
	// port nothing listens on — the proxy would be unmanageable. The
	// default-collision path already rewrites *listen to the concrete
	// fallback port; listener.Addr() covers the explicit :0 case too.
	boundAddr := listener.Addr().String()

	if err := writePIDFile(os.Getpid(), boundAddr); err != nil {
		_ = listener.Close()
		return fmt.Errorf("write pid file: %w", err)
	}
	defer func() { _ = removePIDFile() }()

	// Cancel on SIGINT / SIGTERM for graceful shutdown.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cliout.Printf(i18n.T("proxy.start_listening")+"\n", boundAddr)
	cliout.Printf(i18n.T("proxy.start_upstream")+"\n", resolvedUpstream)
	cliout.Printf(i18n.T("proxy.start_detectors")+"\n", len(detectors))
	cliout.Printf(i18n.T("proxy.start_point_sdk")+"\n", boundAddr)
	cliout.Println(i18n.T("proxy.start_ctrl_c"))
	cliout.Println("")

	return srv.Serve(ctx, listener)
}

// proxyStop reads the PID file and asks the recorded process to shut
// down (SIGTERM on Unix, TerminateProcess on Windows — see
// terminateProcess). Silently succeeds when there's nothing running (a
// no-op is the expected behaviour after a `Ctrl+C` exit that already
// cleared the file). Surfaces an error only if the PID file points at a
// still-alive process we couldn't signal — and in that case it leaves
// the PID file in place so the still-running proxy keeps a CLI handle.
func proxyStop(args []string) error {
	fs := flag.NewFlagSet("proxy stop", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := rejectProxyPositionals(fs); err != nil {
		return err
	}
	pid, listen, ok := readPIDFile()
	if !ok {
		cliout.Println(i18n.T("proxy.stop_not_running"))
		return nil
	}
	// Don't signal a PID we can't confirm is our proxy. After a
	// non-graceful death (SIGKILL/crash) the PID file is left behind, and
	// the OS may have recycled that PID for an unrelated same-user
	// process. When the PID file recorded a listen address (current
	// format), probe its sanitizer health endpoint; only proceed if our
	// proxy answers there. Legacy single-PID files (empty listen) fall
	// back to a liveness-only check.
	if listen != "" {
		if !sanitizerHealthAt(listen) {
			// Either gone, or the PID now belongs to something that isn't
			// our proxy. Drop the stale file; never signal it.
			_ = removePIDFile()
			cliout.Println(i18n.T("proxy.stop_was_not_running"))
			return nil
		}
	} else if !processAlive(pid) {
		_ = removePIDFile()
		cliout.Println(i18n.T("proxy.stop_was_not_running"))
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		// Unix never errors here; on Windows it means the process is
		// already gone, so clearing the file is safe.
		_ = removePIDFile()
		return fmt.Errorf("find process %d: %w", pid, err)
	}
	if err := terminateProcess(proc); err != nil {
		// If the process is genuinely still alive, the signal failed for
		// some other reason (permission denied, etc.). Do NOT remove the
		// PID file — that would orphan a still-running detached proxy
		// with no CLI handle. Leave the file and surface the error.
		if processAlive(pid) {
			return fmt.Errorf("signal pid %d: %w", pid, err)
		}
		// Process is gone (raced us between the probe and the signal).
		// Clean up the stale file.
		_ = removePIDFile()
		cliout.Println(i18n.T("proxy.stop_was_not_running"))
		return nil
	}
	cliout.Printf(i18n.T("proxy.stop_sent_sigterm"), pid)
	// Wait for the process to actually exit before reporting success.
	// The server uses a 5s graceful-shutdown grace, so poll a hair
	// longer (6s) — a 3s deadline expires mid-shutdown and falsely
	// reports "still alive" for a proxy that is cleanly draining.
	deadline := time.Now().Add(6 * time.Second)
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

// builtinDetectorDesc returns the localized one-line summary for a
// built-in detector. Translations live in the CLI's locale files
// (proxy.detector.<name>) rather than the SDK, which stays UI-agnostic;
// the literal token prefixes (sk-ant-…, AIza…, ghp_/…) are kept verbatim
// inside each translation since they're the actual match patterns. Falls
// back to the SDK's English description for any detector that doesn't yet
// have a locale key, so a newly-added detector still renders something.
func builtinDetectorDesc(name string) string {
	key := "proxy.detector." + name
	if d := i18n.T(key); d != key {
		return d
	}
	return sanitizer.DescribeBuiltin(name)
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
	if err := rejectProxyPositionals(fs); err != nil {
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
	enabled := make(map[string]bool, len(fc.Enabled))
	for _, n := range fc.Enabled {
		enabled[n] = true
	}
	// Two detector classes with OPPOSITE defaults: default-on built-ins are
	// active unless listed in Disabled; opt-in detectors (high false-positive,
	// e.g. luhn_credit_card) are inactive unless listed in Enabled. The wizard
	// must track BOTH lists — previously it only rebuilt Disabled, so a
	// toggled-on opt-in was silently dropped (never written to Enabled, so
	// BuildDetectors never activated it) and was also mis-preselected as
	// already active.
	defaultOn := sanitizer.BuiltinDetectors()
	optInNames := sanitizer.OptInDetectorNames()
	isOptIn := make(map[string]bool, len(optInNames))
	for _, n := range optInNames {
		isOptIn[n] = true
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
		labels[i] = fmt.Sprintf("%-*s — %s", nameWidth, name, builtinDetectorDesc(name))
	}
	// Preselect by each detector's real current state: a default-on detector
	// iff it isn't disabled, an opt-in detector iff it's explicitly enabled.
	preselected := make([]string, 0, len(allDetectors))
	for _, name := range allDetectors {
		if isOptIn[name] {
			if enabled[name] {
				preselected = append(preselected, name)
			}
		} else if !disabled[name] {
			preselected = append(preselected, name)
		}
	}
	selectedEnabled, err := cliprompt.PickMany(
		i18n.T("proxy.configure_builtin_prompt"),
		labels, allDetectors, preselected)
	if err != nil {
		return err
	}
	// Rebuild BOTH lists from the selection: a default-on name absent from
	// the selection goes to Disabled; an opt-in name present in it goes to
	// Enabled (a full rebuild, so an unchecked opt-in drops out of Enabled
	// and thus turns off). Opt-in names are never written to Disabled, nor
	// default-on names to Enabled.
	selSet := make(map[string]bool, len(selectedEnabled))
	for _, name := range selectedEnabled {
		selSet[name] = true
	}
	fc.Disabled = fc.Disabled[:0]
	for _, d := range defaultOn {
		if name := d.Name(); !selSet[name] {
			fc.Disabled = append(fc.Disabled, name)
		}
	}
	fc.Enabled = fc.Enabled[:0]
	for _, name := range optInNames {
		if selSet[name] {
			fc.Enabled = append(fc.Enabled, name)
		}
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
// IsRunning reports whether OUR sanitizer proxy is up. The pid file must
// exist AND, when it recorded a listen address (current format), our
// sanitizer must answer on its health endpoint — a bare pid-liveness
// check would lie after the previous proxy crashed and the OS recycled
// its PID for an unrelated process. Legacy single-PID files (empty
// listen) fall back to a liveness-only check. A stale/foreign PID file is
// removed as a side effect so the next caller doesn't keep seeing a
// phantom proxy. Callers (`everyapi version uninstall`'s plan-render
// block) rely on this returning false for stale leftovers so the
// confirmation text isn't misleading.
func IsRunning() bool {
	pid, listen, ok := readPIDFile()
	if !ok {
		return false
	}
	if listen != "" {
		if sanitizerHealthAt(listen) {
			return true
		}
		// Not our proxy (dead, or PID reused). Drop the stale file.
		_ = removePIDFile()
		return false
	}
	return processAlive(pid)
}

// sanitizerHealthAt reports whether OUR sanitizer proxy answers at
// http://<listen>/__sanitizer/health — a 200 with the "ok" body that
// handleHealth serves. Anything else (network error, non-200, or a
// foreign service squatting a reused port) is "not our proxy". This
// mirrors cmd.sanitizerHealthy, replicated here because that helper is
// unexported in a different package.
func sanitizerHealthAt(listen string) bool {
	// This boolean gates DESTRUCTIVE control flow: proxyStop() and
	// IsRunning() delete the PID file (and skip SIGTERM) when it's false,
	// and IsRunning() drives the uninstall stop-step. A single 250ms probe
	// could time out against a live-but-momentarily-slow proxy (loaded /
	// swapping host), orphaning it — the port stays held and it becomes
	// un-stoppable via `proxy stop`. Use a forgiving timeout with a few
	// retries so only a genuinely dead/foreign endpoint reads as false. A
	// closed port refuses the connection immediately, so the retries add
	// no latency on the common dead case.
	client := &http.Client{Timeout: time.Second}
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(100 * time.Millisecond)
		}
		resp, err := client.Get("http://" + listen + "/__sanitizer/health")
		if err != nil {
			continue
		}
		ok := resp.StatusCode == 200
		var body []byte
		if ok {
			body, _ = io.ReadAll(io.LimitReader(resp.Body, 16))
		}
		resp.Body.Close()
		if ok && strings.TrimSpace(string(body)) == "ok" {
			return true
		}
	}
	return false
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

// ResolveListen returns the address the running proxy recorded in its
// PID file — it may have picked a free port when the 127.0.0.1:8888
// default was already taken — falling back to that default only when no
// PID file is on disk. Exported so out-of-package diagnostics (cmd/doctor)
// probe the same address `proxy status` does instead of hardcoding 8888.
func ResolveListen() string {
	if _, recorded, ok := readPIDFile(); ok && recorded != "" {
		return recorded
	}
	return "127.0.0.1:8888"
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
	for time.Now().Before(deadline) {
		// Match the stop/status liveness criterion (HTTP 200 AND body
		// "ok"), not just any 200 — a foreign service squatting <listen>
		// can also answer 200 on this path, and we must not report its
		// presence as our detached proxy having come up.
		if sanitizerHealthAt(listen) {
			cliout.Printf(i18n.T("proxy.started_pid")+"\n", cmd.Process.Pid, listen)
			cliout.Printf(i18n.T("proxy.started_logs")+"\n", logPath)
			return nil
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
	if err := rejectProxyPositionals(fs); err != nil {
		return err
	}
	addr := *listen
	if addr == "" {
		addr = ResolveListen()
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + addr + "/__sanitizer/status")
	if err != nil {
		// Most likely: connection refused → proxy not running.
		fmt.Fprintln(os.Stderr, i18n.T("proxy.status_not_running"))
		fmt.Fprintln(os.Stderr, i18n.T("proxy.status_start_hint"))
		return fmt.Errorf("sanitizer proxy is not running at %s", addr)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !looksLikeSanitizerJSON(body, resp.Header.Get("Content-Type")) {
		ct := resp.Header.Get("Content-Type")
		fmt.Fprintf(os.Stderr, i18n.T("proxy.status_wrong_server_line1"),
			addr, resp.StatusCode, ct)
		fmt.Fprintln(os.Stderr, i18n.T("proxy.status_wrong_server_line2"))
		fmt.Fprintf(os.Stderr, i18n.T("proxy.status_wrong_server_line3")+"\n", portOf(addr))
		return fmt.Errorf("service at %s is not the EveryAPI sanitizer", addr)
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

// portOccupied — cheap 250 ms TCP dial; returns true iff something
// accepts the connection. Used by `proxy start` to detect a default-port
// collision before binding.
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
