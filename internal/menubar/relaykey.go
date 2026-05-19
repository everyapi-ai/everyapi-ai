package menubar

import (
	"context"
	"errors"

	"github.com/everyapi-ai/everyapi-ai/internal/api"
	"github.com/everyapi-ai/everyapi-ai/internal/config"
)

// errNoRelayKey is the menubar-friendly re-export of the shared
// api sentinel. The wording is tweaked for the GUI surface
// ("create one in the dashboard" reads better than "on the
// account").
var errNoRelayKey = errors.New("no enabled relay API key on this account — create one in the dashboard")

// resolveRelayKey is the menubar wrapper around api.ResolveRelayKey.
// It maps the cache-save failure to a desktop notification (the
// menubar has no stderr surface a user would ever read) and
// normalises the no-key sentinel into the GUI-friendly variant.
//
// See internal/api/relaykey.go for the actual resolution semantics.
func resolveRelayKey(ctx context.Context, creds *config.Credentials) (string, error) {
	key, err := api.ResolveRelayKey(ctx, creds, "")
	var saveErr *api.ErrCacheSave
	switch {
	case errors.As(err, &saveErr):
		// Non-fatal: surface as a notification so a persistently
		// broken config dir doesn't silently re-hit the API per click.
		notify("EveryAPI — couldn't cache relay key", saveErr.Err.Error())
		return key, nil
	case errors.Is(err, api.ErrNoRelayKey):
		return "", errNoRelayKey
	}
	return key, err
}

// relayKeyPrefix returns the first 16 chars of the key for the
// post-copy confirmation notification. We deliberately don't show
// the full key — even though the user just put it on the
// clipboard, the notification banner is a screen-share / screen-
// recording risk surface.
func relayKeyPrefix(key string) string {
	if len(key) <= 16 {
		return key
	}
	return key[:16] + "…"
}
