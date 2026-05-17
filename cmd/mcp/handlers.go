package mcp

// Tool handlers — placeholder stubs replaced in handlers.go once the
// seller API client + status renderer land. Kept separate so the
// protocol layer (server.go) can be reviewed without the api/config
// dependency tangle, and so tests can replace each tool 1-by-1.
//
// V0 invariants for every handler:
//   - First load credentials. ErrNoCredentials → friendly "run
//     relaya login first" error.
//   - 401 from backend → same error shape so the user-facing message
//     is consistent.
//   - All other errors → propagate via `error` return; protocol
//     layer renders them as isError=true tool results.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/relaya-ai/relaya-ai/internal/api"
	"github.com/relaya-ai/relaya-ai/internal/config"
)

// errNotLoggedIn is the canonical user-facing message for missing
// credentials. Single constant so every tool reports the same fix
// instruction.
var errNotLoggedIn = errors.New("not logged in — run 'relaya login' in your terminal first")

// loadCreds returns the on-disk credentials or errNotLoggedIn. Other
// I/O errors (corrupt JSON, perm) bubble up so the user sees the
// real problem.
func loadCreds() (*config.Credentials, error) {
	creds, err := config.Load()
	if errors.Is(err, config.ErrNoCredentials) {
		return nil, errNotLoggedIn
	}
	return creds, err
}

// classifyAPIErr maps backend API errors to user-facing tool errors.
// 401 gets a "session expired, re-login" hint; everything else is
// passed through with the server's message intact.
func classifyAPIErr(err error) error {
	if err == nil {
		return nil
	}
	if api.IsUnauthorized(err) {
		return errors.New("your session expired — run 'relaya login' in your terminal to refresh credentials")
	}
	return err
}

// trimAPIBaseToWebOrigin maps `https://api.relaya.pro` →
// `https://relaya.pro` so the topup / wallet URL points at the
// dashboard. Identical heuristic to the CLI status command; kept
// duplicated here because the production routing (api/dashboard
// split) is a permanent decision and the helper is two lines.
func trimAPIBaseToWebOrigin(base string) string {
	const apiPrefix = "https://api."
	if strings.HasPrefix(base, apiPrefix) {
		return "https://" + base[len(apiPrefix):]
	}
	const httpAPIPrefix = "http://api."
	if strings.HasPrefix(base, httpAPIPrefix) {
		return "http://" + base[len(httpAPIPrefix):]
	}
	return base
}

// ---- relaya_status -------------------------------------------------

func toolStatus() Tool {
	return Tool{
		Name:        "relaya_status",
		Description: "Show the current Relaya account: remaining quota (USD), used quota (USD), and total request count. Use this when the user asks about their Relaya balance, usage, or wallet state.",
		InputSchema: emptyObjectSchema,
		Handler:     handleStatus,
	}
}

func handleStatus(ctx context.Context, _ json.RawMessage) (string, error) {
	creds, err := loadCreds()
	if err != nil {
		return "", err
	}
	client := api.New(creds.APIBase, creds.AccessToken)
	status, err := client.GetStatus(ctx)
	if err != nil {
		return "", classifyAPIErr(err)
	}
	self, err := client.GetSelf(ctx)
	if err != nil {
		return "", classifyAPIErr(err)
	}
	perUnit := status.QuotaPerUnit
	if perUnit <= 0 {
		perUnit = 1
	}
	quotaUSD := float64(self.Quota) / perUnit
	usedUSD := float64(self.UsedQuota) / perUnit

	var b strings.Builder
	if self.Email != "" {
		fmt.Fprintf(&b, "Account: %s (%s)\n", self.Username, self.Email)
	} else {
		fmt.Fprintf(&b, "Account: %s\n", self.Username)
	}
	fmt.Fprintf(&b, "Quota:    $%.2f remaining   $%.2f used\n", quotaUSD, usedUSD)
	fmt.Fprintf(&b, "Requests: %d\n", self.RequestCount)
	fmt.Fprintf(&b, "Top-up:   %s/wallet", trimAPIBaseToWebOrigin(creds.APIBase))
	return b.String(), nil
}

// ---- relaya_topup --------------------------------------------------

func toolTopup() Tool {
	return Tool{
		Name:        "relaya_topup",
		Description: "Return the URL where the user can add credits to their Relaya account. Open this in a browser; payment is handled on the web dashboard.",
		InputSchema: emptyObjectSchema,
		Handler:     handleTopup,
	}
}

func handleTopup(_ context.Context, _ json.RawMessage) (string, error) {
	// topup doesn't strictly require credentials — the URL is
	// per-account on the dashboard side. But returning a URL the
	// user can't reach without logging in would be confusing; we
	// gate on credentials so the error is "go login first" rather
	// than "here's a link that won't work for you".
	creds, err := loadCreds()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Open in browser to top up: %s/wallet", trimAPIBaseToWebOrigin(creds.APIBase)), nil
}

