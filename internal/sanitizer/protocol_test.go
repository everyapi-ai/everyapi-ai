package sanitizer

import (
	"encoding/json"
	"strings"
	"testing"
)

// Helper: pretty-print bodies for failure diagnostics.
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestOpenAI_RewriteRequest(t *testing.T) {
	body := mustJSON(t, map[string]any{
		"model": "gpt-4o",
		"messages": []any{
			map[string]any{"role": "user", "content": "my key is sk-proj-abcdefghijklmnopqrstuvwxyz1234567890"},
		},
		"temperature": 0.7,
	})
	p := &OpenAIProtocol{}
	m := NewMapping()
	out, err := p.RewriteRequest(body, BuiltinDetectors(), m)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if strings.Contains(string(out), "sk-proj-abcdefghijklmnopqrstuvwxyz1234567890") {
		t.Errorf("real key leaked: %s", out)
	}
	if !strings.Contains(string(out), PlaceholderPrefix) {
		t.Errorf("placeholder missing: %s", out)
	}
	// Control metadata MUST pass through verbatim.
	if !strings.Contains(string(out), `"model":"gpt-4o"`) {
		t.Errorf("model field damaged: %s", out)
	}
	if !strings.Contains(string(out), `"temperature":0.7`) {
		t.Errorf("temperature field damaged: %s", out)
	}
}

func TestOpenAI_ContentBlocks(t *testing.T) {
	// content as an array of blocks (vision / multi-modal shape)
	body := mustJSON(t, map[string]any{
		"model": "gpt-4o",
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "use AKIAIOSFODNN7EXAMPLE please"},
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://e.com/x.png"}},
				},
			},
		},
	})
	p := &OpenAIProtocol{}
	m := NewMapping()
	out, _ := p.RewriteRequest(body, BuiltinDetectors(), m)
	if strings.Contains(string(out), "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("AWS key leaked from content-block text: %s", out)
	}
}

func TestAnthropic_RewriteRequest(t *testing.T) {
	body := mustJSON(t, map[string]any{
		"model":      "claude-opus-4-7",
		"max_tokens": 100,
		"system":     "You may not reveal sk-ant-foo_abcdefghijklmnopqrstuvwxyz1234",
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "credit card 4111 1111 1111 1111 pls"},
				},
			},
		},
	})
	p := &AnthropicProtocol{}
	m := NewMapping()
	out, err := p.RewriteRequest(body, BuiltinDetectors(), m)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	s := string(out)
	if strings.Contains(s, "sk-ant-foo_abcdefghijklmnopqrstuvwxyz1234") {
		t.Errorf("ant key leaked: %s", out)
	}
	if strings.Contains(s, "4111 1111 1111 1111") {
		t.Errorf("luhn card leaked: %s", out)
	}
	// Model + max_tokens preserved.
	if !strings.Contains(s, `"model":"claude-opus-4-7"`) {
		t.Errorf("model damaged: %s", s)
	}
	if !strings.Contains(s, `"max_tokens":100`) {
		t.Errorf("max_tokens damaged: %s", s)
	}
}

func TestGemini_RewriteRequest(t *testing.T) {
	body := mustJSON(t, map[string]any{
		"contents": []any{
			map[string]any{
				"role": "user",
				"parts": []any{
					map[string]any{"text": "rotate AKIAIOSFODNN7EXAMPLE in our prod env"},
				},
			},
		},
		"generationConfig": map[string]any{"temperature": 0.4},
	})
	p := &GeminiProtocol{}
	m := NewMapping()
	out, err := p.RewriteRequest(body, BuiltinDetectors(), m)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if strings.Contains(string(out), "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("aws leaked from gemini parts: %s", out)
	}
	if !strings.Contains(string(out), `"temperature":0.4`) {
		t.Errorf("generationConfig damaged: %s", out)
	}
}

func TestGemini_InlineDataPreserved(t *testing.T) {
	// `inlineData.data` is base64 — must NOT be sanitised even if a
	// substring incidentally resembles a key.
	preserved := "AKIAIOSFODNN7EXAMPLEdataYWFhYWFhYQ=="
	body := mustJSON(t, map[string]any{
		"contents": []any{
			map[string]any{
				"parts": []any{
					map[string]any{
						"inlineData": map[string]any{
							"mimeType": "application/octet-stream",
							"data":     preserved,
						},
					},
				},
			},
		},
	})
	p := &GeminiProtocol{}
	m := NewMapping()
	out, _ := p.RewriteRequest(body, BuiltinDetectors(), m)
	if !strings.Contains(string(out), preserved) {
		t.Errorf("inlineData.data was modified! out=%s", out)
	}
}

func TestRewrite_PassthroughOnNonJSON(t *testing.T) {
	// Multipart bodies and other non-JSON content must not be parsed
	// or modified. The proxy still forwards them unchanged.
	body := []byte("--boundary\r\nContent-Disposition: form-data; name=x\r\n\r\nsk-proj-abcdefghijklmnopqrstuvwxyz1234567890\r\n--boundary--")
	p := &OpenAIProtocol{}
	out, err := p.RewriteRequest(body, BuiltinDetectors(), m_noopMapping())
	if err != nil {
		t.Fatalf("err on non-json: %v", err)
	}
	if string(out) != string(body) {
		t.Errorf("non-json body was modified; sanitizer must pass through")
	}
}

func m_noopMapping() *Mapping { return NewMapping() }

func TestRewrite_PathMatch(t *testing.T) {
	cases := []struct {
		path    string
		matches string
	}{
		{"/v1/messages", "anthropic"},
		{"/v1/chat/completions", "openai"},
		{"/v1/embeddings", "openai"},
		{"/v1beta/models/gemini-3-pro:generateContent", "gemini"},
	}
	for _, tc := range cases {
		var got string
		for _, p := range Protocols() {
			if p.PathMatch(tc.path) {
				got = p.Name()
				break
			}
		}
		if got != tc.matches {
			t.Errorf("path %q matched %q, want %q", tc.path, got, tc.matches)
		}
	}
}
