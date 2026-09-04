package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInjectedCodexProviderEnablesStandaloneWebSearch(t *testing.T) {
	_, codexHome := codexTestHome(t)
	if _, err := prepareCodex("https://api.everyapi.ai", "unused"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(codexHome, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "supports_standalone_web_search = true") {
		t.Fatalf("generated provider does not enable standalone web search:\n%s", body)
	}
	if !strings.Contains(string(body), "supports_websockets = false") {
		t.Fatalf("generated provider does not pin HTTP Responses transport:\n%s", body)
	}
}
