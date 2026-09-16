package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

// newAccountsTest isolates the config directory and clears the per-process account pin, which is package state in the SDK and would otherwise leak between tests.
func newAccountsTest(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := config.SelectAccount(""); err != nil {
		t.Fatalf("clear account selection: %v", err)
	}
	t.Cleanup(func() { _ = config.SelectAccount("") })
}

// captureOut redirects the CLI's output writer for the duration of fn.
func captureOut(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	original := cliout.Out
	cliout.Out = &buf
	t.Cleanup(func() { cliout.Out = original })
	fn()
	return buf.String()
}

func testCreds(username string, id int) *config.Credentials {
	return &config.Credentials{APIBase: config.DefaultAPIBase, AccessToken: "tok-" + username, UserID: id, Username: username}
}

func seedActive(t *testing.T, name string, c *config.Credentials) {
	t.Helper()
	if err := config.Save(c); err != nil {
		t.Fatalf("save active credentials: %v", err)
	}
	if err := config.SetActiveAccountName(name); err != nil {
		t.Fatalf("set active account name: %v", err)
	}
}

func seedParked(t *testing.T, name string, c *config.Credentials) {
	t.Helper()
	if err := config.SaveAccount(name, c); err != nil {
		t.Fatalf("save parked account %s: %v", name, err)
	}
}

func TestAccountsListJSONReportsEveryAccountAndNoSecrets(t *testing.T) {
	newAccountsTest(t)
	seedActive(t, "work", testCreds("alice", 1))
	seedParked(t, "personal", testCreds("bob", 2))

	out := ""
	err := error(nil)
	out = captureOut(t, func() { err = Accounts([]string{"list", "--format=json"}) })
	if err != nil {
		t.Fatalf("accounts list: %v", err)
	}
	var rows []accountJSON
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("parse %q: %v", out, err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want two accounts", rows)
	}
	if rows[0].Name != "work" || !rows[0].Active {
		t.Errorf("first row = %+v, want the active work account", rows[0])
	}
	if rows[1].Name != "personal" || rows[1].Active {
		t.Errorf("second row = %+v, want the parked personal account", rows[1])
	}
	if strings.Contains(out, "tok-alice") || strings.Contains(out, "tok-bob") {
		t.Errorf("account listing leaked token material:\n%s", out)
	}
}

func TestAccountsListWithoutAccountsExplainsHowToSignIn(t *testing.T) {
	newAccountsTest(t)
	var err error
	out := captureOut(t, func() { err = Accounts([]string{"list"}) })
	if err != nil {
		t.Fatalf("accounts list: %v", err)
	}
	if !strings.Contains(out, "auth login") {
		t.Errorf("empty listing must point at login, got %q", out)
	}
}

func TestAccountsSwitchMovesTheActiveCredential(t *testing.T) {
	newAccountsTest(t)
	seedActive(t, "work", testCreds("alice", 1))
	seedParked(t, "personal", testCreds("bob", 2))

	var err error
	captureOut(t, func() { err = Accounts([]string{"switch", "personal"}) })
	if err != nil {
		t.Fatalf("accounts switch: %v", err)
	}
	live, err := config.Load()
	if err != nil {
		t.Fatalf("load after switch: %v", err)
	}
	if live.Username != "bob" {
		t.Errorf("active credential = %q, want bob", live.Username)
	}
	parked, err := config.LoadAccount("work")
	if err != nil {
		t.Fatalf("the outgoing account must be kept: %v", err)
	}
	if parked.Username != "alice" {
		t.Errorf("parked credential = %q, want alice", parked.Username)
	}
}

func TestAccountsSwitchRejectsUnknownName(t *testing.T) {
	newAccountsTest(t)
	seedActive(t, "work", testCreds("alice", 1))
	var err error
	captureOut(t, func() { err = Accounts([]string{"switch", "nobody"}) })
	if err == nil {
		t.Fatal("switching to an unknown account succeeded")
	}
	if !strings.Contains(err.Error(), "nobody") {
		t.Errorf("error must name the account, got %v", err)
	}
}

func TestAccountsRenameRefusesAnExistingName(t *testing.T) {
	newAccountsTest(t)
	seedActive(t, "work", testCreds("alice", 1))
	seedParked(t, "personal", testCreds("bob", 2))
	var err error
	captureOut(t, func() { err = Accounts([]string{"rename", "personal", "work"}) })
	if err == nil {
		t.Fatal("rename overwrote an existing account")
	}
	if _, loadErr := config.LoadAccount("personal"); loadErr != nil {
		t.Errorf("the source account must survive a rejected rename: %v", loadErr)
	}
}

