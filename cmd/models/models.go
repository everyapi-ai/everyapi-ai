// Package models wires `everyapi models` — model catalog (which
// models my user can call, what they cost, what routing groups
// reach them). Three subs: list, pricing, groups.
package models

import (
	"errors"
	"flag"
	"fmt"
	"sort"
	"strings"

	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

const usage = `everyapi models — model catalog visible to your account

USAGE
  everyapi models <subcommand> [flags]

SUBCOMMANDS
  list                       Print every model id your group can route to
  pricing [--model <m>]      Per-model rate sheet (optionally filtered)
  groups                     Routing groups your account can use
`

func Run(args []string) error {
	if len(args) == 0 || args[0] == "list" {
		return runList(args[1:])
	}
	if args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		cliout.Println(usage)
		return nil
	}
	switch args[0] {
	case "pricing":
		return runPricing(args[1:])
	case "groups":
		return runGroups(args[1:])
	default:
		cliout.Println(usage)
		return fmt.Errorf("unknown 'models' subcommand %q", args[0])
	}
}

func newClient() (*api.Client, error) {
	creds, err := config.Load()
	if errors.Is(err, config.ErrNoCredentials) {
		return nil, errors.New("not logged in — run 'everyapi login' first")
	}
	if err != nil {
		return nil, err
	}
	return api.New(creds.APIBase, creds.AccessToken).WithUserID(creds.UserID), nil
}

func classifyErr(err error) error {
	if err == nil {
		return nil
	}
	if api.IsUnauthorized(err) {
		return errors.New("your session expired — run 'everyapi login' again")
	}
	return err
}

func runList(args []string) error {
	fs := flag.NewFlagSet("models list", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	ms, err := client.UserModels(cliout.WithCtx())
	if err != nil {
		return classifyErr(err)
	}
	if len(ms) == 0 {
		cliout.Println("No models routable from your group. Ask an admin to enable one.")
		return nil
	}
	sort.Strings(ms)
	cliout.Printf("%d model(s) accessible:\n", len(ms))
	for _, m := range ms {
		cliout.Printf("  %s\n", m)
	}
	return nil
}

func runPricing(args []string) error {
	fs := flag.NewFlagSet("models pricing", flag.ContinueOnError)
	modelFilter := fs.String("model", "", "filter to one model name (substring match)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	p, err := client.GetPricing(cliout.WithCtx())
	if err != nil {
		return classifyErr(err)
	}
	rows := p.Rows
	if *modelFilter != "" {
		filtered := rows[:0]
		for _, r := range rows {
			if strings.Contains(r.ModelName, *modelFilter) {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ModelName < rows[j].ModelName })
	if len(rows) == 0 {
		cliout.Println("No pricing rows match.")
		return nil
	}
	cliout.Printf("%d model(s):\n", len(rows))
	for _, r := range rows {
		switch r.QuotaType {
		case 1:
			cliout.Printf("  %-40s  fixed: %g per call (owner=%s)\n", r.ModelName, r.ModelPrice, fallback(r.OwnerBy, "-"))
		default:
			// quota_type 0 = per-token ratio
			cliout.Printf("  %-40s  ratio: prompt×%g  completion×%g  (owner=%s)\n",
				r.ModelName, r.ModelRatio, ratioOrOne(r.CompletionRatio), fallback(r.OwnerBy, "-"))
		}
	}
	if len(p.UsableGroup) > 0 {
		cliout.Println("\nYour group multipliers (effective when routing via --group):")
		groups := make([]string, 0, len(p.UsableGroup))
		for g := range p.UsableGroup {
			groups = append(groups, g)
		}
		sort.Strings(groups)
		for _, g := range groups {
			ratio, ok := p.GroupRatio[g]
			if !ok {
				cliout.Printf("  %-12s  (no explicit ratio)  %s\n", g, p.UsableGroup[g])
				continue
			}
			cliout.Printf("  %-12s  ×%g  %s\n", g, ratio, p.UsableGroup[g])
		}
	}
	return nil
}

func runGroups(args []string) error {
	fs := flag.NewFlagSet("models groups", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	groups, err := client.UserGroups(cliout.WithCtx())
	if err != nil {
		return classifyErr(err)
	}
	if len(groups) == 0 {
		cliout.Println("No routing groups available to your account.")
		return nil
	}
	names := make([]string, 0, len(groups))
	for n := range groups {
		names = append(names, n)
	}
	sort.Strings(names)
	cliout.Printf("%d routing group(s):\n", len(names))
	for _, n := range names {
		g := groups[n]
		ratio := fmt.Sprintf("%v", g.Ratio)
		cliout.Printf("  %-12s  ratio=%-6s  %s\n", n, ratio, g.Desc)
	}
	return nil
}

func ratioOrOne(r float64) float64 {
	// Pricing rows often leave completion_ratio at 0 to mean "same
	// as prompt"; render that as 1× so the output isn't a confusing
	// "completion×0".
	if r == 0 {
		return 1
	}
	return r
}

func fallback(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