// ---- relaya_seller_list --------------------------------------------

func toolSellerList() Tool {
	return Tool{
		Name:        "relaya_seller_list",
		Description: "List the user's mounted seller channels on the Relaya marketplace (id, name, upstream type, status, models). Use when the user asks 'what channels am I selling?' or 'show my marketplace listings'.",
		InputSchema: emptyObjectSchema,
		Handler:     handleSellerList,
	}
}

func handleSellerList(ctx context.Context, _ json.RawMessage) (string, error) {
	creds, err := loadCreds()
	if err != nil {
		return "", err
	}
	client := api.New(creds.APIBase, creds.AccessToken)
	channels, err := client.ListSellerChannels(ctx)
	if err != nil {
		return "", classifyAPIErr(err)
	}
	if len(channels) == 0 {
		return "No seller channels mounted. Visit " + trimAPIBaseToWebOrigin(creds.APIBase) + "/seller/channels to add one.", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d seller channel(s):\n", len(channels))
	for _, ch := range channels {
		fmt.Fprintf(&b, "  [#%d] %s — type=%d status=%s\n", ch.ID, ch.Name, ch.Type, statusLabel(ch.Status))
		if ch.Models != "" {
			fmt.Fprintf(&b, "        models: %s\n", ch.Models)
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// statusLabel maps the integer Channel.status to a human string.
// Aligned with backend/internal/common.ChannelStatus* constants
// (1=enabled, 2=manually-disabled, 3=auto-disabled). Unknown values
// pass through as the raw integer so a future status doesn't render
// as a misleading label.
func statusLabel(s int) string {
	switch s {
	case 1:
		return "enabled"
	case 2:
		return "disabled (manual)"
	case 3:
		return "disabled (auto)"
	default:
		return fmt.Sprintf("status=%d", s)
	}
}

// ---- relaya_seller_withdraw ----------------------------------------

// sellerWithdrawArgs allows the caller to specify a quota amount;
// omitted = transfer the full pending balance.
type sellerWithdrawArgs struct {
	// Quota is in DB units. We expose it directly so an AI agent
	// scripted by a developer can do partial transfers; humans will
	// usually leave it empty for "all".
	Quota *int `json:"quota,omitempty"`
}

var sellerWithdrawSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "quota": {
      "type": "integer",
      "description": "Optional: specific quota (in DB units) to transfer. Omit for full pending balance.",
      "minimum": 1
    }
  },
  "additionalProperties": false
}`)

func toolSellerWithdraw() Tool {
	return Tool{
		Name:        "relaya_seller_withdraw",
		Description: "Transfer the user's pending seller earnings to their main Relaya balance. Without arguments, transfers the entire pending amount. Use when the user asks to 'withdraw' or 'cash out' marketplace earnings.",
		InputSchema: sellerWithdrawSchema,
		Handler:     handleSellerWithdraw,
	}
}

func handleSellerWithdraw(ctx context.Context, raw json.RawMessage) (string, error) {
	creds, err := loadCreds()
	if err != nil {
		return "", err
	}
	var args sellerWithdrawArgs
	if len(raw) > 0 && !isJSONNull(raw) {
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
	}
	client := api.New(creds.APIBase, creds.AccessToken)

	// "Full balance" path: query /self for the pending seller_quota,
	// then transfer that. Two round-trips, but it's the only way
	// the user-friendly default works against a backend that
	// requires a numeric quota arg.
	var quota int
	if args.Quota != nil {
		quota = *args.Quota
	} else {
		self, err := client.GetSelf(ctx)
		if err != nil {
			return "", classifyAPIErr(err)
		}
		quota = self.SellerQuota
		if quota <= 0 {
			return "Nothing to withdraw — your seller balance is $0.", nil
		}
	}

	if err := client.TransferSellerQuota(ctx, quota); err != nil {
		return "", classifyAPIErr(err)
	}
	status, sErr := client.GetStatus(ctx)
	perUnit := 1.0
	if sErr == nil && status.QuotaPerUnit > 0 {
		perUnit = status.QuotaPerUnit
	}
	return fmt.Sprintf("Transferred $%.2f from seller balance to main balance.", float64(quota)/perUnit), nil
}

// isJSONNull is a quick check for the literal `null` payload that
// MCP clients sometimes send when a tool takes no args (instead of
// omitting the field). Treating both as "no args" avoids a
// false-positive "invalid arguments" rejection.
func isJSONNull(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s == "" || s == "null"
}
