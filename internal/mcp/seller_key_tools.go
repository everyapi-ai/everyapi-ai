package mcp

// Plain-API-key seller onboarding tools — the MCP counterpart of
// `everyapi seller add-key` / `everyapi seller setup`. There is no
// separate "setup wizard" tool: in MCP the AI agent IS the wizard —
// it checks eligibility, collects name / type / key / models from
// the user in conversation, then calls everyapi_seller_add_key once.
//
//   - everyapi_seller_eligibility — read-only mount-gate checklist
//   - everyapi_seller_add_key     — mount a channel with plain API key(s)
//
// The key arrives as a tool argument in plaintext. That matches the
// CLI's --key flag (the credential has to reach the backend either
// way); the tool description tells the agent to only pass a key the
// user explicitly provided in this conversation.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/sellertype"
	"github.com/everyapi-ai/everyapi-sdk/api"
)

// ---- everyapi_seller_eligibility --------------------------------------

func toolSellerEligibility() Tool {
	return Tool{
		Name: "everyapi_seller_eligibility",
		Description: "Check whether the user currently passes the EveryAPI marketplace " +
			"mount gates (marketplace open, account active, email verified, account age, " +
			"prior usage, channel cap). Read-only. Call this BEFORE asking the user for an " +
			"API key when they want to become a seller / mount a channel — failing a gate " +
			"after they've pasted a credential is a bad experience.",
		InputSchema: emptyObjectSchema,
		Handler:     handleSellerEligibility,
	}
}

