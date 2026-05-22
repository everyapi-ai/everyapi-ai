// Package subscription wires `everyapi subscription …` — plans
// (read), self (read), preference (write). Pay flows
// (Stripe / Creem / EPay) intentionally stay on `everyapi topup`
// since card collection needs a browser.
package subscription

import (
	"errors"
	"flag"
	"fmt"
	"sort"
	"time"

	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

const usage = `everyapi subscription — subscription plans / self / preference

USAGE
  everyapi subscription <subcommand> [flags]

SUBCOMMANDS
  plans                            List enabled subscription plans
  self                             Show your active + past subscriptions
  preference --set <value>         Update billing preference
                                   (commonly: "topup" / "subscription" / "subscription_first")
                                   Backend normalises unknown values.

NOTE
  Buying a plan is still a browser flow — use 'everyapi topup' to
  open the dashboard wallet behind the anti-phishing handshake.
`

func Run(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		cliout.Println(usage)
		if len(args) == 0 {
			return errors.New("missing subcommand (try 'everyapi subscription help')")
		}
		return nil
	}
	switch args[0] {
	case "plans":
		return runPlans(args[1:])
	case "self":
		return runSelf(args[1:])
	case "preference":
		return runPreference(args[1:])
	default:
		cliout.Println(usage)
		return fmt.Errorf("unknown 'subscription' subcommand %q", args[0])
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

func runPlans(args []string) error {
	fs := flag.NewFlagSet("subscription plans", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	plans, err := client.GetSubscriptionPlans(cliout.WithCtx())
	if err != nil {
		return classifyErr(err)
	}
	if len(plans) == 0 {
		cliout.Println("No enabled subscription plans.")
		return nil
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].PriceAmount < plans[j].PriceAmount })
	cliout.Printf("%d plan(s):\n", len(plans))
	for _, p := range plans {
		dur := formatDuration(p.DurationUnit, p.DurationValue, p.CustomSeconds)
		extra := ""
		if p.UpgradeGroup != "" {
			extra = fmt.Sprintf("  upgrades to group=%s", p.UpgradeGroup)
		}
		cliout.Printf("  [#%d] %s — %g %s / %s%s\n", p.ID, p.Title, p.PriceAmount, p.Currency, dur, extra)
		if p.Subtitle != "" {
			cliout.Printf("        %s\n", p.Subtitle)
		}
	}
	return nil
}

func formatDuration(unit string, value int, custom int64) string {
	switch unit {
	case "custom":
		return fmt.Sprintf("%ds", custom)
	case "":
		return "1 month"
	default:
		if value <= 1 {
			return unit
		}
		return fmt.Sprintf("%d %ss", value, unit)
	}
}

func runSelf(args []string) error {
	fs := flag.NewFlagSet("subscription self", flag.ContinueOnError)
	all := fs.Bool("all", false, "include expired / cancelled subscriptions")
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	self, err := client.GetSubscriptionSelf(cliout.WithCtx())
	if err != nil {
		return classifyErr(err)
	}
	cliout.Printf("Billing preference: %s\n\n", self.BillingPreference)
	rows := self.Subscriptions
	label := "active"
	if *all {
		rows = self.AllSubscriptions
		label = "all"
	}
	if len(rows) == 0 {
		cliout.Printf("No %s subscriptions.\n", label)
		return nil
	}
	cliout.Printf("%d %s subscription(s):\n", len(rows), label)
	for _, s := range rows {
		expires := "never"
		if s.ExpiresAt > 0 {
			expires = time.Unix(s.ExpiresAt, 0).Format("2006-01-02")
		}
		started := "?"
		if s.StartAt > 0 {
			started = time.Unix(s.StartAt, 0).Format("2006-01-02")
		}
		cliout.Printf("  [#%d] %s  source=%s  status=%s  %s → %s\n",
			s.ID, s.PlanTitle, s.Source, s.Status, started, expires)
	}
	return nil
}

func runPreference(args []string) error {
	fs := flag.NewFlagSet("subscription preference", flag.ContinueOnError)
	set := fs.String("set", "", "new billing preference (e.g. topup / subscription / subscription_first)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	if *set == "" {
		// Bare `subscription preference` reads current value.
		self, err := client.GetSubscriptionSelf(cliout.WithCtx())
		if err != nil {
			return classifyErr(err)
		}
		cliout.Printf("Current billing preference: %s\n", self.BillingPreference)
		cliout.Println("Use --set <value> to change. Backend normalises unknown values to the default.")
		return nil
	}
	if err := client.UpdateSubscriptionPreference(cliout.WithCtx(), *set); err != nil {
		return classifyErr(err)
	}
	cliout.Printf("Billing preference updated to %q.\n", *set)
	return nil
}
