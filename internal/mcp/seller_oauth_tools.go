// MCP tools wrapping the seller OAuth onboarding paths the CLI also
// exposes (`everyapi seller add-oauth codex|claude`). Each provider is
// two tools — start + finish — because the OAuth flow can't complete
// in a single chat turn: the AI agent has to print a code/URL,
// wait for the human to do something in a browser, then finish.
//
// Tool naming intentionally suffixes the provider name (rather than
// taking it as an argument) so MCP clients that render a flat tool
// list let users / models pick the right flow without a "which
// provider?" branch in the description.
//
// Gemini's loopback flow has no MCP equivalent here: a `/callback`
// listener bound to a random ephemeral port can't survive across
// two tool calls (the listener is per-process state, but the
// listener lifecycle would have to span pause-the-user time), and
// running the listener inside the MCP server process would force
// the AI agent into an awkward "I'll block this tool while a
// browser callback happens" UX. Gemini stays CLI-only.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/everyapi-ai/everyapi-ai/internal/api"
)

// ---- everyapi_seller_add_oauth_codex_start ----------------------------

type codexStartArgs struct {
	Name   string `json:"name"`
	Models string `json:"models"`
}

var codexStartSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "name":   {"type": "string", "description": "Channel display name shown in the dashboard. Free-form."},
    "models": {"type": "string", "description": "Comma-separated models this channel will serve (e.g. \"gpt-4,gpt-4o\")."}
  },
  "required": ["name", "models"],
  "additionalProperties": false
}`)

func toolSellerAddOAuthCodexStart() Tool {
	return Tool{
		Name: "everyapi_seller_add_oauth_codex_start",
		Description: strings.TrimSpace(`
Start an OAuth onboarding for a Codex / ChatGPT subscription as a seller channel.
Returns a short user_code + verification_uri the user must enter in their browser.
After the user finishes consent, the AI MUST call everyapi_seller_add_oauth_codex_poll
with the returned flow_id (usually a few times) until status="authorized".
Use when the user says they want to add their ChatGPT Plus / Codex account as a seller channel.
`),
		InputSchema: codexStartSchema,
		Handler:     handleSellerAddOAuthCodexStart,
	}
}

func handleSellerAddOAuthCodexStart(ctx context.Context, raw json.RawMessage) (string, error) {
	var args codexStartArgs
	if len(raw) > 0 && !isJSONNull(raw) {
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
	}
	args.Name = strings.TrimSpace(args.Name)
	args.Models = strings.TrimSpace(args.Models)
	if args.Name == "" || args.Models == "" {
		return "", errors.New("both `name` and `models` are required")
	}
	client, _, err := loadOAuthClient()
	if err != nil {
		return "", err
	}
	start, err := client.StartSellerCodexOAuth(ctx, args.Name, args.Models)
	if err != nil {
		return "", classifyAPIErr(err)
	}
	var b strings.Builder
	fmt.Fprintln(&b, "Codex device-auth flow started. Tell the user to do this:")
	fmt.Fprintf(&b, "  1) Open: %s\n", start.VerificationURI)
	fmt.Fprintf(&b, "  2) Enter the code: %s\n", start.UserCode)
	fmt.Fprintln(&b, "  3) Approve there, then come back.")
	fmt.Fprintf(&b, "After the user reports they've completed step 3, call everyapi_seller_add_oauth_codex_poll with flow_id=%q\n", start.FlowID)
	fmt.Fprintf(&b, "(initial poll interval %d sec; the flow expires in %d sec).", start.Interval, start.ExpiresIn)
	return b.String(), nil
}

// ---- everyapi_seller_add_oauth_codex_poll -----------------------------

type codexPollArgs struct {
	FlowID string `json:"flow_id"`
}

var codexPollSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "flow_id": {"type": "string", "description": "The flow_id returned by everyapi_seller_add_oauth_codex_start."}
  },
  "required": ["flow_id"],
  "additionalProperties": false
}`)

func toolSellerAddOAuthCodexPoll() Tool {
	return Tool{
		Name: "everyapi_seller_add_oauth_codex_poll",
		Description: strings.TrimSpace(`
Check whether the user has finished the Codex device-auth consent step.
Possible outcomes:
  - status="pending" — keep waiting; call again after a few seconds.
  - status="slow_down" — server asks for a longer poll interval.
  - status="authorized" — channel is mounted. Stop polling.
  - status="expired" / "denied" — the flow is dead; tell the user to re-run codex_start.
Use immediately after the user reports they finished consent, then re-call every few
seconds until status leaves "pending" or "slow_down".
`),
		InputSchema: codexPollSchema,
		Handler:     handleSellerAddOAuthCodexPoll,
	}
}

