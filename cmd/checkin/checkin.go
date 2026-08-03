// Package checkin wires `everyapi checkin` — daily check-in for
// quota grants. Three shapes:
//
//	everyapi checkin                claim today's reward
//	everyapi checkin status [...]   monthly history
//	everyapi checkin makeup <date>  cover a missed day (no reward)
package checkin

import (
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/everyapi-ai/everyapi-ai/internal/cliargs"
	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

func Run(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "help", "--help", "-h":
			cliout.Println(i18n.T("checkin.usage"))
			return nil
		case "status":
			return runStatus(args[1:])
		case "makeup":
			return runMakeup(args[1:])
		case "claim":
			// Explicit alias for bare `everyapi checkin`. The bare
			// form predates the explicit verb but the picker only
			// shows declared subs, so a user landing on the picker
			// would think `status` was all the command did. Adding
			// `claim` surfaces the claim action in the picker too;
			// bare-form invocation stays valid.
			if len(args) > 1 && (args[1] == "help" || args[1] == "--help" || args[1] == "-h") {
				cliout.Println(i18n.T("checkin.usage"))
				return nil
			}
			return runCheckin()
		default:
			// An unknown subcommand must NOT silently fall through to a
			// state-changing claim (e.g. `everyapi checkin staus` would
			// quietly burn today's check-in). Surface it like the other
			// subcommands do.
			cliout.Println(i18n.T("checkin.usage"))
			return fmt.Errorf(i18n.T("common.unknown_subcommand"), "checkin", args[0])
		}
	}
	return runCheckin()
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

func runCheckin() error {
	client, err := newClient()
	if err != nil {
		return err
	}
	res, err := client.DoCheckin(cliout.WithCtx())
	if err != nil {
		return classifyErr(err)
	}
	cliout.Printf(i18n.T("checkin.success")+"\n", res.QuotaAwarded, res.CheckinDate)
	return nil
}

func runStatus(args []string) error {
	fs := flag.NewFlagSet("checkin status", flag.ContinueOnError)
	month := fs.String("month", "", "YYYY-MM (default: current month)")
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
	stat, err := client.GetCheckinStatus(cliout.WithCtx(), *month)
	if err != nil {
		return classifyErr(err)
	}
	if !stat.Enabled {
		cliout.Println(i18n.T("checkin.disabled"))
		return nil
	}
	cliout.Printf(i18n.T("checkin.reward_range")+"\n", stat.MinQuota, stat.MaxQuota)
	cliout.Printf(i18n.T("checkin.lifetime")+"\n", stat.Stats.TotalCheckins, stat.Stats.TotalQuota)
	if stat.Stats.CheckedInToday {
		cliout.Println(i18n.T("checkin.today_claimed"))
	} else {
		cliout.Println(i18n.T("checkin.today_unclaimed"))
	}
	if stat.Makeup.Enabled && len(stat.Makeup.EligibleDates) > 0 {
		cliout.Printf(i18n.T("checkin.makeup_available")+"\n",
			strings.Join(stat.Makeup.EligibleDates, ", "))
	}
	if len(stat.Stats.Records) == 0 {
		cliout.Println(i18n.T("checkin.no_records"))
		return nil
	}
	cliout.Printf(i18n.T("checkin.month_days")+"\n", stat.Stats.CheckinCount)
	for _, r := range stat.Stats.Records {
		if r.IsMakeup {
			// A made-up day pays nothing; printing "+0 quota" would read as a
			// broken reward rather than as the deliberate trade-off it is.
			cliout.Printf("  %s  %s\n", r.CheckinDate, i18n.T("checkin.makeup_marker"))
			continue
		}
		cliout.Printf("  %s  +%d quota\n", r.CheckinDate, r.QuotaAwarded)
	}
	return nil
}

func runMakeup(args []string) error {
	fs := flag.NewFlagSet("checkin makeup", flag.ContinueOnError)
	date := fs.String("date", "", "YYYY-MM-DD (UTC) day to cover")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// Accept the date as a bare positional too — `everyapi checkin makeup
	// 2026-08-01` is the shape a user reaches for first.
	if *date == "" && fs.NArg() == 1 {
		*date = fs.Arg(0)
	} else if err := cliargs.RejectPositionals(fs); err != nil {
		return err
	}
	if *date == "" {
		cliout.Println(i18n.T("checkin.usage"))
		return errors.New(i18n.T("checkin.makeup_date_required"))
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	res, err := client.DoCheckinMakeup(cliout.WithCtx(), *date)
	if err != nil {
		return classifyErr(err)
	}
	cliout.Printf(i18n.T("checkin.makeup_success")+"\n", res.CheckinDate)
	return nil
}
