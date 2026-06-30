package admin

import (
	"errors"
	"fmt"

	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
)

const marketplaceOptionKey = "marketplace.enabled"

func adminMarketplace(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi admin marketplace {status|on|off}")
	}
	sub := args[0]
	switch sub {
	case "status":
		return marketplaceStatus()
	case "on":
		return marketplaceSet(true)
	case "off":
		return marketplaceSet(false)
	default:
		return fmt.Errorf("unknown 'admin marketplace' subcommand %q (expected status|on|off)", sub)
	}
}

func marketplaceStatus() error {
	client, _, err := adminClient()
	if err != nil {
		return err
	}
	val, found, err := client.GetOption(cliout.WithCtx(), marketplaceOptionKey)
	if err != nil {
		return classifyErr(err)
	}
	if !found {
		cliout.Printf("%s", i18n.T("admin.marketplace.status_unset"))
		return nil
	}
	cliout.Printf(i18n.T("admin.marketplace.status_set"), boolState(val))
	return nil
}

func marketplaceSet(target bool) error {
	client, _, err := adminClient()
	if err != nil {
		return err
	}
	targetStr := "false"
	if target {
		targetStr = "true"
	}
	// Read-then-write so the operator's terminal shows whether this
	// actually changed state (vs. re-affirmed the existing value).
	// One extra GET round-trip is fine for a once-in-a-while ops
	// command — there is no batch use case for this.
	prev, _, err := client.GetOption(cliout.WithCtx(), marketplaceOptionKey)
	if err != nil {
		return classifyErr(err)
	}
	if err := client.SetBoolOption(cliout.WithCtx(), marketplaceOptionKey, target); err != nil {
		return classifyErr(err)
	}
	if prev == targetStr {
		cliout.Printf(i18n.T("admin.marketplace.no_change"), boolState(targetStr))
		return nil
	}
	cliout.Printf(i18n.T("admin.marketplace.changed"), boolState(prevOrUnset(prev)), boolState(targetStr))
	return nil
}

func prevOrUnset(prev string) string {
	if prev == "" {
		return "<unset>"
	}
	return prev
}

// adminClient mirrors cmd/seller's sellerClient — load creds, build
// SDK client, hand both back. Same shape so a future helper
// extraction is mechanical.
func adminClient() (*api.Client, *config.Credentials, error) {
	creds, err := config.Load()
	if errors.Is(err, config.ErrNoCredentials) {
		return nil, nil, errors.New(i18n.T("auth.not_logged_in"))
	}
	if err != nil {
		return nil, nil, err
	}
	return api.New(creds.APIBase, creds.AccessToken).WithUserID(creds.UserID), creds, nil
}