func handleSellerAddOAuthCodexPoll(ctx context.Context, raw json.RawMessage) (string, error) {
	var args codexPollArgs
	if len(raw) > 0 && !isJSONNull(raw) {
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
	}
	if strings.TrimSpace(args.FlowID) == "" {
		return "", errors.New("`flow_id` is required (from everyapi_seller_add_oauth_codex_start)")
	}
	client, creds, err := loadOAuthClient()
	if err != nil {
		return "", err
	}
	res, err := client.SellerCodexPoll(ctx, args.FlowID)
	if err != nil {
		return "", classifyAPIErr(err)
	}
	switch res.State {
	case api.SellerCodexPollPending:
		return "status=pending — keep waiting and call everyapi_seller_add_oauth_codex_poll again in a few seconds.", nil
	case api.SellerCodexPollSlowDown:
		return "status=slow_down — back off, wait at least 5 more seconds, then call everyapi_seller_add_oauth_codex_poll again.", nil
	case api.SellerCodexPollExpired:
		return "status=expired — the flow timed out. Tell the user to start over with everyapi_seller_add_oauth_codex_start.", nil
	case api.SellerCodexPollDenied:
		return "status=denied — the user rejected the request in their browser. Start over only if they want to try again.", nil
	case api.SellerCodexPollAuthorized:
		var b strings.Builder
		fmt.Fprintf(&b, "status=authorized — channel #%d mounted.\n", res.ChannelID)
		if res.Email != "" {
			fmt.Fprintf(&b, "Bound account: %s\n", res.Email)
		}
		fmt.Fprintf(&b, "Dashboard: %s/seller/channels", api.WebOriginFromBase(creds.APIBase))
		return b.String(), nil
	default:
		return "", fmt.Errorf("unexpected poll state %d", res.State)
	}
}

// ---- everyapi_seller_add_oauth_claude_start ---------------------------

type claudeStartArgs struct {
	Name   string `json:"name"`
	Models string `json:"models"`
}

var claudeStartSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "name":   {"type": "string", "description": "Channel display name shown in the dashboard."},
    "models": {"type": "string", "description": "Comma-separated models (e.g. \"claude-3-opus,claude-3-sonnet\")."}
  },
  "required": ["name", "models"],
  "additionalProperties": false
}`)

func toolSellerAddOAuthClaudeStart() Tool {
	return Tool{
		Name: "everyapi_seller_add_oauth_claude_start",
		Description: strings.TrimSpace(`
Start an OAuth onboarding for an Anthropic Claude account as a seller channel.
Returns an authorize_url the user must open in their browser to consent.
Anthropic's OAuth provider doesn't support automatic loopback callbacks, so AFTER
the user consents, Anthropic's callback page shows a "<code>#<state>" string the
user must paste back to you. Then call everyapi_seller_add_oauth_claude_complete
with that pasted input.
Use when the user wants to add their Anthropic Claude Pro account as a seller channel.
`),
		InputSchema: claudeStartSchema,
		Handler:     handleSellerAddOAuthClaudeStart,
	}
}

func handleSellerAddOAuthClaudeStart(ctx context.Context, raw json.RawMessage) (string, error) {
	var args claudeStartArgs
	if len(raw) > 0 && !isJSONNull(raw) {
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
	}
	args.Name = strings.TrimSpace(args.Name)
	args.Models = strings.TrimSpace(args.Models)
	if args.Name == "" || args.Models == "" {
		return "", errors.New("both `name` and `models` are required")
	}
	client, _, err := loadOAuthClient()
	if err != nil {
		return "", err
	}
	authorizeURL, err := client.StartSellerClaudeOAuth(ctx, args.Name, args.Models)
	if err != nil {
		return "", classifyAPIErr(err)
	}
	var b strings.Builder
	fmt.Fprintln(&b, "Claude OAuth flow started. Tell the user to do this:")
	fmt.Fprintf(&b, "  1) Open: %s\n", authorizeURL)
	fmt.Fprintln(&b, "  2) Sign in and approve the connection at Anthropic.")
	fmt.Fprintln(&b, "  3) Anthropic's callback page will show a `<code>#<state>` string.")
	fmt.Fprintln(&b, "  4) Tell you that string, then call everyapi_seller_add_oauth_claude_complete with input=<that string>.")
	return b.String(), nil
}

// ---- everyapi_seller_add_oauth_claude_complete ------------------------

type claudeCompleteArgs struct {
	Input string `json:"input"`
}

var claudeCompleteSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "input": {
      "type": "string",
      "description": "Whatever the user pasted from Anthropic's callback page. Accepts \"code#state\", a full URL, or just the code."
    }
  },
  "required": ["input"],
  "additionalProperties": false
}`)

func toolSellerAddOAuthClaudeComplete() Tool {
	return Tool{
		Name: "everyapi_seller_add_oauth_claude_complete",
		Description: strings.TrimSpace(`
Finish the Claude OAuth flow with whatever the user pasted from Anthropic's
callback page. On success, returns the newly mounted channel id. Call this
once the user has provided the "<code>#<state>" string after running
everyapi_seller_add_oauth_claude_start.
`),
		InputSchema: claudeCompleteSchema,
		Handler:     handleSellerAddOAuthClaudeComplete,
	}
}

func handleSellerAddOAuthClaudeComplete(ctx context.Context, raw json.RawMessage) (string, error) {
	var args claudeCompleteArgs
	if len(raw) > 0 && !isJSONNull(raw) {
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
	}
	if strings.TrimSpace(args.Input) == "" {
		return "", errors.New("`input` is required (the `<code>#<state>` string from Anthropic's callback page)")
	}
	client, creds, err := loadOAuthClient()
	if err != nil {
		return "", err
	}
	res, err := client.CompleteSellerClaudeOAuth(ctx, args.Input)
	if err != nil {
		return "", classifyAPIErr(err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Channel #%d mounted (type=claude).\n", res.ChannelID)
	if res.ExpiresAt != "" {
		fmt.Fprintf(&b, "Token expires: %s (auto-refreshes).\n", res.ExpiresAt)
	}
	fmt.Fprintf(&b, "Dashboard: %s/seller/channels", api.WebOriginFromBase(creds.APIBase))
	return b.String(), nil
}
