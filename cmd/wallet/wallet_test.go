package wallet

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/internal/styletest"
	"github.com/everyapi-ai/everyapi-sdk/config"
	"github.com/muesli/termenv"
)

// styledRedeemed bolds the credited quota in the wallet.redeemed line
// (the amount is **-marked in every locale and routed through Emph) and
// strips to plain text when piped.
func TestStyledRedeemed(t *testing.T) {
	orig := i18n.Language()
	i18n.SetLanguage("en")
	t.Cleanup(func() { i18n.SetLanguage(orig) })

	t.Run("styled terminal bolds the quota", func(t *testing.T) {
		styletest.WithColorProfile(t, termenv.TrueColor)
		got := styledRedeemed(50)
		if !strings.Contains(got, "\x1b[1m50\x1b[22m") {
			t.Errorf("styledRedeemed(50) = %q, want bold quota", got)
		}
		if strings.Contains(got, "**") {
			t.Errorf("styledRedeemed(50) leaked literal markers: %q", got)
		}
	})

	t.Run("piped output is plain", func(t *testing.T) {
		styletest.WithColorProfile(t, termenv.Ascii)
		got := styledRedeemed(50)
		if got != "Redeemed: +50 quota credited." {
			t.Errorf("styledRedeemed(50) = %q, want plain", got)
		}
	})
}

// TestWalletBoldsValues drives `wallet history` and `wallet info` and
// asserts the scannable values — the per-row money figure and the
// dashboard top-up URL — render bold on a styled terminal and strip to
// plain text when piped.
func TestWalletBoldsValues(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/user/topup/self"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data": map[string]any{
					"items": []map[string]any{{
						"id": 9, "amount": 5000, "money": 12.5,
						"trade_no": "TR-1", "payment_method": "stripe",
						"create_time": 1700000000, "status": "success",
					}},
					"total": 1,
				},
			})
		case r.URL.Path == "/api/user/topup/info":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data":    map[string]any{"topup_link": "https://app.everyapi.ai/topup"},
			})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
		}
	}))
	defer srv.Close()

	if err := config.Save(&config.Credentials{
		APIBase: srv.URL, AccessToken: "tok", UserID: 1, Username: "tony",
	}); err != nil {
		t.Fatal(err)
	}

	origLang := i18n.Language()
	i18n.SetLanguage("en")
	t.Cleanup(func() { i18n.SetLanguage(origLang) })

	origOut := cliout.Out
	var buf bytes.Buffer
	cliout.Out = &buf
	t.Cleanup(func() { cliout.Out = origOut })

	run := func(t *testing.T, args ...string) string {
		t.Helper()
		buf.Reset()
		if err := Run(args); err != nil {
			t.Fatalf("wallet %v: %v", args, err)
		}
		return buf.String()
	}

	t.Run("history bolds the money figure", func(t *testing.T) {
		styletest.WithColorProfile(t, termenv.TrueColor)
		out := run(t, "history")
		if !strings.Contains(out, "money=\x1b[1m12.5\x1b[22m") {
			t.Errorf("history output missing bold money figure:\n%s", out)
		}
	})

	t.Run("info shows the dashboard url unbolded", func(t *testing.T) {
		styletest.WithColorProfile(t, termenv.TrueColor)
		out := run(t, "info")
		if !strings.Contains(out, "https://app.everyapi.ai/topup") {
			t.Errorf("info output missing dashboard url:\n%s", out)
		}
		// URLs are deliberately not bolded.
		if strings.Contains(out, "\x1b[1mhttps://app.everyapi.ai/topup") {
			t.Errorf("dashboard url should not be bolded:\n%s", out)
		}
	})

	t.Run("piped history is plain", func(t *testing.T) {
		styletest.WithColorProfile(t, termenv.Ascii)
		out := run(t, "history")
		if strings.Contains(out, "\x1b[") {
			t.Errorf("piped history output contains ANSI escapes:\n%q", out)
		}
		if !strings.Contains(out, "money=12.5") {
			t.Errorf("piped history output missing plain money figure:\n%s", out)
		}
	})
}
