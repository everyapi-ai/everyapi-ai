package cmd

import (
	"errors"
	"flag"
	"fmt"

	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

// Status renders the user's quota and usage in USD. Reads
// quota_per_unit from /api/status (unauthenticated) so a stale token
// produces a clean 401 from /api/user/self rather than a confusing
// "got JSON but no quota_per_unit" path.
//
// SIDE EFFECT: on first run after upgrade (when credentials.json has
// no relay_key), resolveRelayKey resolves and caches the relay API
// key, rewriting credentials.json. Subsequent runs are read-only.
// We accept the asymmetry because the alternative — making the user
// run `everyapi login` after upgrade — is worse UX, and the rewrite is
// a one-time migration.
func Status(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	creds, err := config.Load()
	if errors.Is(err, config.ErrNoCredentials) {
		return errors.New(i18n.T("auth.not_logged_in"))
	}
	if err != nil {
		return err
	}
	client := api.New(creds.APIBase, creds.AccessToken).WithUserID(creds.UserID)
	ctx := cliout.WithCtx()

	status, err := client.GetStatus(ctx)
	if err != nil {
		return fmt.Errorf("fetch system status: %w", err)
	}
	self, err := client.GetSelf(ctx)
	if err != nil {
		if api.IsUnauthorized(err) {
			return errors.New(i18n.T("auth.session_expired"))
		}
		return fmt.Errorf("fetch user: %w", err)
	}
	// Lazy-migrate the cached role for credentials.json files
	// written before the Role field existed. Old creds end up with
	// Role=0 (treated as non-admin → help hides admin block); the
	// first `everyapi status` from an admin user repopulates it
	// without needing them to re-login. Save errors are non-fatal —
	// status display is the primary job here.
	if self.Role != creds.Role {
		creds.Role = self.Role
		_ = config.Save(creds)
	}

	perUnit := status.QuotaPerUnit
	if perUnit <= 0 {
		// Defensive: server should always send a positive value; if it
		// doesn't, fall back to displaying raw quota integers rather
		// than dividing by zero.
		perUnit = 1
	}
	quotaUSD := float64(self.Quota) / perUnit
	usedUSD := float64(self.UsedQuota) / perUnit

	cliout.Println("")
	if self.Email != "" {
		cliout.Printf("  %s (%s)\n", self.Username, self.Email)
	} else {
		cliout.Printf("  %s\n", self.Username)
	}
	cliout.Printf("  %-10s "+i18n.T("status.remaining_used")+"\n", i18n.T("status.quota"), quotaUSD, usedUSD)
	cliout.Printf("  %-10s %d\n", i18n.T("status.requests"), self.RequestCount)
	cliout.Printf("  %-10s %s/wallet\n", i18n.T("status.topup"), api.WebOriginFromBase(creds.APIBase))

	// The quota line above comes from /api/user/self (UserAuth) and
	// says nothing about whether the RELAY works: the access token
	// can't relay at all (different auth path), and a relay key can
	// be dead while the account quota is fine. Resolve the relay key
	// and probe the relay path so this distinction is visible — it's
	// the exact confusion that sent us down a long debug rabbit hole.
	//
	// Always emit a `relay:` line — including a `unknown` line on
	// transient lookup/probe failures — so the output shape is
	// consistent. A blank section is ambiguous (still loading? not
	// implemented?); "unknown — transient API error" is honest.
	// status reports account-level relay health, not a per-group
	// override — always the default key path (group "").
	relayKey, rkErr := resolveRelayKey(creds, "")
	switch {
	case errors.Is(rkErr, errNoRelayKey):
		cliout.Printf("  relay:     NOT CONFIGURED — no relay API key on the account\n")
		cliout.Printf("             create an API key in the dashboard, then 'everyapi login'\n")
	case rkErr != nil:
		// Token lookup itself failed (transport, 5xx, etc.). Not a
		// verdict on the key — just say we couldn't check.
		cliout.Printf("  relay:     unknown — could not resolve relay key (%v)\n", rkErr)
	default:
		perr := api.New(creds.APIBase, relayKey).ProbeRelayToken(ctx)
		switch {
		case perr == nil:
			cliout.Printf("  relay:     ok\n")
		case api.IsUnauthorized(perr):
			cliout.Printf("  relay:     UNAVAILABLE — relay key invalid / expired / disabled / out of quota\n")
			cliout.Printf("             (account quota above is separate; top up %s/wallet or run 'everyapi login')\n",
				api.WebOriginFromBase(creds.APIBase))
		default:
			// Non-401 probe failure (5xx, network). The key may be
			// fine — we just couldn't get a verdict. Same shape as
			// the lookup-failure branch.
			cliout.Printf("  relay:     unknown — probe failed (%v)\n", perr)
		}
	}

	cliout.Println("")
	return nil
}
