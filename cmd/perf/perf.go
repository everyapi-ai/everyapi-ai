// Package perf wires `everyapi stats perf` — a per-model performance summary
// (success rate / latency / throughput) of the gateway's relay traffic.
// Complements `everyapi stats upstream` (provider-side status). The
// /api/perf-metrics/summary endpoint is admin-only (it exposes internal
// upstream channel topology and runs heavy log scans), so this sends the
// logged-in credentials and an anonymous call gets 401; pass --base to point
// at a non-default gateway.
package perf

import (
	"errors"
	"flag"
	"sort"

	"github.com/everyapi-ai/everyapi-ai/internal/cliargs"
	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

func Run(args []string) error {
	fs := flag.NewFlagSet("perf", flag.ContinueOnError)
	hours := fs.Int("hours", 24, "window in hours")
	baseFlag := fs.String("base", "", "gateway base URL (default: your gateway, else the public one)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := cliargs.RejectPositionals(fs); err != nil {
		return err
	}
	if *hours <= 0 {
		// A non-positive window would print a misleading header (e.g. "0h")
		// while the backend silently falls back to its default; normalize.
		*hours = 24
	}

	// /api/perf-metrics/summary is admin-only — send credentials when present
	// so a logged-in admin gets data instead of a 401.
	base := config.ResolveAPIBase(*baseFlag)
	client := api.New(base, "")
	creds, lerr := config.Load()
	if lerr != nil && !errors.Is(lerr, config.ErrNoCredentials) {
		// A real load failure (corrupt/unreadable credentials.json) must
		// not masquerade as "not logged in" and surface downstream as an
		// opaque 401 — report it directly.
		return lerr
	}
	if creds != nil {
		client = api.New(base, creds.AccessToken).WithUserID(creds.UserID)
	}
	models, err := client.GetPerfSummary(cliout.WithCtx(), *hours)
	if err != nil {
		return err
	}
	if len(models) == 0 {
		cliout.Println(i18n.T("perf.none"))
		return nil
	}

	// Most-used models first — that's what a buyer comparing routes cares
	// about, and it keeps low-sample noise at the bottom.
	sort.Slice(models, func(i, j int) bool { return models[i].RequestCount > models[j].RequestCount })

	cliout.Printf(i18n.T("perf.header")+"\n", *hours)
	for _, m := range models {
		// SuccessRate is already a 0–100 percentage from the backend.
		cliout.Printf("  %-30s  succ=%5.1f%%  avg=%6dms  tps=%6.1f  n=%d\n",
			cliout.Sanitize(m.ModelName), m.SuccessRate, m.AvgLatencyMs, m.AvgTps, m.RequestCount)
	}
	return nil
}
