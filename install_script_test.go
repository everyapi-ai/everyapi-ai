package main

import (
	"os"
	"strings"
	"testing"
)

func TestInstallScriptMissingCosignMessage(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatalf("read install.sh: %v", err)
	}
	script := string(raw)

	wants := []string{
		`info "publisher verification skipped: cosign is not installed (this is not a signature failure)"`,
		`info "SHA256 integrity verified; publisher authenticity was not verified"`,
		`info "for publisher verification: brew install cosign"`,
		`bash -s -- --force --require-signature`,
	}
	for _, want := range wants {
		if !strings.Contains(script, want) {
			t.Errorf("install.sh missing %q", want)
		}
	}

	if strings.Contains(script, `warn "cosign not installed`) {
		t.Error("missing optional cosign must not be presented as a failed verification warning")
	}
}
