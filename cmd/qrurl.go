package cmd

import "net/url"

// buildVerificationURLWithCode glues a user_code onto the
// verification_uri returned by /api/cli/device-auth-start. The
// dashboard's /cli/auth page reads `?code=` (see web/.../cli/auth.tsx
// validateSearch) and auto-fills the input — meaning a phone QR scan
// goes straight to the confirm screen instead of asking the user to
// retype the 8-character code.
//
// Edge cases:
//   - existing query string on the verification_uri (e.g. UTM tags)
//     is preserved; we only Set the `code` key
//   - on parse failure we fall back to the bare URI rather than
//     returning an error; renderers prefer "show the URL the user
//     can still type" over "crash on a server-side anomaly"
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
