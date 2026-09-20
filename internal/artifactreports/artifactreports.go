// Package artifactreports resolves the account-level switch for the EveryAPI Artifact delivery
// standard — the instruction `everyapi use` injects that makes a launched agent publish a completion
// report when it finishes a task.
//
// The account owns the value so one decision covers every machine; the dashboard's artifacts page and
// `everyapi settings set artifact_reports` both write it. This package is the seam between that remote
// value and a launch path that must keep working with no network at all: Enabled reads a local cache,
// Refresh tops the cache up on a bounded best-effort basis, and Set writes through.
//
// Nothing here may fail a launch. A gateway that is down, slow, or older than the field must leave the
// launch behaving exactly as the last known answer said — the worst outcome available is one unwanted
// report, and that is strictly better than refusing to start the tool the user asked for.
package artifactreports

import (
	"context"
	"errors"
	"time"

	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

// refreshTimeout bounds what a launch will wait to re-learn the switch. Short on purpose: this runs in
// front of the tool the user is trying to start, and a stale-but-correct-yesterday answer beats making
// them watch a spinner on a bad network. Anything slower than this is treated as unreachable.
const refreshTimeout = 2 * time.Second

// Enabled reports whether this launch should carry the artifact delivery standard, from the local cache
// only. Never blocks, never errors: an unreadable settings file resolves to the shipped default of on.
//
// A cache belonging to a different account resolves to the default too. `everyapi auth accounts switch`
// swaps the active credentials without touching settings.json, so honouring whatever value happens to be
// cached would apply the previous account's decision to this one for the rest of the TTL.
func Enabled() bool {
	settings, err := config.LoadSettings()
	if err != nil || !settings.ArtifactReportsCachedFor(currentAccountID()) {
		return true
	}
	return settings.ArtifactReportsEnabled()
}

// Refresh brings the cached switch up to date from the account, if it is stale and the account is
// reachable. Silent by design — it is a cache top-up on the launch path, not an operation the user asked
// for, so every failure leaves the cache alone and returns.
func Refresh(ctx context.Context) {
	client, account, ok := managementClient()
	if !ok {
		return
	}
	settings, err := config.LoadSettings()
	if err != nil || settings.ArtifactReportsFresh(time.Now(), account) {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, refreshTimeout)
	defer cancel()
	self, err := client.GetSelf(ctx)
	if err != nil || self.ArtifactReports == nil {
		// A nil field is a deployment that predates the setting, not an account that wants reports off.
		// Overwriting the cache with the zero value there would switch reports off for everybody whose
		// gateway has not been upgraded yet.
		return
	}
	// Re-read rather than reusing the copy loaded above: whatever else the launch has been doing (pinning
	// a model, recording a reasoning level) may have written the file since, and this must not roll that back.
	current, err := config.LoadSettings()
	if err != nil {
		return
	}
	current.SetArtifactReportsCache(*self.ArtifactReports, account, time.Now())
	_ = config.SaveSettings(current)
}

// ErrNoSession is returned by Set when there is no management session to write the account with.
var ErrNoSession = errors.New("artifact reports: no account session")

// Set writes the switch to the account and refreshes the local cache.
//
// Unlike Refresh this one is allowed — required — to fail loudly. The user asked for a specific outcome
// on their account, and a local-only write that silently disagrees with what the dashboard shows is the
// failure this whole design exists to avoid.
func Set(ctx context.Context, enabled bool) error {
	client, account, ok := managementClient()
	if !ok {
		return ErrNoSession
	}
	if err := client.SetArtifactReports(ctx, enabled); err != nil {
		return err
	}
	// Past this point the account write has landed, which is what the user asked for. Neither half of the
	// cache update may turn that into a reported failure: a read-only or full config dir would otherwise
	// make `settings set artifact_reports false` exit non-zero on a change that DID take effect, sending
	// the user to flip it again. The cache is a local optimisation — the next Refresh repairs it.
	settings, err := config.LoadSettings()
	if err != nil {
		return nil
	}
	settings.SetArtifactReportsCache(enabled, account, time.Now())
	_ = config.SaveSettings(settings)
	return nil
}

// managementClient builds a client for the authenticated account endpoints and reports which account it
// speaks for, or that there is no usable session.
//
// An OAuth2 relay-key login is deliberately excluded: its stored token IS the relay key and carries no
// management session, so /api/user/self cannot answer for it at all. Such a login can neither read nor
// write this switch, and gets the shipped default — the same position it is in for every other account
// setting, and better than pinning it to whatever a previously signed-in account chose.
func managementClient() (*api.Client, int, bool) {
	creds, ok := managementCredentials()
	if !ok {
		return nil, 0, false
	}
	return api.ForCredentials(creds), creds.UserID, true
}

// managementCredentials is the stored session behind managementClient, without building the client.
//
// Split out for currentAccountID, which needs nothing but the account id: api.ForCredentials resolves the
// gateway base through config.ResolveAPIBaseForBase, which re-reads settings.json, and Enabled runs on the
// launch path once per AgentInstructions caller — five of them per launch, plus once per editor redraw.
func managementCredentials() (*config.Credentials, bool) {
	creds, err := config.Load()
	if err != nil || creds == nil || creds.OAuthClientID != "" || creds.UserID == 0 {
		return nil, false
	}
	return creds, true
}

// currentAccountID is the account this machine is signed in as, or 0 when there is none usable. Zero never
// matches a stored cache, because SetArtifactReportsCache is only ever reached with a real account id.
func currentAccountID() int {
	creds, ok := managementCredentials()
	if !ok {
		return 0
	}
	return creds.UserID
}
