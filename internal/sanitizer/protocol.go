package sanitizer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Protocol abstracts the wire format of one upstream LLM API. The
// sanitizer needs to know which JSON fields carry user-supplied text
// (and are therefore sanitisable) versus which carry control metadata
// that must pass through verbatim — substituting the wrong field
// produces garbage on the model side (e.g. swapping a `model` field
// value silently changes which model is billed).
//
// Each implementation provides:
//
//   - PathMatch: does this proxy URL path map to this protocol? The
//     server dispatches by path prefix.
//   - RewriteRequest: read a JSON request body, walk the text-bearing
//     fields, run detectors, replace with placeholders, return the
//     rewritten body bytes. Returns the original body unchanged when
//     no detector fires.
//   - StreamingResponse: does this protocol use SSE for streaming?
//     The proxy uses this to decide whether to run the streaming
//     state machine on responses.
//
// Adding a protocol = one new file implementing this interface plus a
// registry entry in Protocols().
type Protocol interface {
	Name() string
	// PathMatch reports whether the proxy path (e.g. "/v1/messages")
	// routes to this protocol's handler.
	PathMatch(path string) bool
	// RewriteRequest scans + replaces sensitive substrings in the
	// JSON request body and returns the rewritten body. detectors
	// + mapping are shared across protocols within a request.
	RewriteRequest(body []byte, detectors []Detector, m *Mapping) ([]byte, error)
}

// Protocols returns the registered set in path-dispatch priority
// order. The first matching PathMatch wins, so longer / more
// specific prefixes go first.
func Protocols() []Protocol {
	return []Protocol{
		&AnthropicProtocol{},
		&GeminiProtocol{},
		&OpenAIProtocol{},
	}
}

// ScanAndReplaceText is the shared helper: run all detectors on s,
// resolve overlaps, splice placeholders. Used by every protocol's
// per-field walk.
func ScanAndReplaceText(s string, detectors []Detector, m *Mapping) string {
	if s == "" {
		return s
	}
	matches := Scan(s, detectors)
	if len(matches) == 0 {
		return s
	}
	return ReplaceWith(s, matches, m)
}

// walkJSON walks a parsed JSON tree (map[string]any / []any / string
// / number / bool / nil) and applies fn to every string value
// reachable along a path matching one of textFieldKeys. The path is
// the bag-of-keys we encountered descending into the tree.
//
// Matching is permissive: if any segment of the descent path is one
// of the textFieldKeys, the leaf string is treated as user text.
// This catches deeply-nested variants — e.g. Anthropic's
// `messages[].content[].text` — without needing to enumerate every
// shape in the API surface.
//
// Mutates v in place when v is a composite. For top-level strings the
// caller is responsible (we can't reassign through the any
// parameter).
func walkJSON(v any, textKeys map[string]bool, fn func(string) string) any {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if textKeys[k] {
				if s, ok := child.(string); ok {
					t[k] = fn(s)
					continue
				}
			}
			t[k] = walkJSON(child, textKeys, fn)
		}
		return t
	case []any:
		for i, child := range t {
			t[i] = walkJSON(child, textKeys, fn)
		}
		return t
	default:
		return v
	}
}

// rewriteJSONBody is the common scaffolding behind every protocol's
// RewriteRequest. Decodes JSON, walks it via walkJSON, re-marshals.
// Falls back to returning the original body untouched if the body
// isn't valid JSON (the proxy must never break a request that
// happens not to be JSON — e.g. a multipart upload). The detector
// can't process non-JSON anyway in this path; non-JSON requests pass
// through unchanged.
func rewriteJSONBody(body []byte, textKeys map[string]bool, detectors []Detector, m *Mapping) ([]byte, error) {
	if len(body) == 0 {
		return body, nil
	}
	// Cheap pre-check: skip the parse cost when the body has no
	// JSON-object opening or is obviously binary. Saves work on
	// multipart bodies the proxy forwards as-is.
	trimmed := strings.TrimSpace(string(body))
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return body, nil
	}
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		// Pass through unchanged. The upstream will produce the
		// right HTTP-level error if the JSON was actually malformed
		// from the SDK's perspective.
		return body, nil //nolint:nilerr
	}
	walkJSON(root, textKeys, func(s string) string {
		return ScanAndReplaceText(s, detectors, m)
	})
	// Re-marshal WITHOUT HTML-escaping (`<` → `<`). The
	// placeholder syntax uses literal `<<` and `>>` brackets; if the
	// outbound body encodes those as `<<`, the upstream's
	// prompt-cache key — which is computed from the raw request
	// bytes — rotates on every byte-equivalent-but-textually-different
	// representation, defeating the stable-mapping property the spec
	// promises. json.Encoder.SetEscapeHTML(false) keeps `<` literal.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(root); err != nil {
		return nil, fmt.Errorf("re-marshal rewritten body: %w", err)
	}
	// json.Encoder appends a trailing newline; the upstream's HTTP
	// stack handles it fine, but we strip it to keep the body
	// byte-for-byte minimal (and to match the no-rewrite passthrough
	// shape, where the body comes off the wire without a trailing
	// newline).
	out := buf.Bytes()
	if n := len(out); n > 0 && out[n-1] == '\n' {
		out = out[:n-1]
	}
	return out, nil
}
