// Package dispute wires `everyapi market dispute …` — open / list /
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
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

func Run(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		cliout.Println(i18n.T("dispute.usage"))
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
		cliout.Println(i18n.T("dispute.usage"))
		return fmt.Errorf("unknown 'dispute' subcommand %q", args[0])
	}
}

func newClient() (*api.Client, *config.Credentials, error) {
	creds, err := config.Load()
	if errors.Is(err, config.ErrNoCredentials) {
		return nil, nil, errors.New(i18n.T("auth.not_logged_in"))
	}
	if err != nil {
		return nil, nil, err
	}
	return api.New(creds.APIBase, creds.AccessToken).WithUserID(creds.UserID), creds, nil
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
	client, _, err := newClient()
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
	cliout.Printf(i18n.T("dispute.opened")+"\n", d.ID, d.State)
	return nil
}

func runList(args []string) error {
	fs := flag.NewFlagSet("dispute my", flag.ContinueOnError)
	page := fs.Int("page", 0, "1-based page")
	limit := fs.Int("limit", 20, "page size")
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, creds, err := newClient()
	if err != nil {
		return err
	}
	rows, total, err := client.ListMyDisputes(cliout.WithCtx(), *page, *limit)
	if err != nil {
		return classifyErr(err)
	}
	if len(rows) == 0 {
		cliout.Println(i18n.T("dispute.no_rows"))
		return nil
	}
	me := creds.UserID
	cliout.Printf(i18n.T("common.rows_of_total")+"\n", len(rows), total)
	for _, d := range rows {
		when := time.Unix(d.OpenedAt, 0).Format("2006-01-02")
		// Describe the OTHER party relative to me, and which side I'm on.
		// The list mixes disputes I opened with disputes filed against me;
		// the old code always printed "vs uid=<counterparty>", which on a
		// dispute filed against me is my OWN id.
		var side string
		switch {
		case me == 0:
			// creds.UserID is unset (e.g. an OAuth2 login that never recorded
			// it, or pre-UserID credentials.json) — "me" matches nothing, so
			// show both parties neutrally instead of guessing a direction that
			// would mislabel the user's own disputes as filed against them.
			if d.CounterpartyUserID != 0 {
				side = fmt.Sprintf("opener uid=%d vs uid=%d", d.OpenerUserID, d.CounterpartyUserID)
			} else {
				side = fmt.Sprintf("opener uid=%d", d.OpenerUserID)
			}
		case d.OpenerUserID == me:
			if d.CounterpartyUserID != 0 {
				side = fmt.Sprintf("you → uid=%d", d.CounterpartyUserID)
			} else {
				side = "opened by you"
			}
		default:
			side = fmt.Sprintf("uid=%d → you", d.OpenerUserID)
		}
		cliout.Printf("  [#%d] %s/%s  state=%s  amount=%d  %s  opened %s\n",
			d.ID, d.Type, d.TargetKind, d.State, d.AmountQuota, side, when)
	}
	return nil
}

func runShow(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi market dispute show <id>")
	}
	id, err := strconv.Atoi(args[0])
	if err != nil || id <= 0 {
		return fmt.Errorf("invalid id %q", args[0])
	}
	client, _, err := newClient()
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
	cliout.Printf("  target id:     %s\n", cliout.Sanitize(d.TargetID))
	cliout.Printf("  amount:        %d quota\n", d.AmountQuota)
	cliout.Printf("  opened:        %s\n", time.Unix(d.OpenedAt, 0).Format("2006-01-02 15:04:05"))
	if d.UpdatedAt > 0 {
		cliout.Printf("  updated:       %s\n", time.Unix(d.UpdatedAt, 0).Format("2006-01-02 15:04:05"))
	}
	if d.ResolvedAt > 0 {
		cliout.Printf("  resolved:      %s\n", time.Unix(d.ResolvedAt, 0).Format("2006-01-02 15:04:05"))
	}
	if d.Description != "" {
		cliout.Printf("  description:\n    %s\n", cliout.Sanitize(d.Description))
	}
	return nil
}
