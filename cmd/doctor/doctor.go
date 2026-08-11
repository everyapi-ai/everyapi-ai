// Package doctor wires `everyapi doctor` — local self-check that
// bundles the diagnostics a user (or their support thread) would
// otherwise piece together from `everyapi auth status` + `everyapi proxy
// status` + manual `which claude/codex/gemini`. Output is one row
// per check with a clear [OK|WARN|FAIL] prefix so it can be pasted
// into a support ticket without further annotation.
//
// `--format=json` emits the same checks as data instead of prose, and an
// optional tool name narrows the tool section to one client. Both exist for
// EveryAPI Connect, which renders the report in its own window: the desktop
// app knows nothing about how any client is wired — that knowledge lives here,
// in the same package that does the wiring — so it asks this command rather
// than reimplementing the checks against a second copy of the rules.
package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"golang.org/x/term"

	"github.com/everyapi-ai/everyapi-ai/cmd/proxy"
	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/internal/style"
	"github.com/everyapi-ai/everyapi-ai/internal/tools"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

// machineProtocolVersion is the shape contract for --format=json. Bump it when
// a consumer that pinned the old shape would misread the new one.
const machineProtocolVersion = 1

// Run runs every check in order and returns a non-nil error if any FAIL row
// landed. WARN rows do not fail the command — they're advisory.
//
// In human mode checks print as they complete (not all-at-once at the end — so
// a hanging network probe is obvious). In machine mode they are collected and
// emitted as one JSON document, because a partial document is not parseable.
func Run(args []string) error {
	if len(args) > 0 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h") {
		cliout.Println(i18n.T("doctor.usage"))
		return nil
	}

	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	if machineRequested(args) {
		fs.SetOutput(io.Discard)
	}
	format := fs.String("format", "human", "output format (human or json)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	machine := *format == "json"
	if !machine && *format != "human" {
		return fmt.Errorf("unsupported format %q", *format)
	}

	// One optional positional: the tool to narrow the tool section to. flag
	// stops at the first non-flag argument, so parse again past it — otherwise
	// `doctor claude --format=json`, the order everyone types, would read the
	// flag as a second tool name.
	var only string
	if rest := fs.Args(); len(rest) > 0 {
		only = rest[0]
		if err := fs.Parse(rest[1:]); err != nil {
			return err
		}
		machine = *format == "json"
		if !machine && *format != "human" {
			return fmt.Errorf("unsupported format %q", *format)
		}
		if fs.NArg() != 0 {
			return errors.New("doctor accepts at most one tool name")
		}
		if _, err := tools.Lookup(only); err != nil {
			return err
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	report := newReport(machine)

	report.section(i18n.T("doctor.section.account"))
	report.run(i18n.T("doctor.check.creds"), func() (string, string, error) {
		creds, err := config.Load()
		if errors.Is(err, config.ErrNoCredentials) {
			return "", i18n.T("doctor.hint.login"), err
		}
		if err != nil {
			return "", "", err
		}
		return fmt.Sprintf("user_id=%d, base=%s", creds.UserID, creds.APIBase), "", nil
	})

	// Subsequent checks need creds; bail early if the first one
	// failed.
	creds, _ := config.Load()
	if creds == nil {
		report.summarize()
		return report.finish()
	}
	client := api.ForCredentials(creds)

	report.run(i18n.T("doctor.check.session"), func() (string, string, error) {
		self, err := client.GetSelf(ctx)
		if err != nil {
			// A dead token surfaces as a 401 OR, the legacy way, an
			// HTTP-200 envelope rejection (EnvelopeError) — both mean
			// "re-login", not a generic failure.
			var envErr *api.EnvelopeError
			if api.IsUnauthorized(err) || errors.As(err, &envErr) {
				return "", i18n.T("doctor.hint.relogin"), err
			}
			return "", "", err
		}
		return fmt.Sprintf(i18n.T("doctor.detail.logged_in"), self.Username, self.ID), "", nil
	})

	report.run(i18n.T("doctor.check.token"), func() (string, string, error) {
		toks, err := client.ListTokens(ctx)
		if err != nil {
			return "", "", err
		}
		enabled := 0
		for _, t := range toks {
			if t.Status == api.TokenStatusEnabled {
				enabled++
			}
		}
		if enabled == 0 {
			return fmt.Sprintf(i18n.T("doctor.detail.tokens_none"), len(toks)),
				i18n.T("doctor.hint.mint"),
				errSoft("no enabled tokens")
		}
		return fmt.Sprintf(i18n.T("doctor.detail.tokens"), enabled, len(toks)), "", nil
	})

	// A healthy session does NOT imply a working relay: /api/user/self runs
	// UserAuth and skips the quota/expiry gates that /v1/* enforces, so an
	// exhausted or disabled key passes every check above and still 401s the
	// moment a tool sends a request. This is the check that catches it.
	report.run(i18n.T("doctor.check.relay"), func() (string, string, error) {
		if creds.RelayKey == "" {
			// Resolving one would rewrite credentials.json, and a self-check
			// must not have side effects. Say so instead.
			return i18n.T("doctor.detail.relay_absent"), i18n.T("doctor.hint.relogin"), errSoft("no relay key cached")
		}
		gateway := config.ResolveAPIBaseForBase(creds.APIBase)
		err := api.New(gateway, creds.RelayKey).ProbeRelayToken(ctx)
		switch {
		case err == nil:
			return i18n.T("doctor.detail.relay_ok"), "", nil
		case api.IsUnauthorized(err):
			return i18n.T("doctor.detail.relay_rejected"), i18n.T("doctor.hint.relay_rejected"), err
		default:
			// Non-401 (5xx, network): no verdict on the key itself.
			return i18n.T("doctor.detail.relay_unknown"), "", errSoft(err.Error())
		}
	})

	report.section(i18n.T("doctor.section.gateway"))
	report.run(i18n.T("doctor.check.backend"), func() (string, string, error) {
		st, err := client.GetStatus(ctx)
		if err != nil {
			return "", "", err
		}
		return fmt.Sprintf("quota_per_unit=%g", st.QuotaPerUnit), "", nil
	})

	report.run(i18n.T("doctor.check.sanitizer"), func() (string, string, error) {
		// Sanitizer is best-effort. Probe the proxy's RECORDED listen
		// address (proxy start picks a free port when the 127.0.0.1:8888
		// default is taken and writes it to its PID file) on the liveness
		// path /__sanitizer/health with a 1s timeout — hardcoding 8888 here
		// would report a healthy proxy on a fallback port as "down". If no
		// socket answers we surface as WARN, not FAIL, because the sanitizer
		// is opt-in (--sanitize) and off by default.
		addr := proxy.ResolveListen()
		hc := &http.Client{Timeout: 1 * time.Second}
		resp, err := hc.Get("http://" + addr + "/__sanitizer/health")
		if err != nil {
			return i18n.T("doctor.detail.proxy_down"),
				i18n.T("doctor.hint.proxy_start"),
				errSoft(err.Error())
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Sprintf(i18n.T("doctor.detail.proxy_unhealthy"), resp.Status), "", errSoft(resp.Status)
		}
		return fmt.Sprintf(i18n.T("doctor.detail.proxy_ok"), addr), "", nil
	})

	report.section(i18n.T("doctor.section.tools"))
	names := tools.Names()
	if only != "" {
		names = []string{only}
	}
	for _, name := range names {
		name := name
		report.run(name, func() (string, string, error) {
			t, err := tools.Lookup(name)
			if err != nil {
				return "", "", err
			}
			path, err := tools.ResolveExec(t)
			if err != nil {
				return "", t.InstallHint, errSoft("not on PATH")
			}
			return path, "", nil
		})
	}

	report.summarize()
	return report.finish()
}

// machineRequested mirrors the check `everyapi auth status` uses, so a parse
// error under --format=json cannot leak flag-usage prose into the JSON stream.
func machineRequested(args []string) bool {
	for i, arg := range args {
		if arg == "--format=json" || arg == "-format=json" {
			return true
		}
		if (arg == "--format" || arg == "-format") && i+1 < len(args) && args[i+1] == "json" {
			return true
		}
	}
	return false
}

// --- report plumbing ---------------------------------------------

// Column widths chosen so the longest expected name + the longest
// status badge fit without ragged wrapping on an 80-col terminal.
// Names exceeding nameCol just push the badge right; better than
// truncating something the user is trying to copy into a ticket.
const nameCol = 28

const (
	statusOK   = "ok"
	statusWarn = "warn"
	statusFail = "fail"
)

// Check is one row of the report. Section and Name are localized display
// strings; Status is a stable identifier a consumer can branch on.
type Check struct {
	Section string `json:"section"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Detail  string `json:"detail,omitempty"`
	Hint    string `json:"hint,omitempty"`
}

// MachineReport is the --format=json document.
type MachineReport struct {
	Version int     `json:"version"`
	Status  string  `json:"status"`
	Checks  []Check `json:"checks"`
}

type report struct {
	failed     bool
	warned     bool
	totals     map[string]int
	sectionLed bool // suppresses the leading blank-line before the very first section
	badgeW     int  // display width of the widest localized status word

	// machine collects instead of printing: a JSON document has to be whole.
	machine        bool
	currentSection string
	checks         []Check
}

func newReport(machine bool) *report {
	r := &report{totals: map[string]int{}, machine: machine}
	for _, w := range []string{
		i18n.T("doctor.badge.ok"),
		i18n.T("doctor.badge.warn"),
		i18n.T("doctor.badge.fail"),
	} {
		if dw := style.Width(w); dw > r.badgeW {
			r.badgeW = dw
		}
	}
	return r
}

func (r *report) section(title string) {
	r.currentSection = title
	if r.machine {
		return
	}
	if r.sectionLed {
		cliout.Println("")
	}
	r.sectionLed = true
	cliout.Printf("%s\n", paint(title, ansiBold))
	// Underline by DISPLAY width — len(title) over-counts CJK titles.
	cliout.Printf("%s\n", paint(repeat("─", style.Width(title)), ansiDim))
}

func (r *report) run(name string, fn func() (detail, hint string, err error)) {
	detail, hint, err := fn()
	var status, word, color string
	switch {
	case err == nil:
		status, word, color = statusOK, i18n.T("doctor.badge.ok"), ansiGreen
		r.totals[statusOK]++
	case isSoft(err):
		status, word, color = statusWarn, i18n.T("doctor.badge.warn"), ansiYellow
		r.warned = true
		r.totals[statusWarn]++
	default:
		status, word, color = statusFail, i18n.T("doctor.badge.fail"), ansiRed
		r.failed = true
		r.totals[statusFail]++
		detail = err.Error()
	}
	if err == nil {
		hint = ""
	}
	if r.machine {
		r.checks = append(r.checks, Check{
			Section: r.currentSection,
			Name:    name,
			Status:  status,
			Detail:  detail,
			Hint:    hint,
		})
		return
	}
	// Reverse-video chip: the status word padded to badgeW with a
	// 1-space margin each side. The name column pads by DISPLAY width so
	// CJK labels keep the detail column aligned with ASCII rows.
	chip := paint(" "+word+repeat(" ", r.badgeW-style.Width(word))+" ", color, ansiInverse)
	cliout.Printf("  %s  %s  %s\n", chip, padTo(name, nameCol), detail)
	if hint != "" {
		cliout.Printf("  %s  %s  %s%s\n",
			repeat(" ", r.badgeW+2), repeat(" ", nameCol),
			paint(i18n.T("doctor.hint.prefix"), ansiDim), hint)
	}
}

// padTo right-pads s to w display columns (no-op when already wider).
func padTo(s string, w int) string {
	if d := w - style.Width(s); d > 0 {
		return s + repeat(" ", d)
	}
	return s
}

func (r *report) summarize() {
	if r.machine {
		return
	}
	cliout.Println("")
	var summary string
	switch {
	case r.failed:
		summary = paint(i18n.T("doctor.result.failed"), ansiRed, ansiBold)
	case r.warned:
		summary = paint(i18n.T("doctor.result.warned"), ansiYellow)
	default:
		summary = paint(i18n.T("doctor.result.green"), ansiGreen, ansiBold)
	}
	cliout.Printf("%s  "+i18n.T("doctor.tally")+"\n", summary,
		r.totals[statusOK], r.totals[statusWarn], r.totals[statusFail])
}

// finish emits the machine document when asked, then reports the verdict.
func (r *report) finish() error {
	if r.machine {
		if err := json.NewEncoder(cliout.Out).Encode(r.machineReport()); err != nil {
			return err
		}
	}
	return r.err()
}

func (r *report) machineReport() MachineReport {
	checks := r.checks
	if checks == nil {
		checks = []Check{}
	}
	status := statusOK
	switch {
	case r.failed:
		status = statusFail
	case r.warned:
		status = statusWarn
	}
	return MachineReport{Version: machineProtocolVersion, Status: status, Checks: checks}
}

func (r *report) err() error {
	if r.failed {
		return errors.New(i18n.T("doctor.failed_err"))
	}
	return nil
}

// errSoft / isSoft demote an error so a failed check renders as
// WARN instead of FAIL. Used for things that aren't blocking — no
// enabled tokens, sanitizer not running, optional tool missing.
type softErr struct{ s string }

func (e *softErr) Error() string { return e.s }
func errSoft(s string) error     { return &softErr{s: s} }
func isSoft(err error) bool {
	_, ok := err.(*softErr)
	return ok
}

// --- tiny ANSI wrapper (TTY-aware, NO_COLOR-aware) ----------------
//
// Doctor prints colored status badges + section headers, but only
// when the output is a real terminal. Captured into a file, piped
// to another program, or running under NO_COLOR=1, every paint()
// call short-circuits and just returns the unstyled text.
//
// Kept inline (not a shared internal/ansi package) because doctor
// is the only command that needs styling today; if a second comes
// along, extract.

const (
	ansiReset   = "\x1b[0m"
	ansiBold    = "\x1b[1m"
	ansiDim     = "\x1b[2m"
	ansiInverse = "\x1b[7m"
	ansiRed     = "\x1b[31m"
	ansiGreen   = "\x1b[32m"
	ansiYellow  = "\x1b[33m"
)

func paint(s string, codes ...string) string {
	if !colorEnabled() {
		return s
	}
	var buf string
	for _, c := range codes {
		buf += c
	}
	return buf + s + ansiReset
}

// colorEnabled is intentionally checked per-call: NO_COLOR could
// be flipped mid-run (rare, but cheap to honour) and stdout could
// be redirected partway through tests. The Fd() / IsTerminal()
// pair is the same shape cliprompt uses for its picker gate.
func colorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return term.IsTerminal(int(os.Stdout.Fd()))
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}