// TestAccountsRemoveActiveDoesNotPromoteAnother is the invariant behind refusing to auto-promote: the next `everyapi use` after a sign-out must not relay — and bill — through an account the user never chose. The remaining account survives and is one `accounts switch` away; it just does not become active on its own.
func TestAccountsRemoveActiveDoesNotPromoteAnother(t *testing.T) {
	newAccountsTest(t)
	seedActive(t, "work", testCreds("alice", 1))
	seedParked(t, "personal", testCreds("bob", 2))

	var err error
	out := captureOut(t, func() { err = Accounts([]string{"remove", "work"}) })
	if err != nil {
		t.Fatalf("accounts remove: %v", err)
	}
	if _, err := config.Load(); !errors.Is(err, config.ErrNoCredentials) {
		t.Errorf("Load = %v, want ErrNoCredentials — signing out must leave the machine signed out", err)
	}
	if name, nameErr := config.ActiveAccountName(); nameErr != nil || name != "" {
		t.Errorf("ActiveAccountName = (%q, %v), want empty", name, nameErr)
	}
	kept, err := config.LoadAccount("personal")
	if err != nil {
		t.Fatalf("the other account must survive: %v", err)
	}
	if kept.Username != "bob" {
		t.Errorf("kept credential = %q, want bob", kept.Username)
	}
	if !strings.Contains(out, "accounts switch") {
		t.Errorf("output %q must point at the command that signs back in", out)
	}
}

func TestAccountsRemoveLastAccountLeavesTheMachineSignedOut(t *testing.T) {
	newAccountsTest(t)
	seedActive(t, "work", testCreds("alice", 1))

	var err error
	captureOut(t, func() { err = Accounts([]string{"remove", "work"}) })
	if err != nil {
		t.Fatalf("accounts remove: %v", err)
	}
	if _, err := config.Load(); !errors.Is(err, config.ErrNoCredentials) {
		t.Errorf("Load = %v, want ErrNoCredentials", err)
	}
}

func TestAccountsRemoveParkedLeavesTheActiveAlone(t *testing.T) {
	newAccountsTest(t)
	seedActive(t, "work", testCreds("alice", 1))
	seedParked(t, "personal", testCreds("bob", 2))

	var err error
	captureOut(t, func() { err = Accounts([]string{"remove", "personal"}) })
	if err != nil {
		t.Fatalf("accounts remove: %v", err)
	}
	live, err := config.Load()
	if err != nil {
		t.Fatalf("the active account must survive: %v", err)
	}
	if live.Username != "alice" {
		t.Errorf("active credential = %q, want alice", live.Username)
	}
}

func TestAccountsUnknownSubcommandErrors(t *testing.T) {
	newAccountsTest(t)
	var err error
	captureOut(t, func() { err = Accounts([]string{"frobnicate"}) })
	if err == nil {
		t.Fatal("unknown subcommand succeeded")
	}
}

// TestLogoutAllRemovesEveryAccount covers the escape hatch for a shared machine: one command has to leave nothing behind, not just the account that happens to be active.
func TestLogoutAllRemovesEveryAccount(t *testing.T) {
	newAccountsTest(t)
	seedActive(t, "work", testCreds("alice", 1))
	seedParked(t, "personal", testCreds("bob", 2))

	var err error
	captureOut(t, func() { err = Logout([]string{"--all"}) })
	if err != nil {
		t.Fatalf("logout --all: %v", err)
	}
	infos, err := config.ListAccounts()
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(infos) != 0 {
		t.Errorf("accounts remain after logout --all: %+v", infos)
	}
}

// TestLogoutSignsOutOneAccountAndStaysSignedOut pins the default logout against a multi-account machine: it drops the account in effect, keeps the others on disk, and leaves the machine genuinely signed out. EveryAPI Connect shells out to this command and then tells its user they are signed out — a promotion here would make that a lie, on top of quietly re-pointing billing.
func TestLogoutSignsOutOneAccountAndStaysSignedOut(t *testing.T) {
	newAccountsTest(t)
	seedActive(t, "work", testCreds("alice", 1))
	seedParked(t, "personal", testCreds("bob", 2))

	var err error
	captureOut(t, func() { err = Logout(nil) })
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := config.Load(); !errors.Is(err, config.ErrNoCredentials) {
		t.Errorf("Load = %v, want ErrNoCredentials", err)
	}
	if _, err := config.LoadAccount("personal"); err != nil {
		t.Errorf("the other account must survive a single-account logout: %v", err)
	}
}