func handleSellerEligibility(ctx context.Context, _ json.RawMessage) (string, error) {
	creds, err := loadCreds()
	if err != nil {
		return "", err
	}
	client := api.ForCredentials(creds)
	elig, err := client.GetSellerEligibility(ctx)
	if err != nil {
		return "", classifyAPIErr(err)
	}
	var b strings.Builder
	if elig.Eligible {
		b.WriteString("Eligible to mount seller channels.\n")
	} else {
		b.WriteString("NOT eligible to mount seller channels yet.\n")
	}
	b.WriteString(eligibilityChecklist(elig))
	if !elig.Eligible {
		fmt.Fprintf(&b, "\nDashboard: %s/seller/channels", api.WebOriginFromBase(creds.APIBase))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// eligibilityChecklist renders the gate-by-gate [x]/[ ] view shared
// by the eligibility tool and the add-key pre-check failure message.
// English-only — the CLI's localized twin is renderEligibility in
// cmd/seller.
func eligibilityChecklist(e *api.SellerEligibility) string {
	mark := func(ok bool) string {
		if ok {
			return "[x]"
		}
		return "[ ]"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "  %s marketplace open\n", mark(e.MarketplaceEnabled))
	fmt.Fprintf(&b, "  %s account active\n", mark(e.AccountActive))
	fmt.Fprintf(&b, "  %s email verified\n", mark(e.EmailVerified))
	fmt.Fprintf(&b, "  %s account age ≥ %d day(s)\n", mark(e.AccountAgeOK), e.MinAgeDays)
	fmt.Fprintf(&b, "  %s has prior API usage\n", mark(e.HasConsumeLog))
	fmt.Fprintf(&b, "  %s under channel cap (%d/%d)\n", mark(e.UnderCap), e.ChannelCount, e.ChannelCap)
	return b.String()
}

// ---- everyapi_seller_add_key -------------------------------------------

type sellerAddKeyArgs struct {
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	Keys       []string `json:"keys"`
	KeyRemarks []string `json:"key_remarks,omitempty"`
	Models     string   `json:"models"`
	Remark     string   `json:"remark,omitempty"`
}

var sellerAddKeySchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "name": {
      "type": "string",
      "description": "Channel display name shown on the marketplace, e.g. \"my-openai-pool\"."
    },
    "type": {
      "type": "string",
      "description": "Upstream provider: openai, claude, gemini, codex, vertex, aws, xai, deepseek."
    },
    "keys": {
      "type": "array",
      "items": {"type": "string", "minLength": 1},
      "minItems": 1,
      "description": "The upstream API key(s) to mount. More than one key forms a backup pool with per-key failover. ONLY pass keys the user explicitly provided in this conversation — never guess or reuse a key from elsewhere."
    },
    "key_remarks": {
      "type": "array",
      "items": {"type": "string"},
      "description": "Optional per-key labels, index-aligned with keys. Must not be longer than keys."
    },
    "models": {
      "type": "string",
      "description": "Comma-separated model list this channel serves, e.g. \"gpt-4o,gpt-4o-mini\"."
    },
    "remark": {
      "type": "string",
      "description": "Optional channel-level note (visible to the seller only)."
    }
  },
  "required": ["name", "type", "keys", "models"],
  "additionalProperties": false
}`)

func toolSellerAddKey() Tool {
	return Tool{
		Name: "everyapi_seller_add_key",
		Description: "Mount a new seller channel on the EveryAPI marketplace using plain API key(s) " +
			"the user provides. The MCP twin of `everyapi seller add-key`. Collect name, provider " +
			"type, key(s), and the served model list from the user first (consider " +
			"everyapi_seller_eligibility before asking for the key). The key is sent to the EveryAPI " +
			"backend, stored encrypted, and used to serve marketplace traffic — make sure the user " +
			"understands that before calling. Use when the user asks to 'sell my API key', 'mount a " +
			"channel', or 'list my key on the marketplace'.",
		InputSchema: sellerAddKeySchema,
		Handler:     handleSellerAddKey,
	}
}

func handleSellerAddKey(ctx context.Context, raw json.RawMessage) (string, error) {
	creds, err := loadCreds()
	if err != nil {
		return "", err
	}
	var args sellerAddKeyArgs
	if len(raw) > 0 && !isJSONNull(raw) {
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
	}
	if err := validateAddKeyArgs(&args); err != nil {
		return "", err
	}
	kindSlug, ok := sellertype.Resolve(args.Type)
	if !ok {
		return "", fmt.Errorf("unknown channel type %q — use one of %s",
			cliout.Sanitize(args.Type), strings.Join(sellertype.Choices(), ", "))
	}

	client := api.ForCredentials(creds)

	// Eligibility pre-check, same reasoning as the CLI: failing the
	// gate AFTER the user handed over a real API key is the worst
	// ordering. A failed eligibility QUERY (transport error, not a
	// failed gate) is non-fatal — fall through to the create call,
	// which re-checks every gate server-side.
	if elig, eErr := client.GetSellerEligibility(ctx); eErr == nil && !elig.Eligible {
		return "", fmt.Errorf("not eligible to mount seller channels yet:\n%s\nFix the unchecked gates, or see %s/seller/channels",
			strings.TrimRight(eligibilityChecklist(elig), "\n"), api.WebOriginFromBase(creds.APIBase))
	}

	id, err := client.CreateSellerChannel(ctx, api.SellerChannelCreate{
		Name:       args.Name,
		KindSlug:   kindSlug,
		Keys:       args.Keys,
		KeyRemarks: args.KeyRemarks,
		Models:     args.Models,
		Remark:     args.Remark,
	})
	if err != nil {
		return "", classifyAPIErr(err)
	}
	pool := ""
	if len(args.Keys) > 1 {
		pool = fmt.Sprintf(" with a %d-key backup pool", len(args.Keys))
	}
	return fmt.Sprintf("Mounted seller channel #%d: %s (type=%s)%s.\n"+
		"It starts enabled and serves marketplace traffic once the platform's health probe passes.\n"+
		"Manage it at %s/seller/channels or via everyapi_seller_list.",
		id, cliout.Sanitize(args.Name), sellertype.Label(kindSlug), pool, api.WebOriginFromBase(creds.APIBase)), nil
}

// validateAddKeyArgs enforces the same hard requirements as the CLI
// flag parser: all four core fields present, no blank key entries,
// and key_remarks never longer than keys (an over-long remark list is
// a typo worth failing loudly on, not silently truncating).
func validateAddKeyArgs(args *sellerAddKeyArgs) error {
	var missing []string
	if strings.TrimSpace(args.Name) == "" {
		missing = append(missing, "name")
	}
	if strings.TrimSpace(args.Type) == "" {
		missing = append(missing, "type")
	}
	if len(args.Keys) == 0 {
		missing = append(missing, "keys")
	}
	if strings.TrimSpace(args.Models) == "" {
		missing = append(missing, "models")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required argument(s): %s", strings.Join(missing, ", "))
	}
	for i, k := range args.Keys {
		if strings.TrimSpace(k) == "" {
			return fmt.Errorf("keys[%d] is empty — every key entry must be a non-blank API key", i)
		}
	}
	if len(args.KeyRemarks) > len(args.Keys) {
		return fmt.Errorf("key_remarks has %d entries but keys has %d — remarks are index-aligned with keys and may not exceed them",
			len(args.KeyRemarks), len(args.Keys))
	}
	// Duplicate key value is a typo. The backend's SetMultiKeySet silently
	// keeps the first occurrence ("first wins"), so a pasted-twice credential
	// would mount a channel that LOOKS like an N-key pool but carries one
	// credential twice in storage and only one in routing state. Mirror the
	// CLI's parseAddKeyArgs check and surface it before submit.
	if len(args.Keys) > 1 {
		seen := make(map[string]bool, len(args.Keys))
		for _, k := range args.Keys {
			if seen[k] {
				return fmt.Errorf("duplicate key — each credential must be distinct (the same key twice is a typo, not a backup)")
			}
			seen[k] = true
		}
	}
	return nil
}
