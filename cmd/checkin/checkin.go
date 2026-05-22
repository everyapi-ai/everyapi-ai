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
	if len(stat.Stats) == 0 {
		cliout.Println("No check-ins in this month yet — run 'everyapi checkin' to claim today's.")
		return nil
	}
	cliout.Printf("%d day(s) this month:\n", len(stat.Stats))
	for _, row := range stat.Stats {
		// The shape varies by deploy; surface the date and quota
		// fields when present, dump the whole row otherwise. Future
		// schema additions (streak, multiplier) print verbatim.
		date, hasDate := row["checkin_date"].(string)
		quota, hasQuota := row["quota_awarded"].(float64) // JSON numbers decode to float64
		if hasDate && hasQuota {
			cliout.Printf("  %s  +%d quota\n", date, int(quota))
		} else {
			cliout.Printf("  %v\n", row)
		}
	}
	return nil
}

