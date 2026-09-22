// Package artifactreports resolves the account-level switch for the EveryAPI Artifact delivery
// standard — the instruction `everyapi use` injects that makes a launched agent publish a completion
// report when it finishes a task.
//
// The account owns the value so one decision covers every machine; the dashboard's artifacts page and
// `everyapi settings set artifact_reports` both write it. This package is the seam between that remote
// value and a launch path that must keep working with no network at all: Enabled reads the last known
// value for displays and offline fallback, Refresh reconciles it on a bounded best-effort basis, Get
// performs the strict live read that gates automatic publication, and Set writes through.
//
// Refresh never fails a launch. Get deliberately reports an unavailable or old gateway instead of
// turning a cached `true` into permission to publish after the account may have opted out elsewhere.
package artifactreports

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

// refreshTimeout bounds both launch-time reconciliation and the completion-time live read. Anything
// slower is treated as unreachable; the launch continues, while the publication gate fails closed.
const refreshTimeout = 2 * time.Second

// Enabled reports the last known account value from the local cache only. It is for displays and offline
// fallback, not automatic-publication authorization. Never blocks or errors: an unreadable settings file
// resolves to the shipped default of on.
//
// A cache belonging to a different account resolves to the default too. `everyapi auth accounts switch`
// swaps the active credentials without touching settings.json, so honouring whatever value happens to be
// cached would apply the previous account's decision to this one while offline.
func Enabled() bool {
	settings, err := config.LoadSettings()
	if err != nil || !settings.ArtifactReportsCachedFor(currentAccountID()) {
		return true
	}
	return settings.ArtifactReportsEnabled()
}

// Refresh brings the cached switch up to date from the account when the account is reachable. It asks on
// every launch: the switch is changed outside this process (most often in the dashboard), so a local TTL
// would make even the next launch ignore the user's new choice. Silent by design — it is reconciliation
// on the launch path, not an operation the user asked for, so every failure leaves the cache alone and
// returns.
func Refresh(ctx context.Context) {
	_, _ = Get(ctx)
}

// Get reads the current account value from the gateway and returns it only when the gateway answered.
// Unlike Refresh, this is for an explicit live read such as the completion-time publication gate: a
// cached `true` must not authorize a report after the account was switched off elsewhere. Callers decide
// how to surface the error; the injected agent standard fails closed and publishes automatically only on
// an exact true result.
func Get(ctx context.Context) (bool, error) {
	client, account, ok := managementClient()
	if !ok {
		return false, ErrNoSession
	}
	ctx, cancel := context.WithTimeout(ctx, refreshTimeout)
	defer cancel()
	self, err := client.GetSelf(ctx)
	if err != nil {
		return false, err
	}
	if self.ArtifactReports == nil {
		// A nil field is a deployment that predates the setting, not an account that wants reports off.
		// Overwriting the cache with the zero value there would switch reports off for everybody whose
		// gateway has not been upgraded yet.
		return false, fmt.Errorf("artifact reports: gateway does not expose the account preference")
	}
	// Re-read rather than reusing the copy loaded above: whatever else the launch has been doing (pinning
	// a model, recording a reasoning level) may have written the file since, and this must not roll that back.
	current, err := config.LoadSettings()
	if err != nil {
		return *self.ArtifactReports, nil
	}
	current.SetArtifactReportsCache(*self.ArtifactReports, account, time.Now())
	_ = config.SaveSettings(current)
	return *self.ArtifactReports, nil
}

// ErrNoSession is returned by Get or Set when there is no management session for the account endpoint.
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
// gateway base through config.ResolveAPIBaseForBase, which re-reads settings.json, while Enabled is also
// used by local settings displays that need no API client.
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
