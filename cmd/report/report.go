// Package report wires `everyapi market report` — submit an abuse /
// TOS-violation report. Public endpoint, so this works without
// being logged in.
package report

import (
	"errors"
	"flag"

	"github.com/everyapi-ai/everyapi-ai/internal/cliargs"
	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

func Run(args []string) error {
	if len(args) > 0 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h") {
		cliout.Println(i18n.T("report.usage"))
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
	if err := cliargs.RejectPositionals(fs); err != nil {
		return err
	}
	if *email == "" || *cat == "" || *tgtType == "" || *desc == "" {
		return errors.New("--email, --category, --target-type, --description are required")
	}
	// Build a client. The abuse endpoint is TryUserAuth — works
	// without credentials, but if we have them we'll send them so
	// the backend can capture our user_id.
	apiBase, token, userID, err := reportClientConfig()
	if err != nil {
		return err
	}
	client := api.New(apiBase, token).WithUserID(userID)
	if err := client.SubmitAbuseReport(cliout.WithCtx(), api.AbuseReportSubmit{
		ReporterEmail: *email, Category: *cat, TargetType: *tgtType,
		TargetID: *tgtID, Description: *desc, EvidenceURL: *evid,
	}); err != nil {
		return err
	}
	cliout.Println(i18n.T("report.filed"))
	return nil
}

func reportClientConfig() (apiBase, token string, userID int, err error) {
	apiBase = config.ResolveAPIBase("")
	creds, err := config.Load()
	if err != nil && !errors.Is(err, config.ErrNoCredentials) {
		// A corrupt/unreadable config is worth surfacing, not silently
		// falling back to an anonymous report against the default base.
		return "", "", 0, err
	}
	if creds != nil {
		token = creds.AccessToken
		userID = creds.UserID
	}
	return apiBase, token, userID, nil
}
