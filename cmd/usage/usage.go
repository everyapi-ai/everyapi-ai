// Package usage wires `everyapi stats usage` — day-by-day quota_data
// rows for the caller. Backend caps the window at 30 days; we
// default to the last 7 so a bare `everyapi stats usage` is one screen
// of useful output.
package usage

import (
	"errors"
	"flag"
	"sort"
	"time"

	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
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
	perDay := fs.Bool("per-day", true, "group by day")
	perModel := fs.Bool("per-model", false, "group by model")
	if len(args) > 0 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h") {
		cliout.Println(usageDoc)
		return nil
	}
	if err := fs.Parse(args); err != nil {
		return err
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
	client := api.New(creds.APIBase, creds.AccessToken).WithUserID(creds.UserID)
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
	_ = *perDay // --per-day is the default and overrides nothing else
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
		cliout.Printf("  %-40s  quota=%-10d  calls=%-6d  tokens=%d\n", m, a.quota, a.count, a.tokens)
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
	// Bare integer = absolute Unix seconds. time.Parse can't take
	// it so call ParseInt directly.
	var ts int64
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, errors.New("--since/--until: must be a Go duration (e.g. 24h) or Unix-seconds integer")
		}
		ts = ts*10 + int64(ch-'0')
	}
	return ts, nil
}
