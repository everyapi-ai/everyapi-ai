package mcp

// registerTools returns the V0 tool list. Each entry's Handler
// closes over the credentials lookup so the protocol layer doesn't
// need to know about auth. Tools that need credentials call
// loadCreds(); tools that don't (e.g. everyapi_topup) skip it.
//
// V0 set:
//   - everyapi_status         — quota / used / requests
//   - everyapi_topup          — wallet jump URL
//   - everyapi_seller_list    — mounted seller channels
//   - everyapi_seller_withdraw — transfer seller_quota to main balance
//
// V1 adds OAuth onboarding for two providers (codex device flow,
// claude paste flow). Each provider needs TWO tools because the
// flow spans multiple chat turns — the AI agent has to surface
// the user_code / authorize_url, wait for the human to act, then
// finish. Gemini's loopback flow doesn't translate to MCP (no place
// for an HTTP listener to live across tool calls), so it stays
// CLI-only.
//
//   - everyapi_seller_add_oauth_codex_start  — kick off device flow
//   - everyapi_seller_add_oauth_codex_poll   — finish device flow
//   - everyapi_seller_add_oauth_claude_start — get authorize_url
//   - everyapi_seller_add_oauth_claude_complete — paste code+state
//
// The remaining tools from the design list (sanitizer / seller setup
// / add-key) await their CLI counterparts.
func registerTools() []Tool {
	return []Tool{
		toolStatus(),
		toolTopup(),
		toolSellerList(),
		toolSellerWithdraw(),
		toolSellerAddOAuthCodexStart(),
		toolSellerAddOAuthCodexPoll(),
		toolSellerAddOAuthClaudeStart(),
		toolSellerAddOAuthClaudeComplete(),
	}
}
