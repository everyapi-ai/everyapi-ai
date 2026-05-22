// Package dispute wires `everyapi dispute …` — open / list /
// inspect marketplace disputes.
package dispute

import (
	"errors"
	"flag"
	"fmt"
	"strconv"
	"time"

	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

const usage = `everyapi dispute — open / list / inspect disputes

USAGE
  everyapi dispute <subcommand> [flags]

SUBCOMMANDS
  submit --counterparty <uid> --type <t> --target-kind <k> --target <id> --description <d> [--amount <quota>]
                                       Open a dispute
  my     [--page P] [--limit N]        List your open + resolved disputes
  show   <id>                          Single dispute in detail
`

func Run(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		cliout.Println(usage)
		if len(args) == 0 {
			return errors.New("missing subcommand")
		}
		return nil
	}
	switch args[0] {
	case "submit":
		return runSubmit(args[1:])
	case "my":
		return runList(args[1:])
	case "show":
		return runShow(args[1:])
	default:
		cliout.Println(usage)
		return fmt.Errorf("unknown 'dispute' subcommand %q", args[0])
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

func runSubmit(args []string) error {
	fs := flag.NewFlagSet("dispute submit", flag.ContinueOnError)
	counter := fs.Int("counterparty", 0, "counterparty user id")
	typ := fs.String("type", "", "dispute type (see admin docs for valid values)")
	kind := fs.String("target-kind", "", "what the dispute targets (channel / order / etc.)")
	tgt := fs.String("target", "", "target id (string)")
	amount := fs.Int64("amount", 0, "claim amount in quota units (optional)")
	desc := fs.String("description", "", "detailed description")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *typ == "" || *kind == "" || *tgt == "" || *desc == "" {
		return errors.New("--type, --target-kind, --target, --description are required")
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	d, err := client.SubmitDispute(cliout.WithCtx(), api.DisputeSubmit{
		CounterpartyUserID: *counter,
		Type:               *typ,
		TargetKind:         *kind,
		TargetID:           *tgt,
		AmountQuota:        *amount,
		Description:        *desc,
	})
	if err != nil {
		return classifyErr(err)
	}
	cliout.Printf("Dispute #%d opened (state=%s).\n", d.ID, d.State)
	return nil
}

func runList(args []string) error {
	fs := flag.NewFlagSet("dispute my", flag.ContinueOnError)
	page := fs.Int("page", 0, "1-based page")
	limit := fs.Int("limit", 20, "page size")
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	rows, total, err := client.ListMyDisputes(cliout.WithCtx(), *page, *limit)
	if err != nil {
		return classifyErr(err)
	}
	if len(rows) == 0 {
		cliout.Println("No disputes.")
		return nil
	}
	cliout.Printf("%d row(s) of %d total:\n", len(rows), total)
	for _, d := range rows {
		when := time.Unix(d.OpenedAt, 0).Format("2006-01-02")
		side := "opener"
		if d.CounterpartyUserID != 0 {
			side = fmt.Sprintf("vs uid=%d", d.CounterpartyUserID)
		}
		cliout.Printf("  [#%d] %s/%s  state=%s  amount=%d  %s  opened %s\n",
			d.ID, d.Type, d.TargetKind, d.State, d.AmountQuota, side, when)
	}
	return nil
}

func runShow(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi dispute show <id>")
	}
	id, err := strconv.Atoi(args[0])
	if err != nil || id <= 0 {
		return fmt.Errorf("invalid id %q", args[0])
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	d, err := client.GetDispute(cliout.WithCtx(), id)
	if err != nil {
		return classifyErr(err)
	}
	cliout.Printf("Dispute #%d  state=%s  type=%s/%s\n", d.ID, d.State, d.Type, d.TargetKind)
	cliout.Printf("  opener:        uid=%d\n", d.OpenerUserID)
	if d.CounterpartyUserID != 0 {
		cliout.Printf("  counterparty:  uid=%d\n", d.CounterpartyUserID)
	}
	cliout.Printf("  target id:     %s\n", d.TargetID)
	cliout.Printf("  amount:        %d quota\n", d.AmountQuota)
	cliout.Printf("  opened:        %s\n", time.Unix(d.OpenedAt, 0).Format("2006-01-02 15:04:05"))
	if d.UpdatedAt > 0 {
		cliout.Printf("  updated:       %s\n", time.Unix(d.UpdatedAt, 0).Format("2006-01-02 15:04:05"))
	}
	if d.ResolvedAt > 0 {
		cliout.Printf("  resolved:      %s\n", time.Unix(d.ResolvedAt, 0).Format("2006-01-02 15:04:05"))
	}
	if d.Description != "" {
		cliout.Printf("  description:\n    %s\n", d.Description)
	}
	return nil
}
