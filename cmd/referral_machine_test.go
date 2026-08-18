package cmd

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-sdk/config"
	"rsc.io/qr"
)

func TestBuildInviteURLTargetsTheDashboardSignInPage(t *testing.T) {
	for _, tc := range []struct {
		name    string
		apiBase string
		code    string
		want    string
		wantErr bool
	}{
		// The dashboard lives on app.*; the bare domain is the landing page and serves no /signin route.
		{"official", "https://api.everyapi.ai", "AB12CD", "https://app.everyapi.ai/signin?aff=AB12CD", false},
		// The China gateway shares one dashboard host with the global one.
		{"china", "https://api-cn.everyapi.ai", "AB12CD", "https://app.everyapi.ai/signin?aff=AB12CD", false},
		// Self-hosted single binary serves the SPA itself — no host rewrite to invent.
		{"self hosted", "https://gateway.example.com", "AB12CD", "https://gateway.example.com/signin?aff=AB12CD", false},
		// The desktop's invite-card parser only renders https, or http to a loopback host — a plain-http self-hosted base is refused rather than handed a link no consumer accepts.
		{"self hosted http base rejected", "http://api.example.com", "AB12CD", "", true},
		{"local dev", "http://localhost:8787", "AB12CD", "http://localhost:8787/signin?aff=AB12CD", false},
		{"loopback ip", "http://127.0.0.1:8787", "AB12CD", "http://127.0.0.1:8787/signin?aff=AB12CD", false},
		// A trailing slash on the base must not produce "//signin".
		{"trailing slash", "https://api.everyapi.ai/", "AB12CD", "https://app.everyapi.ai/signin?aff=AB12CD", false},
		// A code that needs escaping must not be able to smuggle extra query parameters into the invite link.
		{"escapes the code", "https://api.everyapi.ai", "A&b=c d", "https://app.everyapi.ai/signin?aff=A%26b%3Dc+d", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := buildInviteURL(tc.apiBase, tc.code)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("buildInviteURL(%q, %q): want error, got %q", tc.apiBase, tc.code, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("buildInviteURL(%q, %q): %v", tc.apiBase, tc.code, err)
			}
			if got != tc.want {
				t.Fatalf("buildInviteURL(%q, %q) = %q, want %q", tc.apiBase, tc.code, got, tc.want)
			}
		})
	}
}

func TestReferralMachineRequiresJSONFormat(t *testing.T) {
	if err := ReferralMachine(nil); err == nil {
		t.Fatal("ReferralMachine without --format=json: want error, got nil")
	}
}

func TestReferralMachineReportsSignedOutWithoutError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var out bytes.Buffer
	previous := cliout.Out
	cliout.Out = &out
	t.Cleanup(func() { cliout.Out = previous })
	if err := ReferralMachine([]string{"--format=json"}); err != nil {
		t.Fatalf("ReferralMachine signed out: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["signed_in"] != false {
		t.Fatalf("signed_in = %v, want false", got["signed_in"])
	}
	// A signed-out payload carrying a code or a link would mean the card renders someone else's invite.
	for _, key := range []string{"code", "invite_url", "qr_data", "qr_mime"} {
		if _, present := got[key]; present {
			t.Fatalf("signed-out referral exposed %q: %v", key, got)
		}
	}
}

func TestReferralMachineRendersAScannableInviteCard(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	const accessToken = "management-referral-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/aff":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": "AB12CD"})
		case "/api/status":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data": map[string]any{
					"quota_per_unit":    100.0,
					"quota_for_inviter": 50.0,
					"quota_for_invitee": 25.0,
				},
			})
		case "/api/user/self":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data":    map[string]any{"username": "alice", "aff_count": 3, "aff_quota": 150},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	if err := config.Save(&config.Credentials{
		APIBase:     server.URL,
		AccessToken: accessToken,
		Username:    "alice",
	}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	previous := cliout.Out
	cliout.Out = &out
	t.Cleanup(func() { cliout.Out = previous })
	if err := ReferralMachine([]string{"--format=json"}); err != nil {
		t.Fatalf("ReferralMachine: %v", err)
	}

	var got referralMachineOutput
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != referralMachineProtocolVersion || !got.SignedIn {
		t.Fatalf("version/signed_in = %d/%v", got.Version, got.SignedIn)
	}
	if got.Code != "AB12CD" {
		t.Fatalf("code = %q, want AB12CD", got.Code)
	}
	wantURL := server.URL + "/signin?aff=AB12CD"
	if got.InviteURL != wantURL {
		t.Fatalf("invite_url = %q, want %q", got.InviteURL, wantURL)
	}
	if got.QRMIME != "image/png" {
		t.Fatalf("qr_mime = %q, want image/png", got.QRMIME)
	}
	png, err := base64.StdEncoding.DecodeString(got.QRData)
	if err != nil {
		t.Fatalf("qr_data is not base64: %v", err)
	}
	if !bytes.HasPrefix(png, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("qr_data is not a PNG (first bytes %q)", png[:min(8, len(png))])
	}
	// Re-encoding the reported link must reproduce the reported image: that is what proves the QR carries the invite URL rather than some other string.
	expected, err := qr.Encode(got.InviteURL, referralQRLevel)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(png, expected.PNG()) {
		t.Fatal("qr_data does not encode invite_url")
	}
	if got.InviteCount != 3 {
		t.Fatalf("invite_count = %d, want 3", got.InviteCount)
	}
	if got.PendingRewardUSD != 1.5 {
		t.Fatalf("pending_reward_usd = %v, want 1.5", got.PendingRewardUSD)
	}
	if got.InviterRewardUSD != 0.5 || got.InviteeRewardUSD != 0.25 {
		t.Fatalf("rewards = %v/%v, want 0.5/0.25", got.InviterRewardUSD, got.InviteeRewardUSD)
	}
	if strings.Contains(out.String(), accessToken) {
		t.Fatalf("referral payload leaked access token: %s", out.String())
	}
}

