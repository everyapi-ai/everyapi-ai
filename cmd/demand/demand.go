// Package demand wires `everyapi market demand …` — buyer-side
// marketplace postings ("I want model X under price ceiling Y").
package demand

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

func Run(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		cliout.Println(i18n.T("demand.usage"))
		if len(args) == 0 {
			return errors.New("missing subcommand")
		}
		return nil
	}
	switch args[0] {
	case "list":
		return runList(args[1:], false)
	case "my":
		return runList(args[1:], true)
	case "show":
		return runShow(args[1:])
	case "submit":
		return runSubmit(args[1:])
	case "cancel":
		return runCancel(args[1:])
	case "remove":
		return runRemove(args[1:])
	default:
		cliout.Println(i18n.T("demand.usage"))
		return fmt.Errorf("unknown 'demand' subcommand %q", args[0])
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
	return api.New(creds.APIBase, creds.AccessToken).WithUserID(creds.UserID), nil
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

func parseID(args []string) (int, []string, error) {
	if len(args) == 0 {
		return 0, nil, errors.New("missing <id>")
	}
	id, err := strconv.Atoi(args[0])
	if err != nil || id <= 0 {
		return 0, nil, fmt.Errorf("invalid id %q", args[0])
	}
	return id, args[1:], nil
}

func runList(args []string, mine bool) error {
	fs := flag.NewFlagSet("demand list", flag.ContinueOnError)
	state := fs.String("state", "", "filter (public feed only)")
	page := fs.Int("page", 0, "1-based page")
	limit := fs.Int("limit", 20, "page size")
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	var rows []api.Demand
	var total int
	if mine {
		rows, total, err = client.ListMyDemands(cliout.WithCtx(), *page, *limit)
	} else {
		rows, total, err = client.ListPublicDemands(cliout.WithCtx(), *state, *page, *limit)
	}
	if err != nil {
		return classifyErr(err)
	}
	if len(rows) == 0 {
		cliout.Println(i18n.T("demand.no_rows"))
		return nil
	}
	cliout.Printf("%d row(s) of %d total:\n", len(rows), total)
	for _, d := range rows {
		when := time.Unix(d.CreatedAt, 0).Format("2006-01-02")
		cliout.Printf("  [#%d] %s — model=%s  ceiling=%d quota/M tok  est=%d/mo  state=%s  posted %s\n",
			d.ID, d.Title, d.ModelName, d.MaxPricePerMTokenUSDQuota, d.MonthlyTokenEstimate, d.State, when)
	}
	return nil
}

func runShow(args []string) error {
	id, _, err := parseID(args)
	if err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	d, err := client.GetDemand(cliout.WithCtx(), id)
	if err != nil {
		return classifyErr(err)
	}
	cliout.Printf("Demand #%d  %q  (state=%s)\n", d.ID, d.Title, d.State)
	cliout.Printf("  model:         %s\n", d.ModelName)
	cliout.Printf("  price ceiling: %d quota per 1M tokens\n", d.MaxPricePerMTokenUSDQuota)
	cliout.Printf("  est volume:    %d tokens/month\n", d.MonthlyTokenEstimate)
	if d.RequireOAuth {
		cliout.Println("  require OAuth: yes")
	}
	if d.MinHealthBP > 0 {
		cliout.Printf("  min health:    %d bp\n", d.MinHealthBP)
	}
	if d.MaxLatencyMs > 0 {
		cliout.Printf("  max latency:   %d ms\n", d.MaxLatencyMs)
	}
	if d.TermDescription != "" {
		cliout.Printf("  term:          %s\n", d.TermDescription)
	}
	if d.Description != "" {
		cliout.Printf("  description:   %s\n", d.Description)
	}
	if d.ExpiresAt > 0 {
		cliout.Printf("  expires:       %s\n", time.Unix(d.ExpiresAt, 0).Format("2006-01-02 15:04:05"))
	}
	return nil
}

func runSubmit(args []string) error {
	fs := flag.NewFlagSet("demand submit", flag.ContinueOnError)
	title := fs.String("title", "", "short title (required)")
	model := fs.String("model", "", "model id (required)")
	maxPrice := fs.Float64("max-price", 0, "max USD per 1M tokens (required, > 0)")
	est := fs.Int64("est", 0, "monthly token estimate")
	desc := fs.String("description", "", "free-form description")
	term := fs.String("term", "", "commercial term blurb")
	requireOAuth := fs.Bool("require-oauth", false, "only accept OAuth-mounted upstreams")
	minHealthBP := fs.Int("min-health-bp", 0, "minimum upstream health (basis points)")
	maxLatencyMs := fs.Int("max-latency-ms", 0, "max acceptable upstream latency")
	expires := fs.Int64("expires", 0, "expires_at as Unix seconds (0 = never)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *title == "" || *model == "" || *maxPrice <= 0 {
		return errors.New("--title, --model, --max-price are required (--max-price > 0)")
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	d, err := client.SubmitDemand(cliout.WithCtx(), api.DemandSubmit{
		Title: *title, ModelName: *model,
		MaxPricePerMTokenUSD: *maxPrice,
		MonthlyTokenEstimate: *est,
		Description:          *desc,
		TermDescription:      *term,
		RequireOAuth:         *requireOAuth,
		MinHealthBP:          *minHealthBP,
		MaxLatencyMs:         *maxLatencyMs,
		ExpiresAt:            *expires,
	})
	if err != nil {
		return classifyErr(err)
	}
	cliout.Printf(i18n.T("demand.posted")+"\n", d.ID, d.State)
	return nil
}

func runCancel(args []string) error {
	id, _, err := parseID(args)
	if err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	if err := client.CancelDemand(cliout.WithCtx(), id); err != nil {
		return classifyErr(err)
	}
	cliout.Printf(i18n.T("demand.cancelled")+"\n", id)
	return nil
}

func runRemove(args []string) error {
	id, rest, err := parseID(args)
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("demand remove", flag.ContinueOnError)
	yes := fs.Bool("y", false, "skip confirmation")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if !*yes {
		if !cliprompt.IsInteractive() {
			// Destructive + no TTY to confirm on: fail closed rather than
			// silently removing. Require explicit -y for non-interactive use.
			return errors.New("refusing to remove without confirmation; pass -y to remove non-interactively")
		}
		ok, err := cliprompt.YesNo(
			bufio.NewReader(os.Stdin),
			fmt.Sprintf(i18n.T("demand.remove_confirm"), id),
			false,
		)
		if err != nil {
			return err
		}
		if !ok {
			cliout.Println(i18n.T("common.canceled"))
			return nil
		}
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	if err := client.DeleteDemand(cliout.WithCtx(), id); err != nil {
		return classifyErr(err)
	}
	cliout.Printf(i18n.T("demand.removed")+"\n", id)
	return nil
}
