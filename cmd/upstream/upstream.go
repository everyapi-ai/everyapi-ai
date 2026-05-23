// Package upstream wires `everyapi upstream` — a Statuspage-style
// health rollup of the upstream providers the gateway relays to
// (OpenAI / Anthropic / etc.). The endpoint is public, so this works
// before login; pass --base to point at a non-default gateway.
package upstream

import (
	"flag"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

func Run(args []string) error {
	fs := flag.NewFlagSet("upstream", flag.ContinueOnError)
	baseFlag := fs.String("base", "", "gateway base URL (default: your gateway, else the public one)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Public endpoint — no token needed. --base wins, else the logged-in
	// gateway, else the public default.
	providers, err := api.New(config.ResolveAPIBase(*baseFlag), "").GetUpstreamStatus(cliout.WithCtx())
	if err != nil {
		return err
	}
	if len(providers) == 0 {
		cliout.Println(i18n.T("upstream.none"))
		return nil
	}
	for _, p := range providers {
		cliout.Printf("%s  %-12s %s\n", indicatorMark(p.Indicator), p.Name, indicatorLabel(p.Indicator))
		if p.Description != "" {
			cliout.Printf("      %s\n", p.Description)
		}
		for _, comp := range p.Components {
			cliout.Printf("      - %s: %s\n", comp.Name, comp.Status)
		}
		for _, inc := range p.Incidents {
			cliout.Printf("      ! %s (%s / %s)\n", inc.Name, inc.Status, inc.Impact)
		}
	}
	return nil
}

// indicatorMark maps the Statuspage indicator to a short ASCII tag —
// no color dependency, stays readable when piped to a file.
func indicatorMark(indicator string) string {
	switch indicator {
	case "none":
		return "[ ok ]"
	case "minor":
		return "[warn]"
	case "major", "critical":
		return "[down]"
	default:
		return "[ ?  ]"
	}
}

func indicatorLabel(indicator string) string {
	switch indicator {
	case "none":
		return i18n.T("upstream.ind_none")
	case "minor":
		return i18n.T("upstream.ind_minor")
	case "major":
		return i18n.T("upstream.ind_major")
	case "critical":
		return i18n.T("upstream.ind_critical")
	default:
		return i18n.T("upstream.ind_unknown")
	}
}
