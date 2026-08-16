package cliprompt

import "testing"

// TestOpenBrowserRejectsUntrustedURL pins the guard that stops a server-controlled verification_uri from being handed to open/xdg-open as an option flag or a non-http target. Only the rejection path is asserted — it returns before any exec, so it's deterministic; the happy path would actually shell out to a browser launcher.
func TestOpenBrowserRejectsUntrustedURL(t *testing.T) {
	bad := []string{
		"-a",                  // leading dash → option injection into open
		"-x",                  // any leading dash
		"file:///etc/passwd",  // non-http scheme
		"javascript:alert(1)", // non-http scheme
		"ftp://example.com/x", // non-http scheme
		"//example.com",       // no scheme
		"not a url",           // unparseable-as-absolute
		"https://",            // no host
		"",                    // empty
	}
	for _, u := range bad {
		if err := OpenBrowser(u); err == nil {
			t.Errorf("OpenBrowser(%q) = nil, want rejection error", u)
		}
	}
}
