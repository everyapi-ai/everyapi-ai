// Package wallet wires `everyapi wallet …` — payment history,
// payment methods, and redemption-key application. The existing
// `everyapi topup` command (which opens the dashboard top-up page
// behind the §7-5 anti-phishing handshake) stays as-is for actual
// money-in flows; wallet covers everything that's safe to do over
// the API without a browser.
package wallet

import (
	"errors"
	"flag"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

const usage = `everyapi wallet — payment history, methods, redemption keys

USAGE
  everyapi wallet <subcommand> [flags]

SUBCOMMANDS
  history [--limit N] [--page P] [--keyword K]   Paginated payment history
  info                                            Enabled payment methods, min topup, options
  redeem <key>                                    Apply a topup / redemption key

NOTES
  For actual money-in (Stripe / Creem / Waffo / EPay), use
  'everyapi topup' — it opens the dashboard with the anti-phishing
  verification phrase. Browser handoff is required there because
  card collection happens out-of-band.
`

func Run(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		cliout.Println(usage)
		if len(args) == 0 {
			return errors.New("missing subcommand (try 'everyapi wallet help')")
		}
		return nil
	}
	switch args[0] {
	case "history":
		return runHistory(args[1:])
	case "info":
		return runInfo(args[1:])
	case "redeem":
		return runRedeem(args[1:])
	default:
		cliout.Println(usage)
		return fmt.Errorf("unknown 'wallet' subcommand %q", args[0])
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

func runHistory(args []string) error {
	fs := flag.NewFlagSet("wallet history", flag.ContinueOnError)
	limit := fs.Int("limit", 20, "page size")
	page := fs.Int("page", 0, "1-based page index")
	keyword := fs.String("keyword", "", "substring filter (trade no / notes)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	rows, total, err := client.ListUserTopups(cliout.WithCtx(), *page, *limit, *keyword)
	if err != nil {
		return classifyErr(err)
	}
	if len(rows) == 0 {
		cliout.Println("No topups recorded.")
		return nil
	}
	cliout.Printf("%d row(s) of %d total:\n", len(rows), total)
	for _, t := range rows {
		when := time.Unix(t.CreateTime, 0).Format("2006-01-02 15:04:05")
		method := t.PaymentMethod
		if t.PaymentProvider != "" && t.PaymentProvider != t.PaymentMethod {
			method = fmt.Sprintf("%s/%s", t.PaymentMethod, t.PaymentProvider)
		}
		cliout.Printf("  %s  [#%d]  %s  amount=%d  money=%g  status=%s  trade=%s\n",
			when, t.ID, method, t.Amount, t.Money, t.Status, t.TradeNo)
	}
	return nil
}

func runInfo(args []string) error {
	fs := flag.NewFlagSet("wallet info", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	info, err := client.GetTopupInfo(cliout.WithCtx())
	if err != nil {
		return classifyErr(err)
	}
	cliout.Println("Payment methods:")
	if len(info.PayMethods) == 0 {
		cliout.Println("  (none enabled — admin needs to configure at least one)")
	}
	for _, pm := range info.PayMethods {
		cliout.Printf("  - %s (type=%s, min=%s)\n", pm["name"], pm["type"], pm["min_topup"])
	}
	cliout.Println("\nFeature flags:")
	cliout.Printf("  online (epay): %v\n", info.EnableOnlineTopup)
	cliout.Printf("  stripe:        %v (min=%d)\n", info.EnableStripeTopup, info.StripeMinTopup)
	cliout.Printf("  creem:         %v\n", info.EnableCreemTopup)
	cliout.Printf("  waffo:         %v (min=%d)\n", info.EnableWaffoTopup, info.WaffoMinTopup)
	cliout.Printf("  waffo-pancake: %v (min=%d)\n", info.EnableWaffoPancakeTopup, info.WaffoPancakeMinTopup)
	if info.MinTopup > 0 {
		cliout.Printf("\nOverall minimum top-up: %d\n", info.MinTopup)
	}
	if len(info.AmountOptions) > 0 {
		labels := make([]string, len(info.AmountOptions))
		for i, a := range info.AmountOptions {
			labels[i] = fmt.Sprintf("%g", a)
		}
		cliout.Printf("\nSuggested amounts: %s\n", strings.Join(labels, ", "))
	}
	if len(info.Discount) > 0 {
		keys := make([]string, 0, len(info.Discount))
		for k := range info.Discount {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		cliout.Println("\nDiscount tiers:")
		for _, k := range keys {
			cliout.Printf("  ≥%s → ×%g\n", k, info.Discount[k])
		}
	}
	if info.TopupLink != "" {
		cliout.Printf("\nDashboard top-up: %s\n", info.TopupLink)
	}
	cliout.Println("\nFor card / e-wallet payments use 'everyapi topup' (browser flow).")
	return nil
}

func runRedeem(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi wallet redeem <key>")
	}
	key := args[0]
	rest := args[1:]
	fs := flag.NewFlagSet("wallet redeem", flag.ContinueOnError)
	if err := fs.Parse(rest); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	quota, err := client.Redeem(cliout.WithCtx(), key)
	if err != nil {
		return classifyErr(err)
	}
	cliout.Printf("Redeemed: +%d quota credited.\n", quota)
	return nil
}
