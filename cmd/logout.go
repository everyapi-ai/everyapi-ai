package cmd

import (
	"errors"
	"flag"
	"os"
	"path/filepath"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/tools"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

// toolCredentialHomes are the fixed per-tool config dirs that `everyapi use` seeds with a billable relay key. They live next to credentials.json but config.Delete only removes the latter, so logout must scrub these too — otherwise a fully working credential survives "logout" on disk.
//
// These are the homes EveryAPI owns outright and can delete whole. A client whose credential lands in a file the vendor owns needs a surgical scrub instead; see vendorCredentialScrubs. Launches that take the live-catalog path do not land here at all; see preparedSessionHomes.
//
// codex-home is deliberately absent. It contains Codex's durable rollouts and SQLite thread state, while its auth.json stores only transparentPlaceholderCredential; the real relay key is process-scoped. Deleting that home during logout leaves already-running Codex processes attached to a recreated, unmigrated database and causes repeated `no such table: threads` transcript failures.
var toolCredentialHomes = []string{"hermes-home"}

// preparedSessionHomes is the config-dir-relative root holding every process-scoped client home, mirroring the path tools.preparedHomeRoot resolves. It is where today's live-catalog launches actually land, and two adapters inline the relay key verbatim inside one (hermes writes it into config.yaml, cline into settings/providers.json), so logout clears the whole root rather than chasing individual adapters — that covers every prepared home the launcher can mint, present and future. Nothing reachable is lost: every launch mints a fresh home under this root and no launch ever reads an older one.
const preparedSessionHomes = "sessions"

// vendorCredentialScrubs remove the relay key from files that belong to the client rather than to EveryAPI, so the deletion has to be one entry rather than one directory. DeepSeek Harness is the only launch that persists the key this way — every other client either receives it in its process environment or references it by name.
var vendorCredentialScrubs = []struct {
	name  string
	scrub func() error
}{
	{name: "DeepSeek Harness", scrub: tools.ScrubDeepSeekHarnessCredential},
}

// Logout removes the on-disk credentials. Idempotent — calling it twice doesn't error (config.Delete handles missing file as success). We deliberately do NOT call the backend to invalidate the token: (a) the user wants offline logout to work, (b) the token is the same user-scoped access_token used by /api/user/self, killing it remotely would log them out of the dashboard too.
//
// With more than one account stored, a bare logout signs out of the one currently in effect and promotes a remaining account into its place, so the machine does not end up "logged out" while still holding live sessions the user never asked to drop. `everyapi --account <name> auth logout` signs out that one specifically; `--all` drops every account at once.
func Logout(args []string) error {
	fs := flag.NewFlagSet("logout", flag.ContinueOnError)
	all := fs.Bool("all", false, "sign out of every stored account")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: everyapi auth logout [--all]")
	}
	unlock, err := acquireCredentialLock()
	if err != nil {
		return err
	}
	defer unlock()
	if *all {
		return logoutAllAccounts()
	}
	active, nameErr := config.ActiveAccountName()
	if nameErr != nil {
		return nameErr
	}
	target := config.SelectedAccountName()
	if target == "" {
		target = active
	}
	if target == "" {
		// Nothing is logged in. Still run the scrub below — a prior partial logout, a crash, or a manual deletion of credentials.json can leave a live, billable relay key behind in hermes-home or under the prepared-session root, and an early return here is exactly what would let that key outlive logout.
		if err := config.Delete(); err != nil {
			return err
		}
		scrubToolCredentials()
		cliout.Println(i18n.T("logout.done"))
		return nil
	}
	remaining, err := signOutAccount(target)
	if err != nil {
		return err
	}
	if target == active {
		cliout.Println(i18n.T("logout.done"))
	} else {
		// `everyapi --account other auth logout` left the machine signed in as the active account. "Logged out." would read as the opposite, so name the account that was actually dropped — the same wording `auth accounts remove` uses for the same operation.
		cliout.Printf(i18n.T("accounts.removed")+"\n", cliout.Sanitize(target))
	}
	printRemainingAccounts(remaining)
	return nil
}

// logoutAllAccounts drops every stored account and the account index, leaving the machine exactly as a fresh install.
func logoutAllAccounts() error {
	// Drop any `--account` pin first: signing out of everything is not scoped to one account, and config.Delete honours the pin — under `everyapi --account foo auth logout --all` it would remove foo's file and leave the active credentials.json in place.
	if err := config.SelectAccount(""); err != nil {
		return err
	}
	// Both removals are unconditional rather than driven by a listing: an account whose file no longer parses, or a duplicate left by a switch that died mid-way, is invisible to the listing and is still a live, billable credential. Skipping it here is exactly what would let it outlive a sign-out of everything.
	if err := config.Delete(); err != nil {
		return err
	}
	if err := config.RemoveAllAccounts(); err != nil {
		return err
	}
	scrubToolCredentials()
	cliout.Println(i18n.T("logout.done_all"))
	return nil
}

// scrubToolCredentials removes the per-tool config homes seeded by `everyapi use` so a working relay key never outlives logout. Best effort: credentials.json is already gone by here, so a removal error only warrants a warning (e.g. a file held open on Windows), not a failed logout.
func scrubToolCredentials() {
	cfgDir, err := config.ConfigDir()
	if err != nil {
		return
	}
	for _, home := range toolCredentialHomes {
		p := filepath.Join(cfgDir, home)
		if err := os.RemoveAll(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			cliout.Printf(i18n.T("logout.cached_key_warn"), p, err)
		}
	}
	// The process-scoped homes carry the key on the path launches actually take today, and the only other thing that removes them is the next `everyapi use`'s age-gated sweep — 7 days for a home with no readable owner, 30 days while its owner PID still looks alive. That is far too late for a credential logout has already given up the ability to revoke server-side, and it never runs at all if the user simply stops launching tools.
	prepared := filepath.Join(cfgDir, preparedSessionHomes)
	if err := os.RemoveAll(prepared); err != nil && !errors.Is(err, os.ErrNotExist) {
		cliout.Printf(i18n.T("logout.cached_key_warn"), prepared, err)
	}
	for _, target := range vendorCredentialScrubs {
		if err := target.scrub(); err != nil {
			cliout.Printf(i18n.T("logout.cached_key_warn"), target.name, err)
		}
	}
}
