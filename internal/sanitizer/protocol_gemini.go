package sanitizer

import "strings"

// GeminiProtocol covers Google's Gemini generateContent surface
// (and streamGenerateContent), as proxied through the EveryAPI gateway.
// The path shape is `/v1beta/models/{model}:{method}` where method is
// `generateContent`, `streamGenerateContent`, or `embedContent`.
//
// Sanitisable text fields:
//
//   - contents[].parts[].text         — chat turn text
//   - systemInstruction.parts[].text  — system prompt
//   - tools[].functionDeclarations[].description
//   - tools[].functionDeclarations[].parameters … nested strings
//   - cachedContent (string ref name; not user content but harmless to scan)
//
// Fields that must NOT be rewritten:
//
//   - generationConfig.* (temperature, topK, topP, maxOutputTokens, candidateCount)
//   - safetySettings.*
//   - contents[].parts[].inlineData (base64-encoded bytes — substituting
//     into base64 corrupts the binary). We don't touch `data` or
//     `inlineData` keys.
//   - contents[].parts[].fileData (URI references)
type GeminiProtocol struct{}

func (p *GeminiProtocol) Name() string { return "gemini" }

func (p *GeminiProtocol) PathMatch(path string) bool {
	// Match the generateContent surfaces.
	return strings.HasPrefix(path, "/v1beta/models/") || strings.HasPrefix(path, "/v1/models/")
}

// geminiTextKeys captures Gemini's text-bearing keys. `text` overlaps
// with the other protocols but the key set still lives separately for
// clarity. Deliberately omits `data` and `inlineData` so binary
// base64 payloads pass through verbatim.
var geminiTextKeys = map[string]bool{
	"text":        true, // parts[].text
	"description": true, // functionDeclarations[].description, schema descriptions
	"name":        true, // tool names
}

func (p *GeminiProtocol) RewriteRequest(body []byte, detectors []Detector, m *Mapping) ([]byte, error) {
	return rewriteJSONBody(body, geminiTextKeys, detectors, m)
}
