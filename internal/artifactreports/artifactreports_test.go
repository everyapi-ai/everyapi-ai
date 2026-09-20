package artifactreports

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/everyapi-ai/everyapi-sdk/config"
)

// selfServer stands in for the gateway. `body` is written verbatim so a test can send a payload with the
// field absent, which is what a deployment older than the setting actually returns.
func selfServer(t *testing.T, body string, calls *int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls != nil {
			*calls++
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

// testAccountID is the account every login below signs in as.
const testAccountID = 1

// login points the CLI's config dir at a temp dir and stores a usable management session for apiBase.
func login(t *testing.T, apiBase string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	signIn(t, apiBase, testAccountID)
}

// signIn overwrites the active credentials without moving the config dir — what `everyapi auth accounts
// switch` does to a machine that already has a settings.json.
func signIn(t *testing.T, apiBase string, accountID int) {
	t.Helper()
	if err := config.Save(&config.Credentials{
		APIBase:     apiBase,
		AccessToken: "token",
		UserID:      accountID,
		RelayKey:    "sk-everyapi-test",
	}); err != nil {
		t.Fatal(err)
	}
}

func loadSettings(t *testing.T) *config.Settings {
	t.Helper()
	settings, err := config.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	return settings
}

func TestRefreshCachesTheAccountValue(t *testing.T) {
	calls := 0
	server := selfServer(t, `{"success":true,"data":{"id":1,"artifact_reports":false}}`, &calls)
	login(t, server.URL)

	Refresh(context.Background())

	if calls != 1 {
		t.Fatalf("gateway calls = %d, want 1", calls)
	}
	settings := loadSettings(t)
	if settings.ArtifactReportsEnabled() {
		t.Error("the account said off and the cache still reads on")
	}
	if !settings.ArtifactReportsFresh(time.Now(), testAccountID) {
		t.Error("a just-synced cache is not fresh")
	}
	if Enabled() {
		t.Error("Enabled did not read back the cached value")
	}
}

// The TTL is the whole reason a launch does not pay for a round-trip every time. Without this the
// refresh would be correct and still unshippable.
func TestRefreshSkipsTheGatewayWhileTheCacheIsFresh(t *testing.T) {
	calls := 0
	server := selfServer(t, `{"success":true,"data":{"id":1,"artifact_reports":true}}`, &calls)
	login(t, server.URL)

	settings := loadSettings(t)
	settings.SetArtifactReportsCache(false, testAccountID, time.Now())
	if err := config.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}

	Refresh(context.Background())

	if calls != 0 {
		t.Fatalf("gateway calls = %d, want 0 while the cache is fresh", calls)
	}
	if Enabled() {
		t.Error("a fresh cache was overwritten")
	}
}

func TestRefreshExpiresTheCache(t *testing.T) {
	calls := 0
	server := selfServer(t, `{"success":true,"data":{"id":1,"artifact_reports":true}}`, &calls)
	login(t, server.URL)

	settings := loadSettings(t)
	settings.SetArtifactReportsCache(false, testAccountID, time.Now().Add(-2*config.ArtifactReportsCacheTTL))
	if err := config.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}

	Refresh(context.Background())

	if calls != 1 {
		t.Fatalf("gateway calls = %d, want 1 once the cache has expired", calls)
	}
	if !Enabled() {
		t.Error("an expired cache was not replaced by the account value")
	}
}

