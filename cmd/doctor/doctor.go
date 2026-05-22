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
	"os/exec"
	"time"

	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/tools"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

const usage = `everyapi doctor — self-check (creds, gateway, sanitizer, tools)

USAGE
  everyapi doctor
`

// Run runs every check in order, prints them as they complete (not
// all-at-once at the end — so a hanging network probe is obvious),
// and returns a non-nil error if any FAIL row landed. WARN rows do
// not fail the command — they're advisory.
func Run(args []string) error {
	if len(args) > 0 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h") {
		cliout.Println(usage)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	report := newReport()
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

	report.run("gateway reachable", func() (string, string, error) {
		st, err := client.GetStatus(ctx)
		if err != nil {
			return "", "", err
		}
		return fmt.Sprintf("quota_per_unit=%g", st.QuotaPerUnit), "", nil
	})

	report.run("user session authenticated", func() (string, string, error) {
		self, err := client.GetSelf(ctx)
		if err != nil {
			if api.IsUnauthorized(err) {
				return "", "re-run 'everyapi login'", err
			}
			return "", "", err
		}
		return fmt.Sprintf("logged in as %s (id=%d)", self.Username, self.ID), "", nil
	})

	report.run("at least one relay token exists", func() (string, string, error) {
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

	for _, name := range tools.Names() {
		name := name
		report.run("tool "+name+" on PATH", func() (string, string, error) {
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

type report struct {
	failed bool
	warned bool
}

func newReport() *report { return &report{} }

func (r *report) run(name string, fn func() (detail, hint string, err error)) {
	detail, hint, err := fn()
	if err == nil {
		cliout.Printf("  [OK]   %-32s  %s\n", name, detail)
		return
	}
	if isSoft(err) {
		r.warned = true
		cliout.Printf("  [WARN] %-32s  %s\n", name, detail)
		if hint != "" {
			cliout.Printf("         hint: %s\n", hint)
		}
		return
	}
	r.failed = true
	cliout.Printf("  [FAIL] %-32s  %s\n", name, err.Error())
	if hint != "" {
		cliout.Printf("         hint: %s\n", hint)
	}
}

func (r *report) summarize() {
	switch {
	case r.failed:
		cliout.Println("\nResult: one or more required checks failed.")
	case r.warned:
		cliout.Println("\nResult: ok with warnings (advisory; tool will still run).")
	default:
		cliout.Println("\nResult: all green.")
	}
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
