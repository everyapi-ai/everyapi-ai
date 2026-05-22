// Package report wires `everyapi report` — submit an abuse /
// TOS-violation report. Public endpoint, so this works without
// being logged in.
package report

import (
	"errors"
	"flag"

	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

const usage = `everyapi report — file an abuse / TOS-violation report

USAGE
  everyapi report --email <e> --category <c> --target-type <t> --description <d>
                  [--target-id <id>] [--evidence <url>]

NOTES
  Public endpoint — works without being logged in. When called while
  logged in, the reporter user_id is captured server-side for triage.
`

func Run(args []string) error {
	if len(args) > 0 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h") {
		cliout.Println(usage)
		return nil
	}
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	email := fs.String("email", "", "reporter email (required)")
	cat := fs.String("category", "", "abuse category (see TOS)")
	tgtType := fs.String("target-type", "", "what you're reporting")
	tgtID := fs.String("target-id", "", "target identifier")
	desc := fs.String("description", "", "detailed description (required)")
	evid := fs.String("evidence", "", "evidence URL (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *email == "" || *cat == "" || *tgtType == "" || *desc == "" {
		return errors.New("--email, --category, --target-type, --description are required")
	}
	// Build a client. The abuse endpoint is TryUserAuth — works
	// without credentials, but if we have them we'll send them so
	// the backend can capture our user_id.
	apiBase := "https://api.everyapi.ai"
	token := ""
	userID := 0
	if creds, err := config.Load(); err == nil && creds != nil {
		apiBase = creds.APIBase
		token = creds.AccessToken
		userID = creds.UserID
	}
	client := api.New(apiBase, token).WithUserID(userID)
	if err := client.SubmitAbuseReport(cliout.WithCtx(), api.AbuseReportSubmit{
		ReporterEmail: *email, Category: *cat, TargetType: *tgtType,
		TargetID: *tgtID, Description: *desc, EvidenceURL: *evid,
	}); err != nil {
		return err
	}
	cliout.Println("Report filed. The abuse desk will follow up via the email you provided.")
	return nil
}
