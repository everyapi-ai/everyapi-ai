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
// V2 adds the read-and-delete slice of the BYO-GPU edge surface.
// register / start / stop / logs stay CLI-only — they need either
// local docker access or one-shot-secret display that the chat
// channel can't preserve.
//
//   - everyapi_edge_list    — every node owned by the caller
//   - everyapi_edge_status  — one node's detail (by id)
//   - everyapi_edge_remove  — delete a node (confirm-gated)
//
// V2 also exposes the operator marketplace toggle. Both handlers
// check creds.IsAdmin locally before hitting the backend so a
// non-admin AI agent sees "admin role required" rather than a bare
// 403 envelope; the backend still gates the actual write.
//
//   - everyapi_admin_marketplace_status — read marketplace.enabled
//   - everyapi_admin_marketplace_set    — open / close (confirm-gated)
//
// V3 adds plain-API-key seller onboarding, mirroring the CLI's
// `seller add-key` / `seller setup` pair. There's no wizard tool —
// the AI agent is the wizard: it checks eligibility, collects the
// channel details in conversation, then mounts in one call.
//
//   - everyapi_seller_eligibility — read-only mount-gate checklist
//   - everyapi_seller_add_key     — mount a channel with plain API key(s)
//
// The remaining tool from the design list (sanitizer) stays CLI-only —
// it's a local daemon; what an AI agent controlling it would even
// mean is still undecided.
func registerTools() []Tool {
	return []Tool{
		toolStatus(),
		toolTopup(),
		toolSellerList(),
		toolSellerWithdraw(),
		toolSellerEligibility(),
		toolSellerAddKey(),
		toolSellerAddOAuthCodexStart(),
		toolSellerAddOAuthCodexPoll(),
		toolSellerAddOAuthClaudeStart(),
		toolSellerAddOAuthClaudeComplete(),
		toolEdgeList(),
		toolEdgeStatus(),
		toolEdgeRemove(),
		toolAdminMarketplaceStatus(),
		toolAdminMarketplaceSet(),
	}
}

// ToolNames returns the name of every tool this server exposes, in
// registration order.
//
// Exported for `cmd/mcp`, which prints per-client setup instructions that
// include approval-gating advice keyed to individual tool names. That advice
// has to stay in step with what the server actually registers: a new write
// tool that no caller knows about would be advertised to an agent while
// sitting outside every published gate. Reading the registry here makes the
// drift a test failure instead of a silent gap.
func ToolNames() []string {
	tools := registerTools()
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name)
	}
	return names
}
