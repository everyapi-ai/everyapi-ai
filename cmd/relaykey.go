package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/relaya-ai/relaya-ai/internal/api"
	"github.com/relaya-ai/relaya-ai/internal/config"
)

// errNoRelayKey: the account has no enabled relay API key the CLI can
// use. A distinct sentinel so callers can render an actionable
// message instead of conflating it with a transport failure.
var errNoRelayKey = errors.New("no enabled relay API key on the account")

// resolveRelayKey returns a relay API key (sk-relaya-…) for the relay
// path. The device-auth access token is a MANAGEMENT credential
// (UserAuth), not a relay key (TokenAuth → ValidateUserToken looks up
// the Token table) — feeding it to a tool as ANTHROPIC_AUTH_TOKEN is
// exactly why `relaya use` 401'd. So:
//
//   - creds.RelayKey already cached → return it. We do NOT re-resolve
//     every run: that would mask a key the user deliberately rotated,
//     and the caller's relay precheck already catches a dead key.
//   - otherwise → list the account's tokens via the management API
//     (UserAuth, hence WithUserID), pick the most-recently-created
//     ENABLED token (backend orders `id desc`, see
//     backend/internal/model/token.go GetAllUserTokens), fetch its
//     full key, cache it back into credentials.json, return it.
//
// Multi-token accounts have no override yet: the user gets the newest
// enabled key, full stop. A `--token=<name>` flag on `relaya use`
// would let users with multiple keys (prod vs dev, model-limited vs
// unrestricted) pick deliberately. Not in this PR.
//
// Mutates *creds and rewrites credentials.json so the lookup is
// one-time, not per-invocation.
func resolveRelayKey(creds *config.Credentials) (string, error) {
	if creds.RelayKey != "" {
		return creds.RelayKey, nil
	}

	client := api.New(creds.APIBase, creds.AccessToken).WithUserID(creds.UserID)
	ctx := withCtx()

	tokens, err := client.ListTokens(ctx)
	if err != nil {
		return "", fmt.Errorf("look up relay API key: %w", err)
	}
	var pick *api.TokenSummary
	for i := range tokens {
		if tokens[i].Status == api.TokenStatusEnabled {
			pick = &tokens[i]
			break
		}
	}
	if pick == nil {
		return "", errNoRelayKey
	}
	key, err := client.TokenKey(ctx, pick.ID)
	if err != nil {
		return "", fmt.Errorf("fetch relay API key %q: %w", pick.Name, err)
	}

	creds.RelayKey = key
	if err := config.Save(creds); err != nil {
		// Non-fatal: the key is good for this run; next run just
		// re-resolves. Surface it so a persistently unwritable config
		// dir doesn't silently re-hit the API on every invocation.
		fmt.Fprintln(os.Stderr, "warning: could not cache relay key to credentials.json:", err)
	}
	return key, nil
}
