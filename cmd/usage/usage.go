// Package usage wires `everyapi stats usage` — day-by-day quota_data
// rows for the caller. Backend caps the window at 30 days; we
// default to the last 7 so a bare `everyapi stats usage` is one screen
// of useful output.
package usage

import (
	"errors"
	"flag"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/everyapi-ai/everyapi-ai/internal/cliargs"
	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

const usageDoc = `everyapi stats usage — day-by-day quota usage

USAGE
  everyapi stats usage [--days N] [--since <window|ts>] [--until <ts>] [--per-day | --per-model]

FLAGS
  --days  <int>          Lookback in days (default 7; max 30 per backend cap)
  --since <window|ts>    Window start; overrides --days
  --until <ts>           Window end (default: now)
  --per-day              Group by day (default rollup)
  --per-model            Group by model
`

func Run(args []string) error {
	fs := flag.NewFlagSet("usage", flag.ContinueOnError)
	days := fs.Int("days", 7, "lookback in days")
	sinceStr := fs.String("since", "", "window start")
	untilStr := fs.String("until", "", "window end")
	fs.Bool("per-day", true, "group by day (default)") // discoverable selector; per-day is the default render path, conflict-checked against --per-model below
	perModel := fs.Bool("per-model", false, "group by model")
	if len(args) > 0 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h") {
		cliout.Println(usageDoc)
		return nil
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := cliargs.RejectPositionals(fs); err != nil {
		return err
	}
	// --per-day / --per-model are mutually exclusive grouping selectors.
	// --per-day defaults true (it IS the default render), so only a user
	// who EXPLICITLY passes --per-day alongside --per-model conflicts;
	// surface that instead of silently preferring --per-model.
	perDayExplicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "per-day" {
			perDayExplicit = true
		}
	})
	if perDayExplicit && *perModel {
		return errors.New("--per-day and --per-model are mutually exclusive")
	}
	if *days < 0 || *days > 36500 {
		return errors.New("--days out of range (0-36500)")
	}
	now := time.Now()
	start := now.Add(-time.Duration(*days) * 24 * time.Hour).Unix()
	if *sinceStr != "" {
		s, err := parseWindow(*sinceStr, now)
		if err != nil {
			return err
		}
		start = s
	}
	end := int64(0)
	if *untilStr != "" {
		e, err := parseWindow(*untilStr, now)
		if err != nil {
			return err
		}
		end = e
	}
	creds, err := config.Load()
	if errors.Is(err, config.ErrNoCredentials) {
		return errors.New(i18n.T("auth.not_logged_in"))
	}
	if err != nil {
		return err
	}
	client := api.ForCredentials(creds)
	rows, err := client.UserQuotaDates(cliout.WithCtx(), start, end)
	if err != nil {
		if api.IsUnauthorized(err) {
			return errors.New(i18n.T("auth.session_expired"))
		}
		return err
	}
	if len(rows) == 0 {
		cliout.Println(i18n.T("usage.no_rows"))
		return nil
	}
	if *perModel {
		renderPerModel(rows)
		return nil
	}
	renderPerDay(rows)
	return nil
}

func renderPerDay(rows []api.QuotaDay) {
	type agg struct {
		quota, count, tokens int
	}
	byDay := map[string]*agg{}
	for _, r := range rows {
		key := time.Unix(r.CreatedAt, 0).Format("2006-01-02")
		a, ok := byDay[key]
		if !ok {
			a = &agg{}
			byDay[key] = a
		}
		a.quota += r.Quota
		a.count += r.Count
		a.tokens += r.TokenUsed
	}
	days := make([]string, 0, len(byDay))
	for d := range byDay {
		days = append(days, d)
	}
	sort.Strings(days)
	cliout.Printf(i18n.T("usage.day_by_day")+"\n", len(days))
	total := agg{}
	for _, d := range days {
		a := byDay[d]
		cliout.Printf("  %s  quota=%-10d  calls=%-6d  tokens=%d\n", d, a.quota, a.count, a.tokens)
		total.quota += a.quota
		total.count += a.count
		total.tokens += a.tokens
	}
	cliout.Printf("  ---------- total: quota=%d  calls=%d  tokens=%d\n", total.quota, total.count, total.tokens)
}

func renderPerModel(rows []api.QuotaDay) {
	type agg struct {
		quota, count, tokens int
	}
	byModel := map[string]*agg{}
	for _, r := range rows {
		a, ok := byModel[r.ModelName]
		if !ok {
			a = &agg{}
			byModel[r.ModelName] = a
		}
		a.quota += r.Quota
		a.count += r.Count
		a.tokens += r.TokenUsed
	}
	models := make([]string, 0, len(byModel))
	for m := range byModel {
		models = append(models, m)
	}
	sort.Slice(models, func(i, j int) bool { return byModel[models[i]].quota > byModel[models[j]].quota })
	cliout.Printf(i18n.T("usage.per_model")+"\n", len(models))
	total := agg{}
	for _, m := range models {
		a := byModel[m]
		cliout.Printf("  %-40s  quota=%-10d  calls=%-6d  tokens=%d\n", cliout.Sanitize(m), a.quota, a.count, a.tokens)
		total.quota += a.quota
		total.count += a.count
		total.tokens += a.tokens
	}
	cliout.Printf("  total: quota=%d  calls=%d  tokens=%d\n", total.quota, total.count, total.tokens)
}

// parseWindow mirrors cmd/log.parseWindow — duplicated rather than
// extracted so cmd/usage doesn't depend on cmd/log just for a six-
// line helper. If a third caller appears, lift both into an
// internal package.
func parseWindow(s string, now time.Time) (int64, error) {
	if s == "" {
		return 0, nil
	}
	if n, err := time.ParseDuration(s); err == nil {
		return now.Add(-n).Unix(), nil
	}
	// Shorthand day windows: time.ParseDuration doesn't know the 'd'
	// unit, so strip a trailing 'd' and multiply by 24h (mirrors
	// cmd/log.parseWindow so `stats usage --since 7d` matches `stats log`).
	if strings.HasSuffix(s, "d") {
		if n, err := strconv.Atoi(s[:len(s)-1]); err == nil {
			if n < 0 || n > 36500 {
				return 0, errors.New("--since/--until: day count out of range (0–36500)")
			}
			return now.Add(-time.Duration(n) * 24 * time.Hour).Unix(), nil
		}
	}
	// Bare integer = absolute Unix seconds. strconv.ParseInt reports
	// overflow, unlike the old hand-rolled digit loop which silently
	// wrapped on out-of-range input. ParseInt also accepts a leading
	// '-', so reject negative values — a negative Unix timestamp is
	// never a valid window bound and would otherwise sail through.
	ts, err := strconv.ParseInt(s, 10, 64)
	if err != nil || ts < 0 {
		return 0, errors.New("--since/--until: must be a Go duration (e.g. 24h) or Unix-seconds integer")
	}
	return ts, nil
}
