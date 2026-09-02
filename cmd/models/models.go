// Package models wires `everyapi models` — model catalog (which models my user can call, what they cost, what routing groups reach them). Three subs: list, pricing, groups.
package models

import (
	"errors"
	"flag"
	"fmt"
	"sort"
	"strings"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliargs"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

func Run(args []string) error {
	if len(args) == 0 {
		return runList(nil)
	}
	if args[0] == "list" {
		return runList(args[1:])
	}
	if args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		cliout.Println(i18n.T("models.usage"))
		return nil
	}
	switch args[0] {
	case "pricing":
		return runPricing(args[1:])
	case "groups":
		return runGroups(args[1:])
	default:
		cliout.Println(i18n.T("models.usage"))
		return fmt.Errorf("unknown 'models' subcommand %q", args[0])
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

func runList(args []string) error {
	fs := flag.NewFlagSet("models list", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := cliargs.RejectPositionals(fs); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	directory, err := loadModelDirectory(client)
	if err != nil {
		return classifyErr(err)
	}
	ms := directory.Models
	if directory.PromotionalOnly {
		cliout.Printf(i18n.T("models.promotional_only")+"\n", cliout.Sanitize(requiredPromotionalGroup(directory.RequiredGroup)))
	}
	if len(ms) == 0 {
		cliout.Println(i18n.T("models.no_models"))
		return nil
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].ID < ms[j].ID })
	cliout.Printf(i18n.T("models.count")+"\n", len(ms))
	for _, m := range ms {
		if m.Vendor != "" {
			cliout.Printf("  %-40s  %s\n", cliout.Sanitize(m.ID), cliout.Sanitize(m.Vendor))
		} else {
			cliout.Printf("  %s\n", cliout.Sanitize(m.ID))
		}
	}
	return nil
}

func runPricing(args []string) error {
	fs := flag.NewFlagSet("models pricing", flag.ContinueOnError)
	modelFilter := fs.String("model", "", "filter to one model name (substring match)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := cliargs.RejectPositionals(fs); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	directory, err := loadModelDirectory(client)
	if err != nil {
		return classifyErr(err)
	}
	p, err := client.GetPricing(cliout.WithCtx())
	if err != nil {
		return classifyErr(err)
	}
	rows := p.Rows
	if directory.PromotionalOnly {
		allowedModels := make(map[string]struct{}, len(directory.Models))
		for _, model := range directory.Models {
			allowedModels[model.ID] = struct{}{}
		}
		filtered := rows[:0]
		for _, row := range rows {
			if _, allowed := allowedModels[row.ModelName]; allowed {
				filtered = append(filtered, row)
			}
		}
		rows = filtered
		requiredGroup := requiredPromotionalGroup(directory.RequiredGroup)
		for group := range p.UsableGroup {
			if group != requiredGroup {
				delete(p.UsableGroup, group)
			}
		}
		for group := range p.GroupRatio {
			if group != requiredGroup {
				delete(p.GroupRatio, group)
			}
		}
		cliout.Printf(i18n.T("models.promotional_only")+"\n", cliout.Sanitize(requiredGroup))
	}
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
		cliout.Println(i18n.T("models.no_pricing"))
		return nil
	}
	cliout.Printf("%d model(s):\n", len(rows))
	for _, r := range rows {
		switch r.QuotaType {
		case 1:
			cliout.Printf("  %-40s  fixed: %g per call (owner=%s)\n", cliout.Sanitize(r.ModelName), r.ModelPrice, fallback(cliout.Sanitize(r.OwnerBy), "-"))
		default:
			// quota_type 0 = per-token ratio
			cliout.Printf("  %-40s  ratio: prompt×%g  completion×%g  (owner=%s)\n",
				cliout.Sanitize(r.ModelName), r.ModelRatio, ratioOrOne(r.CompletionRatio), fallback(cliout.Sanitize(r.OwnerBy), "-"))
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
			id := cliout.Sanitize(g)
			// A group whose name carries no text in this language and no English
			// fallback would otherwise print an empty first column; the id is the
			// one thing the caller can actually pass to --group. Sanitize BEFORE
			// the fallback (same order as the owner column above): a name built
			// only from escape sequences or control bytes passes the emptiness
			// test and then sanitizes down to nothing, which is the blank column
			// again. `id` is already sanitized.
			name := fallback(cliout.Sanitize(p.UsableGroup[g].Resolve(i18n.Language())), id)
			if !ok {
				cliout.Printf("  %-24s  id=%-12s  (no explicit ratio)\n", name, id)
				continue
			}
			cliout.Printf("  %-24s  id=%-12s  ×%g\n", name, id, ratio)
		}
	}
	return nil
}

func runGroups(args []string) error {
	fs := flag.NewFlagSet("models groups", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := cliargs.RejectPositionals(fs); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	directory, err := loadModelDirectory(client)
	if err != nil {
		return classifyErr(err)
	}
	// UserGroups is the anonymous mount, so the usable column below describes the anonymous tier rather than this account — a real bug, but not one to fix by switching to SelfGroups here: that mount is behind UserAuth, and an OAuth2 relay-key login carries no user id, so every such install would get a 401 rendered as "session expired, log in again" — which re-login cannot clear. Fixing it properly means falling back to this mount for credentials that cannot reach the authenticated one.
	groups, err := client.UserGroups(cliout.WithCtx())
	if err != nil {
		return classifyErr(err)
	}
	if directory.PromotionalOnly {
		requiredGroup := requiredPromotionalGroup(directory.RequiredGroup)
		for id := range groups {
			if id != requiredGroup {
				delete(groups, id)
			}
		}
		cliout.Printf(i18n.T("models.promotional_only")+"\n", cliout.Sanitize(requiredGroup))
	}
	if len(groups) == 0 {
		cliout.Println(i18n.T("models.no_groups"))
		return nil
	}
	ids := make([]string, 0, len(groups))
	for id := range groups {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		left, right := groups[ids[i]].Name, groups[ids[j]].Name
		if left == right {
			return ids[i] < ids[j]
		}
		return left < right
	})
	cliout.Printf("%d routing group(s):\n", len(ids))
	for _, id := range ids {
		g := groups[id]
		// g.Ratio is `any` (the backend may send a number or a string), so the formatted value is attacker-influenced text — sanitize it like every other server-sourced field printed here.
		ratio := cliout.Sanitize(fmt.Sprintf("%v", g.Ratio))
		availability := "usable"
		if !g.Usable {
			availability = "unavailable"
		}
		cliout.Printf("  %-24s  id=%-12s  ratio=%-6s  %s\n",
			cliout.Sanitize(g.Name), cliout.Sanitize(id), ratio, availability)
	}
	return nil
}

func requiredPromotionalGroup(group string) string {
	if group == "" {
		return "auto"
	}
	return group
}

func loadModelDirectory(client *api.Client) (*api.UserModelDirectory, error) {
	directory, err := client.GetUserModelDirectory(cliout.WithCtx())
	if err == nil {
		return directory, nil
	}
	if !api.IsUnauthorized(err) {
		return nil, err
	}
	creds, loadErr := config.Load()
	if loadErr != nil {
		return nil, loadErr
	}
	if creds.UserID != 0 {
		return nil, err
	}
	if creds.OAuthClientID == "" || creds.RelayKey == "" {
		return &api.UserModelDirectory{}, nil
	}
	relayDirectory, relayErr := api.New(config.ResolveAPIBaseForBase(creds.APIBase), creds.RelayKey).GetRelayModelDirectory(cliout.WithCtx())
	if relayErr != nil {
		return nil, relayErr
	}
	models := make([]api.UserModel, 0, len(relayDirectory.Models))
	for _, model := range relayDirectory.Models {
		models = append(models, api.UserModel{ID: model.ID, Vendor: model.OwnedBy})
	}
	return &api.UserModelDirectory{Models: models, PromotionalOnly: relayDirectory.PromotionalOnly, RequiredGroup: relayDirectory.RequiredGroup}, nil
}

func ratioOrOne(r float64) float64 {
	// Pricing rows often leave completion_ratio at 0 to mean "same as prompt"; render that as 1× so the output isn't a confusing "completion×0".
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
