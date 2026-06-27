package cmd

import (
	"errors"
	"flag"
	"os"
	"path/filepath"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

// toolCredentialHomes are the per-tool config dirs that `everyapi use`
// seeds with the resolved relay key (codex-home/auth.json holds it as
// OPENAI_API_KEY; hermes-home/config.yaml inlines it as api_key). They
// live next to credentials.json but config.Delete only removes the
// latter, so logout must scrub these too — otherwise a fully working,
// billable spend credential survives "logout" on disk.
var toolCredentialHomes = []string{"codex-home", "hermes-home"}

// Logout removes the on-disk credentials. Idempotent — calling it
// twice doesn't error (config.Delete handles missing file as success).
// We deliberately do NOT call the backend to invalidate the token:
// (a) the user wants offline logout to work, (b) the token is the
// same user-scoped access_token used by /api/user/self, killing it
// remotely would log them out of the dashboard too.
func Logout(args []string) error {
	fs := flag.NewFlagSet("logout", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	_, loadErr := config.Load()
	if errors.Is(loadErr, config.ErrNoCredentials) {
		cliout.Println(i18n.T("logout.done"))
		return nil
	}
	if err := config.Delete(); err != nil {
		return err
	}
	scrubToolCredentials()
	cliout.Println(i18n.T("logout.done"))
	return nil
}

// scrubToolCredentials removes the per-tool config homes seeded by
// `everyapi use` so a working relay key never outlives logout. Best
// effort: credentials.json is already gone by here, so a removal error
// only warrants a warning (e.g. a file held open on Windows), not a
// failed logout.
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
}
