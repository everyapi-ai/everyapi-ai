package api

import "strings"

// WebOriginFromBase maps the API host to the dashboard host:
// `https://api.everyapi.ai` → `https://app.everyapi.ai`, so any
// dashboard URL (wallet, seller, channels) printed by the CLI or the
// MCP server points at the UI host (app.*) — NOT the API host and
// NOT the marketing apex (`everyapi.ai`, the bare domain, is the
// landing page and serves no dashboard routes).
//
// Cheap heuristic: only an "api." subdomain is rewritten. Non-matching
// bases (localhost, custom self-host hosts) pass through unchanged so
// they still resolve. Both http:// and https:// are handled so a
// self-hosted setup pointed at http://api.example.com still produces
// http://app.example.com.
func WebOriginFromBase(base string) string {
	const httpsAPI = "https://api."
	if strings.HasPrefix(base, httpsAPI) {
		return "https://app." + base[len(httpsAPI):]
	}
	const httpAPI = "http://api."
	if strings.HasPrefix(base, httpAPI) {
		return "http://app." + base[len(httpAPI):]
	}
	return base
}
