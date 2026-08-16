package mcp

// Admin (operator-role) tools. Backend rejects non-admin callers with 403 regardless, but the handlers ALSO check creds.IsAdmin up-front so the AI agent gets a clear "admin role required" message instead of a generic 401/403 envelope.
//
// V2 surface: marketplace_status (read) and marketplace_set (write, confirm-gated). Marketplace on/off affects every seller AND every buyer on the deployment, so the credential-leak threat model that gated everyapi_seller_withdraw applies here at higher stakes — the `confirm: "yes"` friction step is non-negotiable.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/everyapi-ai/everyapi-sdk/api"
)

// marketplaceOptionKey mirrors cmd/admin/marketplace.go. Kept as a constant here too (rather than imported) so the mcp package's dependency surface stays the SDK + config — no reach into cmd/.
const marketplaceOptionKey = "marketplace.enabled"

// errNotAdmin is the canonical "you're not the right role" message, shaped to give the AI agent enough context to surface the failure to the human without retrying.
var errNotAdmin = errors.New(
	"admin role required — this tool needs operator privileges on the EveryAPI " +
		"deployment. Skip the tool and tell the user it's restricted.",
)

// ensureAdmin gates the admin tools. The check is on the LOCAL credentials cache rather than the backend's 403 because:
//   - it avoids a wasted round-trip for the common non-admin caller;
//   - the user-facing error mentions the actual gate ("admin role required") rather than a generic auth-failure envelope.
//
// A backend that disagreed with the local Role (e.g. a freshly- promoted user whose last `everyapi auth login` was pre-promotion) still gets the 403 from the API call below — the local check is a cheap first line.
func ensureAdmin() (*api.Client, error) {
	creds, err := loadCreds()
	if err != nil {
		return nil, err
	}
	if !creds.IsAdmin() {
		return nil, errNotAdmin
	}
	return api.ForCredentials(creds), nil
}

// ---- everyapi_admin_marketplace_status -------------------------------

func toolAdminMarketplaceStatus() Tool {
	return Tool{
		Name: "everyapi_admin_marketplace_status",
		Description: "Read the EveryAPI marketplace.enabled flag (admin role required). " +
			"Returns 'enabled' / 'disabled' / 'unset (backend default: false)'. " +
			"Use when an operator asks whether the marketplace is currently open.",
		InputSchema: emptyObjectSchema,
		Handler:     handleAdminMarketplaceStatus,
	}
}

func handleAdminMarketplaceStatus(ctx context.Context, _ json.RawMessage) (string, error) {
	client, err := ensureAdmin()
	if err != nil {
		return "", err
	}
	val, found, err := client.GetOption(ctx, marketplaceOptionKey)
	if err != nil {
		return "", classifyAPIErr(err)
	}
	if !found {
		return "marketplace.enabled: unset (backend default: false — marketplace closed)", nil
	}
	label := val
	switch val {
	case "true":
		label = "true — marketplace OPEN (BYO-GPU + seller flow live)"
	case "false":
		label = "false — marketplace CLOSED (no new mounts; existing nodes keep running)"
	}
	return "marketplace.enabled: " + label, nil
}

// ---- everyapi_admin_marketplace_set ----------------------------------

// Single set tool (boolean payload) instead of separate on/off tools. Same surface as the everyapi_seller_withdraw confirm gate: a credential-leaked process could otherwise flip the marketplace silently and the AI is the only party in a position to refuse.

type adminMarketplaceSetArgs struct {
	Enabled *bool  `json:"enabled"`
	Confirm string `json:"confirm"`
}

const adminMarketplaceSetConfirmToken = "yes"

var adminMarketplaceSetSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "enabled": {
      "type": "boolean",
      "description": "Target state of marketplace.enabled. true = open the marketplace (allow new seller mounts + BYO-GPU registrations); false = close it (block new mounts; existing nodes / channels keep serving)."
    },
    "confirm": {
      "type": "string",
      "description": "Required confirmation token. Must be the literal string \"yes\". Friction step on a deployment-wide setting that affects every seller AND every buyer; the AI MUST surface the intent to the operator before passing this.",
      "const": "yes"
    }
  },
  "required": ["enabled", "confirm"],
  "additionalProperties": false
}`)

func toolAdminMarketplaceSet() Tool {
	return Tool{
		Name: "everyapi_admin_marketplace_set",
		Description: "Open or close the EveryAPI marketplace by setting marketplace.enabled " +
			"(admin role required). REQUIRED: pass `confirm: \"yes\"`. The flag gates ALL new " +
			"seller mounts and BYO-GPU node registrations across the deployment, so the AI " +
			"MUST surface the intent to the operator before calling this. Use only when the " +
			"operator explicitly asks to open or close the marketplace.",
		InputSchema: adminMarketplaceSetSchema,
		Handler:     handleAdminMarketplaceSet,
	}
}

func handleAdminMarketplaceSet(ctx context.Context, raw json.RawMessage) (string, error) {
	client, err := ensureAdmin()
	if err != nil {
		return "", err
	}
	var args adminMarketplaceSetArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
	}
	if args.Confirm != adminMarketplaceSetConfirmToken {
		return "", fmt.Errorf("everyapi_admin_marketplace_set requires confirm: %q. "+
			"This flag gates every new seller mount / BYO-GPU registration on the deployment "+
			"— surface the intent to the operator before retrying with confirm set.",
			adminMarketplaceSetConfirmToken)
	}
	// `enabled` is required, but the server does not validate args against InputSchema — a non-pointer bool can't tell "omitted" from "false", and the absent case would default to CLOSING the marketplace deployment-wide (a destructive, wrong-direction write past the confirm gate). The *bool makes the omission explicit so we reject it loudly instead of silently closing. (See repo Rule 4.)
	if args.Enabled == nil {
		return "", fmt.Errorf("everyapi_admin_marketplace_set requires `enabled` (true to open / false to close the marketplace); it was omitted")
	}
	prev, _, err := client.GetOption(ctx, marketplaceOptionKey)
	if err != nil {
		return "", classifyAPIErr(err)
	}
	if err := client.SetBoolOption(ctx, marketplaceOptionKey, *args.Enabled); err != nil {
		return "", classifyAPIErr(err)
	}
	target := "false"
	if *args.Enabled {
		target = "true"
	}
	prevLabel := prev
	if prevLabel == "" {
		prevLabel = "<unset>"
	}
	if prevLabel == target {
		return fmt.Sprintf("marketplace.enabled was already %s — no change.", target), nil
	}
	return fmt.Sprintf("marketplace.enabled: %s → %s (audit-logged by backend).",
		prevLabel, target), nil
}
