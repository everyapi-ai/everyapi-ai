package cmd

import (
	"net/url"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliprompt"
)

// isDisplayableURL reports whether s is a well-formed absolute http(s) URL that is safe to feed to the QR renderer, the system clipboard, or the OS "open URL" helper. The verification_uri is server-controlled, so a malicious/compromised gateway must not be able to smuggle control bytes (terminal-escape injection into the QR/print sinks) or a leading '-' (option injection into open/xdg-open) into those sinks. Display of the URL text still goes through cliout.Sanitize regardless.
//
// The http(s)/host/leading-dash shape check is shared with OpenBrowser via cliprompt.IsBrowsableURL so the two sinks can't diverge; the explicit control-byte scan here documents the extra display-sink concern (url.Parse already rejects control bytes, so IsBrowsableURL would reject them too — this just makes the intent legible).
func isDisplayableURL(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c == 0x7f {
			return false
		}
	}
	return cliprompt.IsBrowsableURL(s)
}

// buildVerificationURLWithCode glues a user_code onto the verification_uri returned by /api/cli/device-auth-start. The dashboard's /cli/auth page reads `?code=` (see web/.../cli/auth.tsx validateSearch) and auto-fills the input — meaning a phone QR scan goes straight to the confirm screen instead of asking the user to retype the 8-character code.
//
// Edge cases:
//   - existing query string on the verification_uri (e.g. UTM tags) is preserved; we only Set the `code` key
//   - on parse failure we fall back to the bare URI rather than returning an error; renderers prefer "show the URL the user can still type" over "crash on a server-side anomaly"
func buildVerificationURLWithCode(verificationURI, userCode string) string {
	if verificationURI == "" || userCode == "" {
		return verificationURI
	}
	u, err := url.Parse(verificationURI)
	if err != nil {
		return verificationURI
	}
	q := u.Query()
	q.Set("code", userCode)
	u.RawQuery = q.Encode()
	return u.String()
}
