// Package upstream wires `everyapi upstream` — a Statuspage-style
// health rollup of the upstream providers the gateway relays to
// (OpenAI / Anthropic / etc.). The endpoint is public, so this works
// before login; pass --base to point at a non-default gateway.
package upstream

import (
	"flag"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

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
	cliout.Printf("%s", render(providers))
	return nil
}

// detailIndent aligns a provider's detail block under its name:
// indicatorMark (6 cells) + the two spaces that follow it.
const detailIndent = "        "

// render turns the provider list into the printable rollup. Split out of
// Run so it can be unit-tested without swapping cliout.Out.
//
// Layout: one aligned line per provider, and an indented detail block
// ONLY when a provider is actually degraded. Operational sub-components
// carry no signal — the backend returns a pile of them per provider, so
// listing them under a green provider is the noise that made the raw
// output unreadable. The rollup line is the whole point; detail earns
// its space only when something is wrong.
func render(providers []api.UpstreamProvider) string {
	// Widest name in display cells (CJK-aware) sets the label column so
	// names of any width or script line up. fmt's %-Ns can't: it counts
	// runes, not terminal cells, so CJK / mixed-width names drift.
	nameW := 0
	for _, p := range providers {
		if w := lipgloss.Width(p.Name); w > nameW {
			nameW = w
		}
	}

	var b strings.Builder
	for _, p := range providers {
		pad := strings.Repeat(" ", nameW-lipgloss.Width(p.Name))
		fmt.Fprintf(&b, "%s  %s%s  %s\n", indicatorMark(p.Indicator), p.Name, pad, indicatorLabel(p.Indicator))

		// Keep only the components that actually carry a problem; the
		// backend includes operational ones, but a green component is
		// not worth a line.
		var broken []api.UpstreamComponent
		for _, c := range p.Components {
			if c.Status != "operational" {
				broken = append(broken, c)
			}
		}
		if isGreen(p.Indicator) && len(broken) == 0 && len(p.Incidents) == 0 {
			continue
		}

		// Degraded: the provider's own summary, the broken components,
		// and any open incidents — indented under the rollup line.
		if p.Description != "" {
			fmt.Fprintf(&b, "%s%s\n", detailIndent, p.Description)
		}
		for _, c := range broken {
			fmt.Fprintf(&b, "%s- %s: %s\n", detailIndent, c.Name, humanize(c.Status))
		}
		for _, inc := range p.Incidents {
			fmt.Fprintf(&b, "%s! %s (%s / %s)\n", detailIndent, inc.Name, humanize(inc.Status), humanize(inc.Impact))
		}
	}
	return b.String()
}

// isGreen reports whether the Statuspage indicator means "all good".
// Empty counts as green: a provider with no indicator has nothing to
// report.
func isGreen(indicator string) bool {
	return indicator == "none" || indicator == ""
}

// humanize turns a Statuspage snake_case enum (degraded_performance) into
// space-separated words for display.
func humanize(s string) string {
	return strings.ReplaceAll(s, "_", " ")
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