// TestLogoutUnderAccountSelectionSignsOutOnlyThatAccount covers `everyapi --account personal auth logout`.
func TestLogoutUnderAccountSelectionSignsOutOnlyThatAccount(t *testing.T) {
	newAccountsTest(t)
	seedActive(t, "work", testCreds("alice", 1))
	seedParked(t, "personal", testCreds("bob", 2))
	if err := config.SelectAccount("personal"); err != nil {
		t.Fatalf("SelectAccount: %v", err)
	}

	var err error
	captureOut(t, func() { err = Logout(nil) })
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := config.LoadAccount("personal"); !errors.Is(err, config.ErrNoSuchAccount) {
		t.Errorf("LoadAccount(personal) = %v, want ErrNoSuchAccount", err)
	}
	if err := config.SelectAccount(""); err != nil {
		t.Fatalf("clear selection: %v", err)
	}
	live, err := config.Load()
	if err != nil {
		t.Fatalf("the active account must survive: %v", err)
	}
	if live.Username != "alice" {
		t.Errorf("active credential = %q, want alice", live.Username)
	}
}

// TestCommitLoginCredentialsKeepsTheOutgoingAccount is the behaviour the whole feature turns on: signing into a second account must not evict the first.
func TestCommitLoginCredentialsKeepsTheOutgoingAccount(t *testing.T) {
	newAccountsTest(t)
	seedActive(t, "alice", testCreds("alice", 1))

	outcome, err := commitLoginCredentials(testCreds("bob", 2), "")
	if err != nil {
		t.Fatalf("commitLoginCredentials: %v", err)
	}
	if outcome.Name != "bob" {
		t.Errorf("new account name = %q, want bob", outcome.Name)
	}
	if outcome.Parked != "alice" {
		t.Errorf("parked account = %q, want alice", outcome.Parked)
	}
	live, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if live.Username != "bob" {
		t.Errorf("active credential = %q, want bob", live.Username)
	}
	kept, err := config.LoadAccount("alice")
	if err != nil {
		t.Fatalf("the previous account must be kept: %v", err)
	}
	if kept.Username != "alice" {
		t.Errorf("kept credential = %q, want alice", kept.Username)
	}
}

// TestCommitLoginCredentialsRefreshesInPlace keeps an ordinary re-login after an expiry from minting a second copy of one account.
func TestCommitLoginCredentialsRefreshesInPlace(t *testing.T) {
	newAccountsTest(t)
	seedActive(t, "alice", testCreds("alice", 1))

	refreshed := testCreds("alice", 1)
	refreshed.AccessToken = "tok-refreshed"
	outcome, err := commitLoginCredentials(refreshed, "")
	if err != nil {
		t.Fatalf("commitLoginCredentials: %v", err)
	}
	if outcome.Parked != "" {
		t.Errorf("re-login parked %q, want nothing parked", outcome.Parked)
	}
	infos, err := config.ListAccounts()
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("accounts = %+v, want exactly one", infos)
	}
	live, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if live.AccessToken != "tok-refreshed" {
		t.Errorf("access token = %q, want the refreshed one", live.AccessToken)
	}
}

// TestCommitLoginCredentialsRefreshesAParkedAccount covers the ordinary fix for a parked session that expired: signing in again must land back in that slot. A second entry there would leave the superseded — and still usable, since a new login revokes nothing — token on disk under the name the user recognizes, while the live session hid behind `work-2`.
func TestCommitLoginCredentialsRefreshesAParkedAccount(t *testing.T) {
	newAccountsTest(t)
	seedActive(t, "alice", testCreds("alice", 1))
	seedParked(t, "work", testCreds("bob", 2))

	refreshed := testCreds("bob", 2)
	refreshed.AccessToken = "tok-refreshed"
	outcome, err := commitLoginCredentials(refreshed, "")
	if err != nil {
		t.Fatalf("commitLoginCredentials: %v", err)
	}
	if outcome.Name != "work" {
		t.Errorf("account name = %q, want the parked slot 'work'", outcome.Name)
	}
	if outcome.Parked != "alice" {
		t.Errorf("parked account = %q, want alice", outcome.Parked)
	}
	infos, err := config.ListAccounts()
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("accounts = %+v, want exactly two", infos)
	}
	live, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if live.AccessToken != "tok-refreshed" {
		t.Errorf("active token = %q, want the refreshed one", live.AccessToken)
	}
	// The superseded copy must be gone, not merely hidden behind the active row.
	dir, err := config.AccountsDir()
	if err != nil {
		t.Fatalf("AccountsDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "work.json")); !os.IsNotExist(err) {
		t.Errorf("stat accounts/work.json = %v, want the superseded credential removed", err)
	}
}

