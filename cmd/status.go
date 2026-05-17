package cmd

import (
	"errors"
	"flag"
	"fmt"

	"github.com/relaya-ai/relaya-ai/internal/api"
	"github.com/relaya-ai/relaya-ai/internal/config"
)

// Status renders the user's quota and usage in USD. Reads
// quota_per_unit from /api/status (unauthenticated) so a stale token
// produces a clean 401 from /api/user/self rather than a confusing
// "got JSON but no quota_per_unit" path.
func Status(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	creds, err := config.Load()
	if errors.Is(err, config.ErrNoCredentials) {
		return errors.New("not logged in — run 'relaya login' first")
	}
	if err != nil {
		return err
	}
	client := api.New(creds.APIBase, creds.AccessToken).WithUserID(creds.UserID)
	ctx := withCtx()

	status, err := client.GetStatus(ctx)
	if err != nil {
		return fmt.Errorf("fetch system status: %w", err)
	}
	self, err := client.GetSelf(ctx)
	if err != nil {
		if api.IsUnauthorized(err) {
			return errors.New("your session expired — run 'relaya login' again")
		}
		return fmt.Errorf("fetch user: %w", err)
	}

	perUnit := status.QuotaPerUnit
	if perUnit <= 0 {
		// Defensive: server should always send a positive value; if it
		// doesn't, fall back to displaying raw quota integers rather
		// than dividing by zero.
		perUnit = 1
	}
	quotaUSD := float64(self.Quota) / perUnit
	usedUSD := float64(self.UsedQuota) / perUnit

	println("")
	if self.Email != "" {
		printf("  %s (%s)\n", self.Username, self.Email)
	} else {
		printf("  %s\n", self.Username)
	}
	printf("  quota:     $%.2f remaining   $%.2f used\n", quotaUSD, usedUSD)
	printf("  requests:  %d\n", self.RequestCount)
	printf("  topup:     %s/wallet\n", trimAPIBaseToWebOrigin(creds.APIBase))
	println("")
	return nil
}

// trimAPIBaseToWebOrigin maps `https://api.relaya.pro` →
// `https://relaya.pro` so the printed topup URL points at the
// dashboard rather than the API host. Cheap heuristic — only the
// "api." subdomain is rewritten; non-matching bases (localhost,
// custom self-host hosts) are left unchanged so they still resolve.
func trimAPIBaseToWebOrigin(base string) string {
	const apiPrefix = "https://api."
	if len(base) > len(apiPrefix) && base[:len(apiPrefix)] == apiPrefix {
		return "https://" + base[len(apiPrefix):]
	}
	return base
}
