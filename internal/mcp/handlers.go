package mcp

// Tool handlers — placeholder stubs replaced in handlers.go once the
// seller API client + status renderer land. Kept separate so the
// protocol layer (server.go) can be reviewed without the api/config
// dependency tangle, and so tests can replace each tool 1-by-1.
//
// V0 invariants for every handler:
//   - First load credentials. ErrNoCredentials → friendly "run
//     everyapi login first" error.
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
	"sync"

	"github.com/everyapi-ai/everyapi-ai/internal/api"
	"github.com/everyapi-ai/everyapi-ai/internal/config"
)

// errNotLoggedIn is the canonical user-facing message for missing
// credentials. Single constant so every tool reports the same fix
// instruction.
var errNotLoggedIn = errors.New("not logged in — run 'everyapi login' in your terminal first")

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

// sharedOAuthClient is a single api.Client kept alive for the duration
// of the MCP server process. The OAuth seller flows (codex device,
// claude paste) span MULTIPLE tool calls — start sets a session
// cookie on the backend, complete/poll must replay it — so the
// http.CookieJar can't be per-call. The CLI's per-invocation jar
// pattern doesn't apply: MCP is long-lived.
//
// One jar per process is also one user per process (credentials.json
// is a single account); concurrent flows from the same user are
// distinct backend session entries keyed by state, so they coexist.
//
// Not used by tools that don't need session continuity
// (everyapi_status, _topup, _seller_list, _seller_withdraw) — those
// still build a fresh client per call so a stale jar doesn't surprise
// them.
var (
	sharedOAuthClient     *api.Client
	sharedOAuthClientOnce sync.Once
)

// loadOAuthClient hands back the long-lived OAuth-aware client.
// Credentials are loaded ONCE on first use — a relogin while the MCP
// server is running won't be picked up until restart, which matches
// the CLI's "credentials are stable per process" assumption.
func loadOAuthClient() (*api.Client, *config.Credentials, error) {
	creds, err := loadCreds()
	if err != nil {
		return nil, nil, err
	}
	sharedOAuthClientOnce.Do(func() {
		sharedOAuthClient = api.New(creds.APIBase, creds.AccessToken).
			WithUserID(creds.UserID).
			WithCookieJar()
	})
	return sharedOAuthClient, creds, nil
}

// classifyAPIErr maps backend API errors to user-facing tool errors.
// 401 gets a "session expired, re-login" hint; everything else is
// passed through with the server's message intact.
func classifyAPIErr(err error) error {
	if err == nil {
		return nil
	}
	if api.IsUnauthorized(err) {
		return errors.New("your session expired — run 'everyapi login' in your terminal to refresh credentials")
	}
	return err
}

// ---- everyapi_status -------------------------------------------------

func toolStatus() Tool {
	return Tool{
		Name:        "everyapi_status",
		Description: "Show the current EveryAPI account: remaining quota (USD), used quota (USD), and total request count. Use this when the user asks about their EveryAPI balance, usage, or wallet state.",
		InputSchema: emptyObjectSchema,
		Handler:     handleStatus,
	}
}

func handleStatus(ctx context.Context, _ json.RawMessage) (string, error) {
	creds, err := loadCreds()
	if err != nil {
		return "", err
	}
	client := api.New(creds.APIBase, creds.AccessToken).WithUserID(creds.UserID)
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
	fmt.Fprintf(&b, "Top-up:   %s/wallet", api.WebOriginFromBase(creds.APIBase))
	return b.String(), nil
}

// ---- everyapi_topup --------------------------------------------------