// TestCommitLoginCredentialsDisambiguatesSameUsername covers two accounts whose usernames collide — a personal and a work login under the same display name.
func TestCommitLoginCredentialsDisambiguatesSameUsername(t *testing.T) {
	newAccountsTest(t)
	seedActive(t, "alice", testCreds("alice", 1))

	outcome, err := commitLoginCredentials(testCreds("alice", 2), "")
	if err != nil {
		t.Fatalf("commitLoginCredentials: %v", err)
	}
	if outcome.Name != "alice-2" {
		t.Errorf("new account name = %q, want alice-2", outcome.Name)
	}
	if outcome.Parked != "alice" {
		t.Errorf("parked account = %q, want alice", outcome.Parked)
	}
}

func TestCommitLoginCredentialsHonoursAnAlias(t *testing.T) {
	newAccountsTest(t)
	seedActive(t, "alice", testCreds("alice", 1))

	outcome, err := commitLoginCredentials(testCreds("bob", 2), "work")
	if err != nil {
		t.Fatalf("commitLoginCredentials: %v", err)
	}
	if outcome.Name != "work" {
		t.Errorf("new account name = %q, want work", outcome.Name)
	}
	name, err := config.ActiveAccountName()
	if err != nil {
		t.Fatalf("ActiveAccountName: %v", err)
	}
	if name != "work" {
		t.Errorf("ActiveAccountName = %q, want work", name)
	}
}

func TestCommitLoginCredentialsRejectsATakenAlias(t *testing.T) {
	newAccountsTest(t)
	seedActive(t, "alice", testCreds("alice", 1))
	seedParked(t, "work", testCreds("carol", 3))

	if _, err := commitLoginCredentials(testCreds("bob", 2), "work"); err == nil {
		t.Fatal("an alias already in use was accepted")
	}
	kept, err := config.LoadAccount("work")
	if err != nil {
		t.Fatalf("the existing account must survive: %v", err)
	}
	if kept.Username != "carol" {
		t.Errorf("existing account = %q, want carol", kept.Username)
	}
}

func TestCommitLoginCredentialsRejectsAnInvalidAlias(t *testing.T) {
	newAccountsTest(t)
	if _, err := commitLoginCredentials(testCreds("bob", 2), "../escape"); err == nil {
		t.Fatal("a traversing alias was accepted")
	}
}

// TestCommitLoginCredentialsUnderSelectionRefreshesThatSlot covers `everyapi --account personal auth login`: renewing a parked account's expired session must not switch the machine over to it.
func TestCommitLoginCredentialsUnderSelectionRefreshesThatSlot(t *testing.T) {
	newAccountsTest(t)
	seedActive(t, "work", testCreds("alice", 1))
	seedParked(t, "personal", testCreds("bob", 2))
	if err := config.SelectAccount("personal"); err != nil {
		t.Fatalf("SelectAccount: %v", err)
	}

	refreshed := testCreds("bob", 2)
	refreshed.AccessToken = "tok-renewed"
	outcome, err := commitLoginCredentials(refreshed, "")
	if err != nil {
		t.Fatalf("commitLoginCredentials: %v", err)
	}
	if outcome.Name != "personal" {
		t.Errorf("account name = %q, want personal", outcome.Name)
	}
	if err := config.SelectAccount(""); err != nil {
		t.Fatalf("clear selection: %v", err)
	}
	live, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if live.Username != "alice" {
		t.Errorf("active credential = %q, want the untouched alice", live.Username)
	}
	parked, err := config.LoadAccount("personal")
	if err != nil {
		t.Fatalf("LoadAccount(personal): %v", err)
	}
	if parked.AccessToken != "tok-renewed" {
		t.Errorf("parked access token = %q, want the renewed one", parked.AccessToken)
	}
}

