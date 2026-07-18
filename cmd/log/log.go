// Package log wires `everyapi stats log …` — buyer-visible request log
// telemetry. Three subs: list (most recent rows), stat (totals over
// a window), summary (per-model breakdown). All read-only and
// auth-bound to the calling user.
package log

import (
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/everyapi-ai/everyapi-ai/internal/cliargs"
	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

func Run(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		cliout.Println(i18n.T("log.usage"))
		if len(args) == 0 {
			return errors.New("missing subcommand (try 'everyapi stats log help')")
		}
		return nil
	}
	switch args[0] {
	case "list":
		return runList(args[1:])
	case "stat":
		return runStat(args[1:])
	case "summary":
		return runSummary(args[1:])
	default:
		cliout.Println(i18n.T("log.usage"))
		return fmt.Errorf("unknown 'log' subcommand %q", args[0])
	}
}

func newClient() (*api.Client, error) {
	creds, err := config.Load()
	if errors.Is(err, config.ErrNoCredentials) {
		return nil, errors.New(i18n.T("auth.not_logged_in"))
	}
	if err != nil {
		return nil, err
	}
	return api.ForCredentials(creds), nil
}

func classifyErr(err error) error {
	if err == nil {
		return nil
	}
	if api.IsUnauthorized(err) {
		return errors.New(i18n.T("auth.session_expired"))
	}
	return err
}

// parseWindow accepts shorthand windows (1h / 24h / 7d / 30d) or an
// absolute Unix-seconds integer. Empty → 0 (server-side default).
// Returning Unix seconds means everything downstream is the same
// shape the backend speaks; no timezone in the SDK boundary.
//
// Go's time.ParseDuration recognizes h/m/s/ms/us/ns but not d, so
// we strip a trailing 'd' and multiply by 24h before handing off.
func parseWindow(s string, now time.Time) (int64, error) {
	if s == "" {
		return 0, nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n, nil
	}
	durStr := s
	if strings.HasSuffix(durStr, "d") {
		n, err := strconv.Atoi(durStr[:len(durStr)-1])
		if err == nil {
			if n < 0 || n > 36500 {
				return 0, fmt.Errorf("--since/--until: %q day count out of range (0–36500)", s)
			}
			return now.Add(-time.Duration(n) * 24 * time.Hour).Unix(), nil
		}
	}
	d, err := time.ParseDuration(durStr)
	if err != nil {
		return 0, fmt.Errorf("--since/--until: %q is neither a Unix-seconds integer nor a duration (e.g. 24h, 7d)", s)
	}
	return now.Add(-d).Unix(), nil
}

func bindCommon(fs *flag.FlagSet, f *api.LogFilter, sinceStr, untilStr *string) {
	fs.StringVar(&f.TokenName, "token", "", "filter by token display name")
	fs.StringVar(&f.ModelName, "model", "", "filter by model")
	fs.StringVar(&f.Group, "group", "", "filter by routing group")
	fs.StringVar(sinceStr, "since", "", "window start (e.g. 24h) or Unix seconds")
	fs.StringVar(untilStr, "until", "", "window end (Unix seconds; default: now)")
	fs.IntVar(&f.Type, "type", 0, "log type filter (backend constants)")
}

func resolveWindow(f *api.LogFilter, sinceStr, untilStr string) error {
	now := time.Now()
	s, err := parseWindow(sinceStr, now)
	if err != nil {
		return err
	}
	e, err := parseWindow(untilStr, now)
	if err != nil {
		return err
	}
	f.Start, f.End = s, e
	return nil
}

