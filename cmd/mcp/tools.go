package mcp

// registerTools returns the V0 tool list. Each entry's Handler
// closes over the credentials lookup so the protocol layer doesn't
// need to know about auth. Tools that need credentials call
// loadCreds(); tools that don't (e.g. relaya_topup) skip it.
//
// V0 set:
//   - relaya_status         — quota / used / requests
//   - relaya_topup          — wallet jump URL
//   - relaya_seller_list    — mounted seller channels
//   - relaya_seller_withdraw — transfer seller_quota to main balance
//
// The full design lists 9 tools total; the other 5 (sanitizer /
// seller setup / add-key / add-oauth) await their underlying
// feature in a follow-up release.
func registerTools() []Tool {
	return []Tool{
		toolStatus(),
		toolTopup(),
		toolSellerList(),
		toolSellerWithdraw(),
	}
}