// Every failure has to leave the last known answer in place. A launch that cannot reach the gateway must
// behave exactly as the previous one did, not revert to the default and start publishing again.
func TestRefreshKeepsTheCacheWhenTheAccountCannotAnswer(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		down bool
	}{
		{name: "gateway unreachable", down: true},
		{name: "envelope rejection", body: `{"success":false,"message":"unauthorized"}`},
		{name: "malformed payload", body: `not json`},
		// A gateway older than the field sends no artifact_reports at all. Decoding that as false would
		// switch reports off for every user of an un-upgraded deployment.
		{name: "field absent", body: `{"success":true,"data":{"id":1}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := "http://127.0.0.1:1"
			if !tc.down {
				base = selfServer(t, tc.body, nil).URL
			}
			login(t, base)

			settings := loadSettings(t)
			settings.SetArtifactReportsCache(false, testAccountID, time.Now().Add(-2*config.ArtifactReportsCacheTTL))
			if err := config.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}

			Refresh(context.Background())

			if Enabled() {
				t.Error("a failed refresh discarded the cached opt-out")
			}
		})
	}
}

func TestRefreshDoesNothingWithoutASession(t *testing.T) {
	calls := 0
	selfServer(t, `{"success":true,"data":{"id":1,"artifact_reports":false}}`, &calls)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	Refresh(context.Background())

	if calls != 0 {
		t.Fatalf("gateway calls = %d, want 0 when logged out", calls)
	}
	if !Enabled() {
		t.Error("a logged-out machine must keep the shipped default")
	}
}

func TestSetWritesTheAccountAndTheCache(t *testing.T) {
	var method, path string
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	t.Cleanup(server.Close)
	login(t, server.URL)

	if err := Set(context.Background(), false); err != nil {
		t.Fatal(err)
	}

	if method != http.MethodPut || path != "/api/user/self" {
		t.Errorf("request = %s %s, want PUT /api/user/self", method, path)
	}
	if string(body) != `{"artifact_reports":false}` {
		t.Errorf("request body = %s", body)
	}
	if Enabled() {
		t.Error("Set did not update the local cache")
	}
	if !loadSettings(t).ArtifactReportsFresh(time.Now(), testAccountID) {
		t.Error("Set left the cache without a sync timestamp, so the next launch would re-fetch it")
	}
}

// Set is the path where silence is the wrong answer: the user asked for an account-wide outcome, and a
// local-only write would leave this machine quietly disagreeing with the page they just looked at.
func TestSetReportsAMissingSession(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := Set(context.Background(), false); err != ErrNoSession {
		t.Fatalf("Set without credentials = %v, want ErrNoSession", err)
	}
}

func TestSetSurfacesAGatewayRejection(t *testing.T) {
	server := selfServer(t, `{"success":false,"message":"nope"}`, nil)
	login(t, server.URL)

	if err := Set(context.Background(), false); err == nil {
		t.Fatal("a rejected write reported success")
	}
	if !Enabled() {
		t.Error("a rejected write still moved the local cache")
	}
}

// settings.json is per-machine and `everyapi auth accounts switch` does not touch it, so a cached switch
// has to remember whose it is. Without that, signing in as somebody else inherits their predecessor's
// decision for the rest of the TTL — silently, and in the direction that suppresses reports.
func TestCacheDoesNotCrossAccounts(t *testing.T) {
	calls := 0
	server := selfServer(t, `{"success":true,"data":{"id":2,"artifact_reports":true}}`, &calls)
	login(t, server.URL)

	settings := loadSettings(t)
	settings.SetArtifactReportsCache(false, testAccountID, time.Now())
	if err := config.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if Enabled() {
		t.Fatal("the signed-in account's own cached opt-out was ignored")
	}

	// Same machine, same settings.json, different account.
	signIn(t, server.URL, testAccountID+1)

	if !Enabled() {
		t.Error("the new account inherited the previous account's opt-out")
	}
	Refresh(context.Background())
	if calls != 1 {
		t.Fatalf("gateway calls = %d, want 1: another account's cache must not count as fresh", calls)
	}
	if !Enabled() {
		t.Error("the refreshed value for the new account was not used")
	}
}

// An OAuth2 relay-key login has no management session, so it can neither read nor write this switch. It
// must fall back to the shipped default rather than to whatever the last full session left behind.
func TestRelayKeyOnlyLoginFallsBackToTheDefault(t *testing.T) {
	login(t, "http://127.0.0.1:1")
	settings := loadSettings(t)
	settings.SetArtifactReportsCache(false, testAccountID, time.Now())
	if err := config.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}

	if err := config.Save(&config.Credentials{
		APIBase:       "http://127.0.0.1:1",
		AccessToken:   "relay-key",
		OAuthClientID: "client",
	}); err != nil {
		t.Fatal(err)
	}

	if !Enabled() {
		t.Error("a relay-key login inherited a full session's cached opt-out")
	}
	if err := Set(context.Background(), false); err != ErrNoSession {
		t.Errorf("Set on a relay-key login = %v, want ErrNoSession", err)
	}
}
