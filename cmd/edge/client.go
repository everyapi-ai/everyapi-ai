package edge

import (
	"errors"
	"net/url"
	"strings"

	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
)

// edgeClient is the shared "load creds, build client" path every edge subcommand starts with. Mirrors cmd/seller's sellerClient — keeping the shape consistent so a future helper extraction is mechanical.
func edgeClient() (*api.Client, *config.Credentials, error) {
	creds, err := config.Load()
	if errors.Is(err, config.ErrNoCredentials) {
		return nil, nil, errors.New(i18n.T("auth.not_logged_in"))
	}
	if err != nil {
		return nil, nil, err
	}
	// Deliberately the login base, NOT api.ForCredentials (which applies settings.gateway_region): gateway_region is a buyer-path acceleration choice, and edge is the supplier side. Its REST client stays on the login base so it matches the reverse-WS gateway (register.go's gatewayURLFromAPIBase(creds.APIBase)) — the whole edge module dials one base regardless of the buyer region preference.
	return api.New(creds.APIBase, creds.AccessToken).WithUserID(creds.UserID), creds, nil
}

// gatewayURLFromAPIBase rewrites the SDK's REST API base (for example https://api.everyapi.ai) into the WS scheme the agent needs (for example wss://api.everyapi.ai). The agent appends '/edge/connect' itself, so we hand over the bare origin.
//
// Operator override via `--gateway` flag bypasses this. Useful for pointing a dev machine at a staging or local gateway without changing the SDK base.
func gatewayURLFromAPIBase(apiBase string) string {
	u, err := url.Parse(apiBase)
	if err != nil || u.Host == "" {
		return apiBase
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String()
}
