package tools

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
)

// Codex offers its built-in image tool only to providers that send a non-empty x-openai-actor-authorization header (codex-rs/core/src/tools/spec_plan.rs). Both launch modes must carry it, alongside the agent attribution header the injected provider already sends.
func TestCodexProvidersEnableTheImageTool(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider string
		prepare  func() error
		want     map[string]string
	}{
		{
			name:     "injected",
			provider: "everyapi",
			prepare:  func() error { _, err := prepareCodex("https://api.everyapi.ai", "unused"); return err },
			want:     map[string]string{"X-EveryAPI-Agent": "codex", "x-openai-actor-authorization": "everyapi"},
		},
		{
			name:     "transparent",
			provider: "everyapi_openai",
			prepare:  func() error { _, err := prepareCodexTransparent(); return err },
			want:     map[string]string{"x-openai-actor-authorization": "everyapi"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, codexHome := codexTestHome(t)
			if err := tc.prepare(); err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(filepath.Join(codexHome, "config.toml"))
			if err != nil {
				t.Fatal(err)
			}
			var generated struct {
				ModelProviders map[string]struct {
					HTTPHeaders map[string]string `toml:"http_headers"`
				} `toml:"model_providers"`
			}
			if _, err := toml.Decode(string(body), &generated); err != nil {
				t.Fatalf("generated config.toml is invalid: %v\n%s", err, body)
			}
			got := generated.ModelProviders[tc.provider].HTTPHeaders
			if len(got) != len(tc.want) {
				t.Fatalf("http_headers = %v, want %v", got, tc.want)
			}
			for name, value := range tc.want {
				if got[name] != value {
					t.Errorf("http_headers[%q] = %q, want %q", name, got[name], value)
				}
			}
		})
	}
}
