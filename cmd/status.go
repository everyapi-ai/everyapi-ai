package cmd

import (
	"context"
	"errors"
	"flag"
	"fmt"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/internal/style"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

// styledQuota renders the "remaining / used" body of the quota line.
// The amounts are marked with **…** in status.remaining_used across
// every locale; routing the formatted string through style.Emph bolds
// them on a styled terminal and strips the markers to plain text when
// output is piped / NO_COLOR — keeping `everyapi auth status | grep`
// parseable. Markers live in the format string, never in the data, so
// formatting must happen before the Emph pass.
func styledQuota(quotaUSD, usedUSD float64) string {
	return style.Emph(fmt.Sprintf(i18n.T("status.remaining_used"), quotaUSD, usedUSD))
}

// Status renders the user's quota and usage in USD. Reads
// quota_per_unit from /api/status (unauthenticated) so a stale token
// produces a clean 401 from /api/user/self rather than a confusing
// "got JSON but no quota_per_unit" path.
//
// SIDE EFFECT: on first run after upgrade (when credentials.json has
// no relay_key), resolveRelayKey resolves and caches the relay API
// key, rewriting credentials.json. Subsequent runs are read-only.
// We accept the asymmetry because the alternative — making the user
// run `everyapi auth login` after upgrade — is worse UX, and the rewrite is
// a one-time migration.
func Status(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := rejectFlagPositionals(fs); err != nil {
		return err
	}
	creds, err := config.Load()
	if errors.Is(err, config.ErrNoCredentials) {
		return errors.New(i18n.T("auth.not_logged_in"))
	}
	if err != nil {
		return err
	}
	ctx := cliout.WithCtx()

	// OAuth2 relay-key logins (the fallback when the legacy
	// /api/cli/device-auth-start is absent) carry no management session:
	// the stored access token IS the relay key and there's no user id,
	// so GetSelf/quota can't work. Don't mislead the user with the
	// "session expired — re-login" string below (re-login via the same
	// OAuth2 path reproduces this exact state). Show relay health only.
	if creds.OAuthClientID != "" {
		cliout.Println("")
		cliout.Println(i18n.T("status.relay_key_mode"))
		printRelayHealth(ctx, creds)
		cliout.Println("")
		return nil
	}

	client := api.ForCredentials(creds)

	status, err := client.GetStatus(ctx)
	if err != nil {
		return fmt.Errorf("fetch system status: %w", err)
	}
	self, err := client.GetSelf(ctx)
	if err != nil {
		// GetStatus (public) just succeeded, so the backend is reachable
		// — a rejection of the authenticated self call means the session
		// is bad. That arrives either as a 401 or, for an invalid token,
		// as the backend's HTTP-200 envelope rejection (EnvelopeError);
		// both map to the friendly "session expired, re-login" message.
		var envErr *api.EnvelopeError
		if api.IsUnauthorized(err) || errors.As(err, &envErr) {
			return errors.New(i18n.T("auth.session_expired"))
		}
		return fmt.Errorf("fetch user: %w", err)
	}
	// Lazy-migrate the cached role for credentials.json files
	// written before the Role field existed. Old creds end up with
	// Role=0 (treated as non-admin → help hides admin block); the
	// first `everyapi auth status` from an admin user repopulates it
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
		cliout.Printf("  %s (%s)\n", style.Bold(cliout.Sanitize(self.Username)), cliout.Sanitize(self.Email))
	} else {
		cliout.Printf("  %s\n", style.Bold(cliout.Sanitize(self.Username)))
	}
	cliout.Printf("  %-10s %s\n", i18n.T("status.quota"), styledQuota(quotaUSD, usedUSD))
	cliout.Printf("  %-10s %s\n", i18n.T("status.requests"), style.Bold(fmt.Sprintf("%d", self.RequestCount)))
	cliout.Printf("  %-10s %s/wallet\n", i18n.T("status.topup"), api.WebOriginFromBase(creds.APIBase))

	printRelayHealth(ctx, creds)

	cliout.Println("")
	return nil
}

// printRelayHealth resolves the account's relay key and probes the relay
// path, emitting one `relay:` line. The quota line above comes from
// /api/user/self (UserAuth) and says nothing about whether the RELAY
// works: the access token can't relay at all (different auth path), and
// a relay key can be dead while the account quota is fine. We always
// emit a line — including `unknown` on transient lookup/probe failures —
// so the output shape stays consistent (a blank section is ambiguous).
// status reports account-level relay health, not a per-group override —
// always the default key path (group "").
func printRelayHealth(ctx context.Context, creds *config.Credentials) {
	gw := config.ResolveAPIBaseForBase(creds.APIBase)
	relayKey, rkErr := resolveRelayKey(creds, "")
	switch {
	case errors.Is(rkErr, errNoRelayKey):
		cliout.Printf("  relay:     %s — no relay API key on the account\n", style.Bold("NOT CONFIGURED"))
		cliout.Printf("             create an API key in the dashboard, then 'everyapi auth login'\n")
	case rkErr != nil:
		// Token lookup itself failed (transport, 5xx, etc.). Not a
		// verdict on the key — just say we couldn't check.
		cliout.Printf("  relay:     %s — could not resolve relay key (%v)\n", style.Bold("unknown"), rkErr)
	default:
		perr := api.New(gw, relayKey).ProbeRelayToken(ctx)
		switch {
		case perr == nil:
			cliout.Printf("  relay:     %s\n", style.Bold("ok"))
		case api.IsUnauthorized(perr):
			cliout.Printf("  relay:     %s — relay key invalid / expired / disabled / out of quota\n", style.Bold("UNAVAILABLE"))
			cliout.Printf("             (account quota above is separate; top up %s/wallet or run 'everyapi auth login')\n",
				api.WebOriginFromBase(creds.APIBase))
		default:
			// Non-401 probe failure (5xx, network). The key may be
			// fine — we just couldn't get a verdict. Same shape as
			// the lookup-failure branch.
			cliout.Printf("  relay:     %s — probe failed (%v)\n", style.Bold("unknown"), perr)
		}
	}
}