func TestReferralMachineStillRendersWhenCountersAreUnavailable(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/user/aff" {
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": "AB12CD"})
			return
		}
		// The counters and reward rates are decoration; losing them must not cost the user a scannable code.
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	if err := config.Save(&config.Credentials{
		APIBase:     server.URL,
		AccessToken: "token",
		Username:    "alice",
	}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	previous := cliout.Out
	cliout.Out = &out
	t.Cleanup(func() { cliout.Out = previous })
	if err := ReferralMachine([]string{"--format=json"}); err != nil {
		t.Fatalf("ReferralMachine with failing counters: %v", err)
	}

	var got referralMachineOutput
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.InviteURL == "" || got.QRData == "" {
		t.Fatalf("invite card lost its link/QR: %+v", got)
	}
	if got.InviteCount != 0 || got.PendingRewardUSD != 0 || got.InviterRewardUSD != 0 {
		t.Fatalf("unavailable counters should stay zero: %+v", got)
	}
}

func TestReferralMachineClampsCounterValuesTheConsumersWouldReject(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/aff":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": "AB12CD"})
		case "/api/status":
			// A negative inviter rate and an invitee rate past the desktop parser's 1e12 USD ceiling.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data": map[string]any{
					"quota_per_unit":    0.01,
					"quota_for_inviter": -5.0,
					"quota_for_invitee": 1e13,
				},
			})
		case "/api/user/self":
			// A negative invite count and an affiliate balance past the 1e15 count / 1e12 USD ceilings.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data":    map[string]any{"username": "alice", "aff_count": -1, "aff_quota": 10000000000000000},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	if err := config.Save(&config.Credentials{
		APIBase:     server.URL,
		AccessToken: "token",
		Username:    "alice",
	}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	previous := cliout.Out
	cliout.Out = &out
	t.Cleanup(func() { cliout.Out = previous })
	if err := ReferralMachine([]string{"--format=json"}); err != nil {
		t.Fatalf("ReferralMachine with unroutable counter values: %v", err)
	}

	var got referralMachineOutput
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	// A value the consumers would refuse must not take the scannable card down with it.
	if got.InviteURL == "" || got.QRData == "" {
		t.Fatalf("invite card lost its link/QR: %+v", got)
	}
	if got.InviteCount != 0 || got.PendingRewardUSD != 0 || got.InviterRewardUSD != 0 || got.InviteeRewardUSD != 0 {
		t.Fatalf("unroutable counters should clamp to zero: %+v", got)
	}
}

func TestReferralMachineFailsWhenTheAffiliateCodeIsUnusable(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/user/aff" {
			// An account surface that answers with a blank code leaves nothing to invite anyone with.
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": ""})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	if err := config.Save(&config.Credentials{
		APIBase:     server.URL,
		AccessToken: "token",
		Username:    "alice",
	}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	previous := cliout.Out
	cliout.Out = &out
	t.Cleanup(func() { cliout.Out = previous })
	err := ReferralMachine([]string{"--format=json"})
	if err == nil {
		t.Fatal("blank affiliate code: want error, got nil")
	}
	// The desktop switches on this prefix; a plain error string would reach it as a generic failure.
	if !strings.HasPrefix(err.Error(), "EVERYAPI_STATUS_ERROR:") {
		t.Fatalf("error = %q, want a machine status code", err.Error())
	}
}
