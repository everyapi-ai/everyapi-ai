package sanitizer

import "strings"

// OpenAIProtocol covers the OpenAI Chat Completions wire format and
// its compatible spinoffs (Codex CLI, almost every "OpenAI-compatible"
// SDK on the planet, including Anthropic-on-OpenAI shims and DeepSeek).
//
// Sanitisable text fields:
//
//   - messages[].content                — flat string body
//   - messages[].content[].text         — content-blocks variant
//   - messages[].tool_calls[].function.arguments — stringified JSON
//                                          arguments the model emitted;
//                                          can carry user-pasted secrets
//                                          on follow-up turns
//   - messages[].name                   — user-supplied speaker label
//   - tools[].function.description      — pinned in the prompt
//   - tools[].function.parameters … strings nested arbitrarily deep
//   - response_format.json_schema.description — same story
//
// Fields that must NOT be rewritten (control metadata):
//
//   - model, temperature, max_tokens, stream, top_p, frequency_penalty,
//     presence_penalty, stop, seed, user (an arbitrary id), tool_choice,
//     etc.
//
// The walkJSON helper matches by key name; we list every text-bearing
// key here and rely on the walk to find each leaf wherever it lives.
type OpenAIProtocol struct{}

func (p *OpenAIProtocol) Name() string { return "openai" }

func (p *OpenAIProtocol) PathMatch(path string) bool {
	// Match the chat-completions surface and adjacent legacy
	// surfaces that share the same body shape. Embeddings has
	// `input` which can be a string OR array — we treat input as
	// text too.
	return strings.HasPrefix(path, "/v1/chat/completions") ||
		strings.HasPrefix(path, "/v1/completions") ||
		strings.HasPrefix(path, "/v1/embeddings") ||
		strings.HasPrefix(path, "/v1/responses")
}

// openaiTextKeys is the registry of JSON object keys whose string
// value carries user-supplied text. Add to this set if a new field
// surfaces in OpenAI's spec — schema-driven rather than path-driven
// to stay robust against minor API revisions.
var openaiTextKeys = map[string]bool{
	"content":       true, // messages[].content (string variant) and content-blocks[].text-via-walkJSON
	"text":          true, // content-blocks[].text and tools[].input_schema descriptions occasionally
	"arguments":     true, // tool_calls[].function.arguments (stringified JSON, scanned as-is)
	"description":   true, // tools[].function.description, schemas
	"name":          true, // messages[].name
	"input":         true, // embeddings input string
	"instructions":  true, // /v1/responses
}

func (p *OpenAIProtocol) RewriteRequest(body []byte, detectors []Detector, m *Mapping) ([]byte, error) {
	return rewriteJSONBody(body, openaiTextKeys, detectors, m)
}
