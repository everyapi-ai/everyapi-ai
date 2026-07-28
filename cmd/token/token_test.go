package token

import (
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

// TestRunCreateRequiresQuota locks the boundary guard: `token create`
// with neither --unlimited nor a positive --quota must be refused
// before any network call, so we never mint an enabled 0-quota token
// that `everyapi use` would select and then fail every relay on.
func TestRunCreateRequiresQuota(t *testing.T) {
	err := runCreate([]string{"--name", "x"})
	if err == nil {
		t.Fatal("expected an error when neither --quota nor --unlimited is set")
	}
	if !strings.Contains(err.Error(), "quota") {
		t.Errorf("error should mention quota, got: %v", err)
	}
}

func TestClearCachedRelayKeyKeepsExplicitUnrelatedSelection(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	creds := &config.Credentials{
		APIBase:         "https://api.example.com",
		RelayKey:        "sk-everyapi-selected",
		RelayKeyTokenID: 7,
	}
	if err := config.Save(creds); err != nil {
		t.Fatal(err)
	}

	clearCachedRelayKey(creds, 8)
	if creds.RelayKey != "sk-everyapi-selected" || creds.RelayKeyTokenID != 7 {
		t.Fatalf("unrelated token invalidated explicit selection: %+v", creds)
	}
}

func TestRunRecognizesSwitch(t *testing.T) {
	err := Run([]string{"switch", "unexpected"})
	if err == nil || !strings.Contains(err.Error(), "unexpected positional arguments") {
		t.Fatalf("error = %v, want switch argument validation", err)
	}
}

func TestEnabledTokenChoicesFiltersAndSelectsCurrent(t *testing.T) {
	toks := []api.TokenSummary{
		{ID: 1, Name: "disabled", Status: api.TokenStatusDisabled},
		{ID: 2, Name: "prod", Status: api.TokenStatusEnabled, Group: "paid"},
		{ID: 3, Name: "default", Status: api.TokenStatusEnabled},
	}
	enabled, labels, initial := enabledTokenChoices(toks, 3)
	if len(enabled) != 2 || enabled[0].ID != 2 || enabled[1].ID != 3 {
		t.Fatalf("enabled choices = %+v", enabled)
	}
	if len(labels) != 2 || strings.Contains(strings.Join(labels, " "), "disabled") {
		t.Fatalf("labels include unusable key: %q", labels)
	}
	if initial != 1 || !strings.Contains(labels[1], "current") {
		t.Fatalf("current selection: initial=%d labels=%q", initial, labels)
	}
}

func TestEnabledTokenChoicesSanitizesTUILabels(t *testing.T) {
	toks := []api.TokenSummary{{
		ID: 4, Name: "prod\n\x1b[31mspoof", Group: "paid\rgroup", Status: api.TokenStatusEnabled,
	}}
	_, labels, _ := enabledTokenChoices(toks, 0)
	if len(labels) != 1 {
		t.Fatalf("labels = %q", labels)
	}
	if strings.ContainsAny(labels[0], "\r\n\x1b") {
		t.Fatalf("unsafe terminal text in picker label: %q", labels[0])
	}
}

func TestExpiresValue(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		def     int64
		want    int64
		wantErr bool
	}{
		{"empty → default", "", 1234, 1234, false},
		{"empty → default (never sentinel)", "", api.TokenExpiresNever, api.TokenExpiresNever, false},
		{"literal never", "never", 0, api.TokenExpiresNever, false},
		{"absolute unix seconds", "1700000000", 0, 1700000000, false},
		{"garbage", "tomorrow", 0, 0, true},
		{"negative ints stay verbatim — backend treats -1 as 'never'", "-1", 0, -1, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := expiresValue(c.in, c.def)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, c.wantErr)
			}
			if !c.wantErr && got != c.want {
				t.Errorf("got %d, want %d", got, c.want)
			}
		})
	}
}

func TestParseID(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		verb    string
		wantID  int
		wantErr bool
	}{
		{"plain id", []string{"42"}, "show", 42, false},
		{"id + extra flags pass through", []string{"42", "--name", "x"}, "update", 42, false},
		{"empty", nil, "show", 0, true},
		{"zero is rejected (no real token has id 0)", []string{"0"}, "show", 0, true},
		{"negative", []string{"-1"}, "show", 0, true},
		{"not a number", []string{"abc"}, "show", 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _, err := parseID(c.args, c.verb)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, c.wantErr)
			}
			if !c.wantErr && got != c.wantID {
				t.Errorf("got id %d, want %d", got, c.wantID)
			}
		})
	}
}

func TestStatusLabel(t *testing.T) {
	cases := map[int]string{
		api.TokenStatusEnabled:   "enabled",
		api.TokenStatusDisabled:  "disabled",
		api.TokenStatusExpired:   "expired",
		api.TokenStatusExhausted: "exhausted",
		99:                       "status=99",
	}
	for in, want := range cases {
		if got := statusLabel(in); got != want {
			t.Errorf("statusLabel(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestExpiresLabel(t *testing.T) {
	if got := expiresLabel(api.TokenExpiresNever); got != "never" {
		t.Errorf("never sentinel: got %q, want %q", got, "never")
	}
	// Concrete timestamp: 2023-11-14 22:13:20 UTC. Don't pin the
	// formatted string because expiresLabel uses local-time
	// formatting (intentional for the dashboard-style UX) and CI
	// timezones differ — just assert non-empty + non-"never".
	if got := expiresLabel(1700000000); got == "" || got == "never" {
		t.Errorf("concrete timestamp produced %q", got)
	}
}

func TestIDSubcommandHelpDoesNotParseHelpAsID(t *testing.T) {
	for _, sub := range []string{"show", "key", "revoke", "enable", "disable", "usage"} {
		t.Run(sub, func(t *testing.T) {
			if err := Run([]string{sub, "--help"}); err != nil {
				t.Fatalf("token %s --help returned error: %v", sub, err)
			}
		})
	}
}
