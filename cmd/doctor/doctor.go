// Package doctor wires `everyapi doctor` — local self-check that
// bundles the diagnostics a user (or their support thread) would
// otherwise piece together from `everyapi status` + `everyapi proxy
// status` + manual `which claude/codex/gemini`. Output is one row
// per check with a clear [OK|WARN|FAIL] prefix so it can be pasted
// into a support ticket without further annotation.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"

	"golang.org/x/term"

	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/internal/tools"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

// Run runs every check in order, prints them as they complete (not
// all-at-once at the end — so a hanging network probe is obvious),
// and returns a non-nil error if any FAIL row landed. WARN rows do
// not fail the command — they're advisory.
func Run(args []string) error {
	if len(args) > 0 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h") {
		cliout.Println(i18n.T("doctor.usage"))
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	report := newReport()

	report.section(i18n.T("doctor.section.account"))
	report.run("credentials cached", func() (string, string, error) {
		creds, err := config.Load()
		if errors.Is(err, config.ErrNoCredentials) {
			return "", "run 'everyapi login' first", err
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
		return report.err()
	}
	client := api.New(creds.APIBase, creds.AccessToken).WithUserID(creds.UserID)

	report.run("session authenticated", func() (string, string, error) {
		self, err := client.GetSelf(ctx)
		if err != nil {
			if api.IsUnauthorized(err) {
				return "", "re-run 'everyapi login'", err
			}
			return "", "", err
		}
		return fmt.Sprintf("logged in as %s (id=%d)", self.Username, self.ID), "", nil
	})

	report.run("relay token mintable", func() (string, string, error) {
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
			return fmt.Sprintf("%d token(s), 0 enabled", len(toks)),
				"mint one with 'everyapi token create --name prod --unlimited'",
				errSoft("no enabled tokens")
		}
		return fmt.Sprintf("%d enabled / %d total", enabled, len(toks)), "", nil
	})

	report.section(i18n.T("doctor.section.gateway"))
	report.run("backend reachable", func() (string, string, error) {
		st, err := client.GetStatus(ctx)
		if err != nil {
			return "", "", err
		}
		return fmt.Sprintf("quota_per_unit=%g", st.QuotaPerUnit), "", nil
	})

	report.run("sanitizer proxy", func() (string, string, error) {
		// Sanitizer is best-effort. Probe the default listen
		// (loopback:8786 — current default) with a 1s timeout; if
		// no socket answers we surface as WARN, not FAIL, because
		// `--direct` mode bypasses it anyway.
		hc := &http.Client{Timeout: 1 * time.Second}
		resp, err := hc.Get("http://127.0.0.1:8786/healthz")
		if err != nil {
			return "not running (--direct will bypass it anyway)",
				"start with 'everyapi proxy start'",
				errSoft(err.Error())
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Sprintf("listening but unhealthy (%s)", resp.Status), "", errSoft(resp.Status)
		}
		return "listening on 127.0.0.1:8786", "", nil
	})

	report.section(i18n.T("doctor.section.tools"))
	for _, name := range tools.Names() {
		name := name
		report.run(name, func() (string, string, error) {
			t, err := tools.Lookup(name)
			if err != nil {
				return "", "", err
			}
			path, err := exec.LookPath(t.ExecName)
			if err != nil {
				return "", t.InstallHint, errSoft("not on PATH")
			}
			return path, "", nil
		})
	}

	report.summarize()
	return report.err()
}

// --- report plumbing ---------------------------------------------

// Column widths chosen so the longest expected name + the longest
// status badge fit without ragged wrapping on an 80-col terminal.
// Names exceeding nameCol just push the badge right; better than
// truncating something the user is trying to copy into a ticket.
const (
	nameCol      = 28
	statusBadgeW = 6 // "  ok  " / " warn " / " fail "
)

type report struct {
	failed     bool
	warned     bool
	totals     map[string]int
	sectionLed bool // suppresses the leading blank-line before the very first section
}

func newReport() *report {
	return &report{totals: map[string]int{}}
}

func (r *report) section(title string) {
	if r.sectionLed {
		cliout.Println("")
	}
	r.sectionLed = true
	cliout.Printf("%s\n", paint(title, ansiBold))
	cliout.Printf("%s\n", paint(repeat("─", len(title)), ansiDim))
}

func (r *report) run(name string, fn func() (detail, hint string, err error)) {
	detail, hint, err := fn()
	var badge, color string
	switch {
	case err == nil:
		badge, color = "  ok  ", ansiGreen
		r.totals["ok"]++
	case isSoft(err):
		badge, color = " warn ", ansiYellow
		r.warned = true
		r.totals["warn"]++
	default:
		badge, color = " fail ", ansiRed
		r.failed = true
		r.totals["fail"]++
		detail = err.Error()
	}
	cliout.Printf("  %s  %-*s  %s\n", paint(badge, color, ansiInverse), nameCol, name, detail)
	if err != nil && hint != "" {
		cliout.Printf("  %s  %-*s  %s%s\n",
			repeat(" ", statusBadgeW), nameCol, "", paint("hint: ", ansiDim), hint)
	}
}

func (r *report) summarize() {
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
		r.totals["ok"], r.totals["warn"], r.totals["fail"])
}

func (r *report) err() error {
	if r.failed {
		return errors.New("doctor reported failures (see rows above)")
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