// TestStatusAccountLineAppearsOnlyWhenItDisambiguates keeps the single-account user's status output exactly as it was, while making the multi-account one say which account it describes.
func TestStatusAccountLineAppearsOnlyWhenItDisambiguates(t *testing.T) {
	t.Run("one account stays silent", func(t *testing.T) {
		newAccountsTest(t)
		seedActive(t, "work", testCreds("alice", 1))
		if out := captureOut(t, printActiveAccountLine); out != "" {
			t.Errorf("single-account status printed %q, want nothing", out)
		}
	})

	t.Run("two accounts name the active one", func(t *testing.T) {
		newAccountsTest(t)
		seedActive(t, "work", testCreds("alice", 1))
		seedParked(t, "personal", testCreds("bob", 2))
		out := captureOut(t, printActiveAccountLine)
		if !strings.Contains(out, "work") {
			t.Errorf("status printed %q, want the active account name", out)
		}
		if strings.Contains(out, "personal") {
			t.Errorf("status printed %q, want only the active account", out)
		}
	})

	t.Run("a pinned selection is always named", func(t *testing.T) {
		newAccountsTest(t)
		seedActive(t, "work", testCreds("alice", 1))
		seedParked(t, "personal", testCreds("bob", 2))
		if err := config.SelectAccount("personal"); err != nil {
			t.Fatalf("SelectAccount: %v", err)
		}
		out := captureOut(t, printActiveAccountLine)
		if !strings.Contains(out, "personal") {
			t.Errorf("status printed %q, want the selected account name", out)
		}
	})
}

// TestLoginCredentialPathNamesTheFileTheLoginWrote keeps the "credentials saved to …" line honest. Under `everyapi --account <name> auth login` the credential lands in accounts/<name>.json, and naming credentials.json there would point the user at a file the login never touched and imply the active account changed.
func TestLoginCredentialPathNamesTheFileTheLoginWrote(t *testing.T) {
	newAccountsTest(t)
	seedActive(t, "work", testCreds("alice", 1))
	seedParked(t, "personal", testCreds("bob", 2))

	if got := filepath.Base(loginCredentialPath(loginAccountResult{Name: "work"})); got != "credentials.json" {
		t.Errorf("active account path = %q, want credentials.json", got)
	}
	parked := loginCredentialPath(loginAccountResult{Name: "personal"})
	if filepath.Base(parked) != "personal.json" || filepath.Base(filepath.Dir(parked)) != "accounts" {
		t.Errorf("parked account path = %q, want accounts/personal.json", parked)
	}
}

// TestCommitLoginCredentialsUnderSelectionRejectsADifferentAccount covers the other half of "renew that slot": authenticating as somebody else would replace the pinned account's credential while the name kept saying otherwise, so the session it names is gone and the listing still shows it.
func TestCommitLoginCredentialsUnderSelectionRejectsADifferentAccount(t *testing.T) {
	newAccountsTest(t)
	seedActive(t, "work", testCreds("alice", 1))
	seedParked(t, "personal", testCreds("bob", 2))
	if err := config.SelectAccount("personal"); err != nil {
		t.Fatalf("SelectAccount: %v", err)
	}

	if _, err := commitLoginCredentials(testCreds("carol", 3), ""); err == nil {
		t.Fatal("a pinned login accepted a credential for a different account")
	}
	kept, err := config.LoadAccount("personal")
	if err != nil {
		t.Fatalf("the pinned account must survive: %v", err)
	}
	if kept.Username != "bob" {
		t.Errorf("pinned credential = %q, want the untouched bob", kept.Username)
	}
}

