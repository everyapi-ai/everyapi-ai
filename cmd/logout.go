package cmd

import (
	"errors"
	"flag"

	"github.com/everyapi-ai/everyapi-ai/internal/config"
)

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
		println("Already logged out.")
		return nil
	}
	if err := config.Delete(); err != nil {
		return err
	}
	println("Logged out.")
	return nil
}