func runList(args []string) error {
	fs := flag.NewFlagSet("log list", flag.ContinueOnError)
	var f api.LogFilter
	var sinceStr, untilStr string
	bindCommon(fs, &f, &sinceStr, &untilStr)
	fs.IntVar(&f.PageSize, "limit", 20, "page size")
	fs.IntVar(&f.Page, "page", 0, "1-based page index")
	fs.StringVar(&f.RequestID, "request-id", "", "filter to one upstream request id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := cliargs.RejectPositionals(fs); err != nil {
		return err
	}
	if err := resolveWindow(&f, sinceStr, untilStr); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	rows, total, err := client.ListUserLogs(cliout.WithCtx(), f)
	if err != nil {
		return classifyErr(err)
	}
	if len(rows) == 0 {
		cliout.Println(i18n.T("log.no_rows"))
		return nil
	}
	cliout.Printf(i18n.T("log.label.rows_window")+"\n", len(rows), total)
	for _, r := range rows {
		ts := time.Unix(r.CreatedAt, 0).Format("01-02 15:04:05")
		model := cliout.Sanitize(r.ModelName)
		if model == "" {
			model = "-"
		}
		cliout.Printf("  %s  [#%d] %s via %s — quota=%d, tokens=%d/%d, %dms, ch=#%d\n",
			ts, r.ID, model, emptyAs(cliout.Sanitize(r.TokenName), "(default)"),
			r.Quota, r.PromptTokens, r.CompletionTokens, r.UseTime, r.ChannelID)
		if r.RequestID != "" {
			cliout.Printf("    request_id: %s\n", cliout.Sanitize(r.RequestID))
		}
		if r.Content != "" {
			cliout.Printf("    %s\n", cliout.Sanitize(r.Content))
		}
	}
	return nil
}

func runStat(args []string) error {
	fs := flag.NewFlagSet("log stat", flag.ContinueOnError)
	var f api.LogFilter
	var sinceStr, untilStr string
	bindCommon(fs, &f, &sinceStr, &untilStr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := cliargs.RejectPositionals(fs); err != nil {
		return err
	}
	if err := resolveWindow(&f, sinceStr, untilStr); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	stat, err := client.SelfLogStat(cliout.WithCtx(), f)
	if err != nil {
		return classifyErr(err)
	}
	cliout.Printf(i18n.T("log.label.window")+"\n", windowLabel(f.Start), windowLabel(f.End))
	cliout.Printf("  quota: %d (gateway units)\n", stat.Quota)
	cliout.Printf("  rpm:   %.2f\n", stat.RPM)
	cliout.Printf("  tpm:   %.2f\n", stat.TPM)
	return nil
}

func runSummary(args []string) error {
	fs := flag.NewFlagSet("log summary", flag.ContinueOnError)
	sinceStr := fs.String("since", "168h", "window start (e.g. 168h, 7d) or Unix seconds")
	untilStr := fs.String("until", "", "window end (Unix seconds; default: now)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := cliargs.RejectPositionals(fs); err != nil {
		return err
	}
	now := time.Now()
	start, err := parseWindow(*sinceStr, now)
	if err != nil {
		return err
	}
	end, err := parseWindow(*untilStr, now)
	if err != nil {
		return err
	}
	if end == 0 {
		// Empty --until means "now"; the backend rejects an open upper bound
		// (endTimestamp <= 0) with "time span too large", so the default
		// `stats log summary` would always error. Bound it to now.
		end = now.Unix()
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	rows, err := client.UserLogModelSummary(cliout.WithCtx(), start, end)
	if err != nil {
		return classifyErr(err)
	}
	if len(rows) == 0 {
		cliout.Println(i18n.T("log.no_spend"))
		return nil
	}
	cliout.Printf(i18n.T("log.label.per_model_hdr")+"\n", windowLabel(start), windowLabel(end))
	totalQuota := 0
	for _, r := range rows {
		totalQuota += r.Quota
	}
	for _, r := range rows {
		kind := cliout.Sanitize(r.ChannelKindSlug)
		if kind == "" {
			kind = "(legacy)"
		}
		pct := 0.0
		if totalQuota > 0 {
			pct = 100.0 * float64(r.Quota) / float64(totalQuota)
		}
		cliout.Printf("  %-30s  quota=%-10d  count=%-6d  %5.1f%%  upstream=%s\n",
			cliout.Sanitize(r.ModelName), r.Quota, r.Count, pct, kind)
	}
	cliout.Printf("  total: quota=%d\n", totalQuota)
	return nil
}

func windowLabel(ts int64) string {
	if ts == 0 {
		return i18n.T("log.label.open")
	}
	return time.Unix(ts, 0).Format("2006-01-02 15:04:05")
}

func emptyAs(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