// TestSameLoginAccount pins when a login overwrites in place versus parking the previous account.
func TestSameLoginAccount(t *testing.T) {
	cases := []struct {
		name string
		a, b *config.Credentials
		want bool
	}{
		{"same gateway and user", testCreds("alice", 1), testCreds("renamed-alice", 1), true},
		{"different user", testCreds("alice", 1), testCreds("bob", 2), false},
		{
			"same user on a different gateway",
			&config.Credentials{APIBase: config.DefaultAPIBase, UserID: 1},
			&config.Credentials{APIBase: "https://gateway.example.com", UserID: 1},
			false,
		},
		{
			"oauth credentials share a slot per gateway",
			&config.Credentials{APIBase: config.DefaultAPIBase, OAuthClientID: "everyapi-cli"},
			&config.Credentials{APIBase: config.DefaultAPIBase, OAuthClientID: "everyapi-cli"},
			true,
		},
		{"nil", nil, testCreds("alice", 1), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameLoginAccount(tc.a, tc.b); got != tc.want {
				t.Errorf("sameLoginAccount = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestCommitLoginCredentialsRejectsAnAliasHeldByAnotherAccount covers the collision the "refresh in place" path used to skip: re-logging into the account already active with someone else's alias would point the index at that name and bury the account that actually holds it, which the next switch then overwrites.
func TestCommitLoginCredentialsRejectsAnAliasHeldByAnotherAccount(t *testing.T) {
	newAccountsTest(t)
	seedActive(t, "home", testCreds("alice", 1))
	seedParked(t, "work", testCreds("bob", 2))

	if _, err := commitLoginCredentials(testCreds("alice", 1), "work"); err == nil {
		t.Fatal("re-login with an alias another account holds succeeded")
	}
	kept, err := config.LoadAccount("work")
	if err != nil {
		t.Fatalf("the account holding the alias must survive: %v", err)
	}
	if kept.Username != "bob" {
		t.Errorf("work credential = %q, want bob", kept.Username)
	}
	name, err := config.ActiveAccountName()
	if err != nil {
		t.Fatalf("ActiveAccountName: %v", err)
	}
	if name != "home" {
		t.Errorf("active account = %q, want home", name)
	}
}

// TestCommitLoginCredentialsRenamesTheActiveAccountWithAFreeAlias is the other half: an alias nobody holds is still allowed to rename the account being refreshed.
func TestCommitLoginCredentialsRenamesTheActiveAccountWithAFreeAlias(t *testing.T) {
	newAccountsTest(t)
	seedActive(t, "alice", testCreds("alice", 1))

	outcome, err := commitLoginCredentials(testCreds("alice", 1), "home")
	if err != nil {
		t.Fatalf("commitLoginCredentials: %v", err)
	}
	if outcome.Name != "home" || outcome.Parked != "" {
		t.Errorf("outcome = %+v, want a rename with nothing parked", outcome)
	}
	infos, err := config.ListAccounts()
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(infos) != 1 || infos[0].Name != "home" {
		t.Errorf("accounts = %+v, want only the renamed account", infos)
	}
}

// TestLogoutOverACorruptCredentialStillScrubs pins the case logout exists for: credentials.json no longer parses, so the account listing cannot describe it, and the per-tool homes still hold a live, billable relay key. Failing the command there is what would leave that key on disk.
func TestLogoutOverACorruptCredentialStillScrubs(t *testing.T) {
	newAccountsTest(t)
	seedActive(t, "work", testCreds("alice", 1))
	dir, err := config.ConfigDir()
	if err != nil {
		t.Fatalf("ConfigDir: %v", err)
	}
	home := filepath.Join(dir, toolCredentialHomes[0])
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("seed tool home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "credentials.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("corrupt credentials: %v", err)
	}

	captureOut(t, func() { err = Logout(nil) })
	if err != nil {
		t.Fatalf("logout over a corrupt credential: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "credentials.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("corrupt credentials.json survived logout: %v", err)
	}
	if _, err := os.Stat(home); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("tool credential home survived logout: %v", err)
	}
}

// TestLogoutAllUnderAccountSelectionRemovesTheActiveCredential covers `everyapi --account personal auth logout --all`: config.Delete follows the pin, so an --all that only called it would sign out of the pinned account and leave the active credential in place.
func TestLogoutAllUnderAccountSelectionRemovesTheActiveCredential(t *testing.T) {
	newAccountsTest(t)
	seedActive(t, "work", testCreds("alice", 1))
	seedParked(t, "personal", testCreds("bob", 2))
	if err := config.SelectAccount("personal"); err != nil {
		t.Fatalf("SelectAccount: %v", err)
	}

	var err error
	captureOut(t, func() { err = Logout([]string{"--all"}) })
	if err != nil {
		t.Fatalf("logout --all: %v", err)
	}
	dir, dirErr := config.ConfigDir()
	if dirErr != nil {
		t.Fatalf("ConfigDir: %v", dirErr)
	}
	if _, err := os.Stat(filepath.Join(dir, "credentials.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("active credentials.json survived logout --all: %v", err)
	}
	infos, err := config.ListAccounts()
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(infos) != 0 {
		t.Errorf("accounts remain after logout --all: %+v", infos)
	}
}