func toolTopup() Tool {
	return Tool{
		Name:        "everyapi_topup",
		Description: "Return the URL where the user can add credits to their EveryAPI account. Open this in a browser; payment is handled on the web dashboard.",
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
	return fmt.Sprintf("Open in browser to top up: %s/wallet", api.WebOriginFromBase(creds.APIBase)), nil
}

// ---- everyapi_seller_list --------------------------------------------

func toolSellerList() Tool {
	return Tool{
		Name:        "everyapi_seller_list",
		Description: "List the user's mounted seller channels on the EveryAPI marketplace (id, name, upstream type, status, models). Use when the user asks 'what channels am I selling?' or 'show my marketplace listings'.",
		InputSchema: emptyObjectSchema,
		Handler:     handleSellerList,
	}
}

func handleSellerList(ctx context.Context, _ json.RawMessage) (string, error) {
	creds, err := loadCreds()
	if err != nil {
		return "", err
	}
	client := api.New(creds.APIBase, creds.AccessToken).WithUserID(creds.UserID)
	channels, err := client.ListSellerChannels(ctx)
	if err != nil {
		return "", classifyAPIErr(err)
	}
	if len(channels) == 0 {
		return "No seller channels mounted. Visit " + api.WebOriginFromBase(creds.APIBase) + "/seller/channels to add one.", nil
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
// Aligned with the server's ChannelStatus enum:
// 1=enabled, 2=manually-disabled, 3=auto-disabled. Unknown values
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

// ---- everyapi_seller_withdraw ----------------------------------------

// sellerWithdrawArgs is the JSON schema's mirror.
//
// `confirm` is a deliberate friction step on a money-moving tool.
// Any process on the user's machine that can read
// ~/.config/everyapi/credentials.json (a debug-enabled AI agent, a
// stowaway MCP client, malware that's reached the home dir) can
// otherwise invoke this transfer silently — the access token alone
// authorises the backend. Requiring an explicit "yes" string in the
// request body forces the calling AI to surface the action to the
// human first; a credential-stealing process can't fabricate
// intent. The token still needs to be guarded by the OS, but at
// least this tool stops being a one-step silent drain.
//
// Quota is in DB units. Optional — omitted = transfer the full
// pending balance.
type sellerWithdrawArgs struct {
	Confirm string `json:"confirm"`
	Quota   *int   `json:"quota,omitempty"`
}

// sellerWithdrawConfirmToken is the literal a caller must echo back
// in the `confirm` field to authorise a transfer. A constant string
// rather than a server-issued nonce so the tool stays stateless
// across the stdin/stdout MCP transport.
const sellerWithdrawConfirmToken = "yes"

var sellerWithdrawSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "confirm": {
      "type": "string",
      "description": "Required confirmation token. Must be the literal string \"yes\". Acts as a friction step so a credential-stealing process can't silently drain a seller balance through this tool.",
      "const": "yes"
    },
    "quota": {
      "type": "integer",
      "description": "Optional: specific quota (in DB units) to transfer. Omit for full pending balance.",
      "minimum": 1
    }
  },
  "required": ["confirm"],
  "additionalProperties": false
}`)

func toolSellerWithdraw() Tool {
	return Tool{
		Name: "everyapi_seller_withdraw",
		Description: "Transfer the user's pending seller earnings to their main EveryAPI balance. " +
			"REQUIRED: the caller MUST pass `confirm: \"yes\"` — this is a friction step on a money-" +
			"moving action so it can't be invoked silently by a credential-stealing process. " +
			"Without arguments other than confirm, transfers the entire pending amount. " +
			"Use when the user asks to 'withdraw' or 'cash out' marketplace earnings.",
		InputSchema: sellerWithdrawSchema,
		Handler:     handleSellerWithdraw,
	}
}

// errSellerWithdrawNeedsConfirm is the user-facing message when the
// confirm guard rejects a request. Kept as a sentinel so tests can
// assert on the exact contract surface.
var errSellerWithdrawNeedsConfirm = fmt.Errorf(
	`everyapi_seller_withdraw requires confirm: %q. `+
		`This is a money-moving tool — surface the transfer to the user before retrying with the confirm field set.`,
	sellerWithdrawConfirmToken,
)

func handleSellerWithdraw(ctx context.Context, raw json.RawMessage) (string, error) {
	// Order matters: loadCreds first → unmarshal → confirm gate.
	//
	// An unauthenticated caller hits errNotLoggedIn before they see
	// the friction step, so the suggested fix-action is the right
	// one ("run everyapi login") rather than the misleading "missing
	// confirm". The friction gate's threat model assumes the caller
	// already has credentials; protecting against unauthenticated
	// callers is loadCreds's job, not the gate's.
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
	// Friction gate. Constant-string check by design — see
	// sellerWithdrawArgs godoc for the threat model.
	if args.Confirm != sellerWithdrawConfirmToken {
		return "", errSellerWithdrawNeedsConfirm
	}

	client := api.New(creds.APIBase, creds.AccessToken).WithUserID(creds.UserID)

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
