package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/styletest"
	"github.com/everyapi-ai/everyapi-sdk/config"
	"github.com/muesli/termenv"
)

// TestTopupBoldsValues asserts the anti-phishing handshake values — the jump URL and, most importantly, the verification phrase the user must compare against the dashboard — render bold on a styled terminal and strip to plain text when piped (so the phrase is still copy-pasteable out of a redirected `everyapi topup`).
func TestTopupBoldsValues(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/cli/jump-session" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data": map[string]any{
					"session_id":          "SESS123",
					"verification_phrase": "PURPLE-TIGER-42",
					"expires_in":          300,
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
	}))
	defer srv.Close()

	if err := config.Save(&config.Credentials{
		APIBase:     srv.URL,
		AccessToken: "tok",
		UserID:      1,
		Username:    "tony",
	}); err != nil {
		t.Fatal(err)
	}

	// Deterministic EOF on stdin so the confirm gate proceeds without a TTY (Topup treats EOF as "yes" for scripted invocations).
	pr, pw, _ := os.Pipe()
	_ = pw.Close()
	origStdin := os.Stdin
	os.Stdin = pr
	t.Cleanup(func() { os.Stdin = origStdin })

	origLang := i18n.Language()
	i18n.SetLanguage("en")
	t.Cleanup(func() { i18n.SetLanguage(origLang) })

	origOut := cliout.Out
	var buf bytes.Buffer
	cliout.Out = &buf
	t.Cleanup(func() { cliout.Out = origOut })

	t.Run("styled terminal bolds url and phrase", func(t *testing.T) {
		buf.Reset()
		styletest.WithColorProfile(t, termenv.TrueColor)
		if err := Topup([]string{"--no-browser"}); err != nil {
			t.Fatalf("Topup: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "\x1b[1mPURPLE-TIGER-42\x1b[22m") {
			t.Errorf("topup output missing bold verification phrase\n--- output ---\n%s", out)
		}
		// The jump URL is deliberately NOT bolded.
		if strings.Contains(out, "jump_session=SESS123\x1b[22m") {
			t.Errorf("topup URL should not be bolded\n--- output ---\n%s", out)
		}
	})

	t.Run("piped output is plain", func(t *testing.T) {
		buf.Reset()
		// Reset stdin pipe — the first run consumed it to EOF already, but a fresh closed pipe keeps the second run deterministic.
		pr2, pw2, _ := os.Pipe()
		_ = pw2.Close()
		os.Stdin = pr2
		styletest.WithColorProfile(t, termenv.Ascii)
		if err := Topup([]string{"--no-browser"}); err != nil {
			t.Fatalf("Topup: %v", err)
		}
		out := buf.String()
		if strings.Contains(out, "\x1b[") {
			t.Errorf("piped topup output contains ANSI escapes:\n%q", out)
		}
		if !strings.Contains(out, "PURPLE-TIGER-42") {
			t.Errorf("piped topup output missing plain phrase:\n%s", out)
		}
	})
}

// The verification phrase and session id are whatever the gateway says they are — an opt-in `api_base` points the CLI at a third-party or self-hosted deployment, and cliout.Sanitize's contract treats every backend-relayed value as untrusted. This screen is the one place where that matters most: its stated job is display integrity, so a CSI sequence smuggled through the phrase can erase and rewrite the URL line printed just above it, and an OSC-52 payload in the session id writes the reader's clipboard behind the "copy the URL by hand" path.
//
// Asserting on the specific hostile sequences rather than "no ESC anywhere" is deliberate: style.Bold legitimately emits \x1b[1m/\x1b[22m under a styled profile, so a blanket no-escape check would fail on correct code.
func TestTopupSanitizesServerStrings(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	// CSI cursor-up + carriage return + erase-line rewrites the URL line one row above; OSC-52 writes the clipboard.
	const hostilePhrase = "GOOD\x1b[1A\r\x1b[2K  URL      https://evil.example/wallet\x1b[1B"
	const hostileSession = "abc\x1b]52;c;ZXZpbA==\x07def"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/cli/jump-session" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data": map[string]any{
					"session_id":          hostileSession,
					"verification_phrase": hostilePhrase,
					"expires_in":          300,
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
	}))
	defer srv.Close()

	if err := config.Save(&config.Credentials{
		APIBase:     srv.URL,
		AccessToken: "tok",
		UserID:      1,
		Username:    "tony",
	}); err != nil {
		t.Fatal(err)
	}

	pr, pw, _ := os.Pipe()
	_ = pw.Close()
	origStdin := os.Stdin
	os.Stdin = pr
	t.Cleanup(func() { os.Stdin = origStdin })

	origLang := i18n.Language()
	i18n.SetLanguage("en")
	t.Cleanup(func() { i18n.SetLanguage(origLang) })

	origOut := cliout.Out
	var buf bytes.Buffer
	cliout.Out = &buf
	t.Cleanup(func() { cliout.Out = origOut })

	styletest.WithColorProfile(t, termenv.TrueColor)
	if err := Topup([]string{"--no-browser"}); err != nil {
		t.Fatalf("Topup: %v", err)
	}
	out := buf.String()

	for _, seq := range []string{"\x1b[1A", "\x1b[2K", "\x1b[1B", "\x1b]52;", "\x07"} {
		if strings.Contains(out, seq) {
			t.Errorf("gateway-supplied escape %q reached the terminal:\n%q", seq, out)
		}
	}
	// The inert text must survive: sanitizing is not censoring, and the user still has to be able to compare the phrase against the dashboard.
	if !strings.Contains(out, "\x1b[1mGOOD") {
		t.Errorf("sanitized phrase lost its trusted bold or its text:\n%q", out)
	}
	if !strings.Contains(out, "jump_session=abcdef") {
		t.Errorf("sanitized session id lost its inert characters:\n%q", out)
	}
}
