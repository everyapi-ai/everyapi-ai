package sanitizer

import "strings"

// AnthropicProtocol covers the native Anthropic Messages wire format
// — i.e. POST /v1/messages on api.anthropic.com (and our gateway).
// Used by Claude Code, the Anthropic Python/TS SDKs, and any client
// targeting Messages directly.
//
// Sanitisable text fields:
//
//   - system               — string or array of content blocks (some
//                            clients pass an array; we cover both via walkJSON)
//   - messages[].content   — string or array of content blocks
//   - content[].text       — text block body
//   - content[].input      — tool_use input (stringified JSON arguments)
//   - tools[].description  — tool description pinned in the prompt
//   - tools[].input_schema.* — nested string descriptions
//
// Fields that must NOT be rewritten:
//
//   - model, max_tokens, temperature, stream, system (when it's a
//     control directive vs user content — we DO sanitise it since
//     buyers can paste secrets into a system prompt; the worst case
//     is a placeholder showing in the system instruction, which the
//     trust-minimal property says is fine).
//   - metadata.user_id, top_k, top_p, stop_sequences
//   - cache_control (prompt-cache markers — these are object refs,
//     not strings, so walkJSON wouldn't touch them even if their
//     parent key were in textKeys).
type AnthropicProtocol struct{}

func (p *AnthropicProtocol) Name() string { return "anthropic" }

func (p *AnthropicProtocol) PathMatch(path string) bool {
	return strings.HasPrefix(path, "/v1/messages") ||
		strings.HasPrefix(path, "/v1/complete")
}

// anthropicTextKeys overlaps with openaiTextKeys but lives separately
// so the per-protocol set stays self-documenting and one API's
// schema drift doesn't quietly change the other's sanitiser surface.
var anthropicTextKeys = map[string]bool{
	"content":     true, // messages[].content + system content blocks
	"text":        true, // content[].text
	"input":       true, // content[].input (tool_use args)
	"description": true, // tools[].description, schemas
	"system":      true, // top-level system string form
	"name":        true, // tools[].name (rarely sensitive but cheap)
}

func (p *AnthropicProtocol) RewriteRequest(body []byte, detectors []Detector, m *Mapping) ([]byte, error) {
	return rewriteJSONBody(body, anthropicTextKeys, detectors, m)
}
