package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/everyapi-ai/everyapi-ai/internal/api"
	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/config"
)

// Re-exports of the shared sentinels so existing call sites in this
// package can match against them without taking a fresh dependency
// on the api package.
var (
	errNoRelayKey         = api.ErrNoRelayKey
	errNoRelayKeyForGroup = api.ErrNoRelayKeyForGroup
)

// resolveRelayKey is the CLI-side wrapper around api.ResolveRelayKey:
// it adds the historical "warn to stderr if the cache write fails"
// behaviour and uses cliout's short-lived context. See
// internal/api/relaykey.go for the resolution rules — this layer
// only owns the side-effect choices.
func resolveRelayKey(creds *config.Credentials, group string) (string, error) {
	ctx := cliout.WithCtx()
	key, err := api.ResolveRelayKey(ctx, creds, group)
	var saveErr *api.ErrCacheSave
	if errors.As(err, &saveErr) {
		// Non-fatal: the key is good for this run; next run just
		// re-resolves. Surface so a persistently unwritable config
		// dir doesn't silently re-hit the API every invocation.
		fmt.Fprintln(os.Stderr, "warning: could not cache relay key to credentials.json:", saveErr.Err)
		return key, nil
	}
	return key, err
}
