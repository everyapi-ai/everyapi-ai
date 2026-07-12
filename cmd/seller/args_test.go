package seller

import (
	"strings"
	"testing"
)

func TestWriteCommandsRejectExtraPositionalsBeforeAPI(t *testing.T) {
	oauthArgs := []string{"--name", "n", "--models", "m", "extra"}
	cases := map[string]func() error{
		"update":   func() error { return sellerUpdate([]string{"1", "--name", "n", "extra"}) },
		"remove":   func() error { return sellerRemove([]string{"1", "-y", "extra"}) },
		"refresh":  func() error { return sellerRefresh([]string{"1", "--kind", "codex", "extra"}) },
		"withdraw": func() error { return sellerWithdraw([]string{"--quota", "1", "extra"}) },
		"add key": func() error {
			_, err := parseAddKeyArgs([]string{"--type", "openai", "--name", "n", "--key", "k", "--models", "m", "extra"})
			return err
		},
		"setup": func() error { return sellerSetup([]string{"extra"}) },
		"compensation": func() error {
			return sellerCompensationSubmit([]string{"--upstream", "x", "--description", "d", "extra"})
		},
		"oauth codex":       func() error { return sellerAddOAuthCodex(oauthArgs) },
		"oauth claude":      func() error { return sellerAddOAuthClaude(oauthArgs) },
		"oauth gemini":      func() error { return sellerAddOAuthGemini(oauthArgs) },
		"oauth antigravity": func() error { return sellerAddOAuthAntigravity(oauthArgs) },
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			err := run()
			if err == nil || !strings.Contains(err.Error(), "unexpected positional arguments") {
				t.Fatalf("error = %v, want positional-argument rejection", err)
			}
		})
	}
}
