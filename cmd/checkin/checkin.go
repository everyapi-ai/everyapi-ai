// Package checkin wires `everyapi checkin` — daily check-in for
// quota grants. Two shapes:
//
//	everyapi checkin                claim today's reward
//	everyapi checkin status [...]   monthly history
package checkin

import (
	"errors"
	"flag"

	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

const usage = `everyapi checkin — daily check-in for quota grants

USAGE
  everyapi checkin                       Claim today's reward
  everyapi checkin status [--month YYYY-MM]
                                         Show the calendar of past check-ins
`

func Run(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "help", "--help", "-h":
			cliout.Println(usage)
			return nil
		case "status":
			return runStatus(args[1:])
		}
	}
	return runCheckin()
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

func runCheckin() error {
	client, err := newClient()
	if err != nil {
		return err
	}
	res, err := client.DoCheckin(cliout.WithCtx())
	if err != nil {
		return classifyErr(err)
	}
	cliout.Printf("Check-in successful: +%d quota awarded (date %s).\n", res.QuotaAwarded, res.CheckinDate)
	return nil
}

func runStatus(args []string) error {
	fs := flag.NewFlagSet("checkin status", flag.ContinueOnError)
	month := fs.String("month", "", "YYYY-MM (default: current month)")
	if err := fs.Parse(args); err != nil {
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
		cliout.Println("Check-in feature is disabled on this instance.")
		return nil
	}
	cliout.Printf("Reward range: %d – %d quota per day\n", stat.MinQuota, stat.MaxQuota)
	cliout.Printf("Lifetime: %d check-in(s) for %d quota total.\n", stat.Stats.TotalCheckins, stat.Stats.TotalQuota)
	if stat.Stats.CheckedInToday {
		cliout.Println("Today: already claimed.")
	} else {
		cliout.Println("Today: not yet claimed — run 'everyapi checkin' to grab it.")
	}
	if len(stat.Stats.Records) == 0 {
		cliout.Println("No check-ins in this month yet.")
		return nil
	}
	cliout.Printf("\n%d day(s) this month:\n", stat.Stats.CheckinCount)
	for _, r := range stat.Stats.Records {
		cliout.Printf("  %s  +%d quota\n", r.CheckinDate, r.QuotaAwarded)
	}
	return nil
}

