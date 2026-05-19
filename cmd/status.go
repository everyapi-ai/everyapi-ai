package cmd

import (
	"errors"
	"flag"
	"fmt"

	"github.com/everyapi-ai/everyapi-ai/internal/api"
	"github.com/everyapi-ai/everyapi-ai/internal/config"
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
		return errors.New("not logged in — run 'everyapi login' first")
	}
	if err != nil {
		return err
	}
	client := api.New(creds.APIBase, creds.AccessToken).WithUserID(creds.UserID)
	ctx := withCtx()

	status, err := client.GetStatus(ctx)
	if err != nil {
		return fmt.Errorf("fetch system status: %w", err)
	}
	self, err := client.GetSelf(ctx)
	if err != nil {
		if api.IsUnauthorized(err) {
			return errors.New("your session expired — run 'everyapi login' again")
		}
		return fmt.Errorf("fetch user: %w", err)
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

	println("")
	if self.Email != "" {
		printf("  %s (%s)\n", self.Username, self.Email)
	} else {
		printf("  %s\n", self.Username)
	}
	printf("  quota:     $%.2f remaining   $%.2f used\n", quotaUSD, usedUSD)
	printf("  requests:  %d\n", self.RequestCount)
	printf("  topup:     %s/wallet\n", trimAPIBaseToWebOrigin(creds.APIBase))

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
		printf("  relay:     NOT CONFIGURED — no relay API key on the account\n")
		printf("             create an API key in the dashboard, then 'everyapi login'\n")
	case rkErr != nil:
		// Token lookup itself failed (transport, 5xx, etc.). Not a
		// verdict on the key — just say we couldn't check.
		printf("  relay:     unknown — could not resolve relay key (%v)\n", rkErr)
	default:
		perr := api.New(creds.APIBase, relayKey).ProbeRelayToken(ctx)
		switch {
		case perr == nil:
			printf("  relay:     ok\n")
		case api.IsUnauthorized(perr):
			printf("  relay:     UNAVAILABLE — relay key invalid / expired / disabled / out of quota\n")
			printf("             (account quota above is separate; top up %s/wallet or run 'everyapi login')\n",
				trimAPIBaseToWebOrigin(creds.APIBase))
		default:
			// Non-401 probe failure (5xx, network). The key may be
			// fine — we just couldn't get a verdict. Same shape as
			// the lookup-failure branch.
			printf("  relay:     unknown — probe failed (%v)\n", perr)
		}
	}

	println("")
	return nil
}

// trimAPIBaseToWebOrigin maps the API host to the dashboard host:
// `https://api.everyapi.ai` → `https://app.everyapi.ai`, so the printed
// wallet URL points at the dashboard (app.*) where the wallet UI
// lives — NOT the API host and NOT the marketing site (everyapi.ai,
// the bare apex, is the landing page and has no /wallet). Cheap
// heuristic — only the "api." subdomain is rewritten; non-matching
// bases (localhost, custom self-host hosts) are left unchanged so
// they still resolve.
func trimAPIBaseToWebOrigin(base string) string {
	const apiPrefix = "https://api."
	if len(base) > len(apiPrefix) && base[:len(apiPrefix)] == apiPrefix {
		return "https://app." + base[len(apiPrefix):]
	}
	return base
}
