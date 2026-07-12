// Package subscription wires `everyapi account subscription …` — plans
// (read), self (read), preference (write). Online payment intentionally stays
// on `everyapi wallet topup`
// since card collection needs a browser.
package subscription

import (
	"errors"
	"flag"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/everyapi-ai/everyapi-ai/internal/cliargs"
	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

func Run(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		cliout.Println(i18n.T("subscription.usage"))
		if len(args) == 0 {
			return errors.New("missing subcommand (try 'everyapi account subscription help')")
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
		cliout.Println(i18n.T("subscription.usage"))
		return fmt.Errorf("unknown 'subscription' subcommand %q", args[0])
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

func runPlans(args []string) error {
	fs := flag.NewFlagSet("subscription plans", flag.ContinueOnError)
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
	plans, err := client.GetSubscriptionPlans(cliout.WithCtx())
	if err != nil {
		return classifyErr(err)
	}
	if len(plans) == 0 {
		cliout.Println(i18n.T("subscription.no_plans"))
		return nil
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].PriceAmount < plans[j].PriceAmount })
	cliout.Printf(i18n.T("subscription.plans_count")+"\n", len(plans))
	for _, p := range plans {
		dur := cliout.Sanitize(formatDuration(p.DurationUnit, p.DurationValue, p.CustomSeconds))
		extra := ""
		if p.UpgradeGroup != "" {
			extra = fmt.Sprintf("  upgrades to group=%s", cliout.Sanitize(p.UpgradeGroup))
		}
		cliout.Printf("  [#%d] %s — %.2f %s / %s%s\n", p.ID, cliout.Sanitize(p.Title), p.PriceAmount, cliout.Sanitize(p.Currency), dur, extra)
		if p.Subtitle != "" {
			cliout.Printf("        %s\n", cliout.Sanitize(p.Subtitle))
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
	if err := cliargs.RejectPositionals(fs); err != nil {
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
	cliout.Printf(i18n.T("subscription.self_billing_preference")+"\n\n", cliout.Sanitize(self.BillingPreference))
	rows := self.Subscriptions
	if *all {
		rows = self.AllSubscriptions
	}
	if len(rows) == 0 {
		if *all {
			cliout.Println(i18n.T("subscription.self_no_all"))
		} else {
			cliout.Println(i18n.T("subscription.self_no_active"))
		}
		return nil
	}
	if *all {
		cliout.Printf(i18n.T("subscription.self_count_all")+"\n", len(rows))
	} else {
		cliout.Printf(i18n.T("subscription.self_count_active")+"\n", len(rows))
	}
	for _, s := range rows {
		sub := s.Subscription
		expires := "never"
		if sub.EndTime > 0 {
			expires = time.Unix(sub.EndTime, 0).Format("2006-01-02")
		}
		started := "?"
		if sub.StartTime > 0 {
			started = time.Unix(sub.StartTime, 0).Format("2006-01-02")
		}
		cliout.Printf("  [#%d] plan=%d  source=%s  status=%s  %s → %s\n",
			sub.ID, sub.PlanID, cliout.Sanitize(sub.Source), cliout.Sanitize(sub.Status), started, expires)
	}
	return nil
}

func runPreference(args []string) error {
	fs := flag.NewFlagSet("subscription preference", flag.ContinueOnError)
	set := fs.String("set", "", "new billing preference: subscription_first / wallet_first / subscription_only / wallet_only")
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
	if *set == "" {
		// Bare `subscription preference` reads current value.
		self, err := client.GetSubscriptionSelf(cliout.WithCtx())
		if err != nil {
			return classifyErr(err)
		}
		cliout.Printf(i18n.T("subscription.current_preference")+"\n", cliout.Sanitize(self.BillingPreference))
		cliout.Println(i18n.T("subscription.set_hint"))
		return nil
	}
	if err := client.UpdateSubscriptionPreference(cliout.WithCtx(), *set); err != nil {
		return classifyErr(err)
	}
	// The backend silently normalises an unrecognised value to a default
	// instead of rejecting it, so echoing the raw input would falsely
	// confirm a typo as applied. Re-read and report the SERVER's stored
	// value, warning when it differs from what was requested.
	applied := strings.TrimSpace(*set)
	if self, rErr := client.GetSubscriptionSelf(cliout.WithCtx()); rErr == nil {
		applied = self.BillingPreference
	}
	cliout.Printf(i18n.T("subscription.preference_updated")+"\n", cliout.Sanitize(applied))
	if requested := strings.TrimSpace(*set); requested != applied {
		cliout.Printf(i18n.T("subscription.preference_normalized")+"\n", cliout.Sanitize(requested), cliout.Sanitize(applied))
	}
	return nil
}
