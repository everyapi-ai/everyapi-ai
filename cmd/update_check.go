package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/version"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

// updateCheckFilename is the on-disk cache name. Stored alongside credentials.json under ConfigDir so logout doesn't wipe it — the reminder cadence is a UX preference, not a credential.
const updateCheckFilename = "update_check.json"

// updateCheckPromptCooldown — when the user picks "remind me later", don't re-prompt within this window even if the cache is still reporting a newer version. Without this the prompt would reappear on every single command invocation for 24h.
//
// The cooldown deliberately bridges across versions (a newer release during the window still respects "later"), to avoid a prompt firehose right after a release lands on a user who just dismissed the previous one. SkippedVersion is the per-version escape hatch for users who never want that specific version.
const updateCheckPromptCooldown = 12 * time.Hour

// updateCheckFailureBackoff — after a failed fetch (offline, slow network), don't retry within this window. Without it, an offline laptop pays `updateRefreshTimeout` on every single command for the entire offline session — since there's no TTL gate, every command would otherwise re-enter the refresh path and re-hit the timeout.
//
// 1h is a compromise: short enough that "GitHub was briefly down" recovers quickly, long enough that an actually-offline machine stops eating latency.
const updateCheckFailureBackoff = 1 * time.Hour

// updateRefreshTimeout caps the synchronous GitHub poll on the auto-check path. Tight by design: every invocation enters this path (no TTL caching) so a slow response can't hold up the user's real command for more than 2.5s. On timeout / network error we write a failure marker so updateCheckFailureBackoff (1h) absorbs the repeat cost on an offline machine.
const updateRefreshTimeout = 2500 * time.Millisecond

// updateCheckCache mirrors the JSON cache file. Hand-rolled tags so a future schema change can stay backwards-compatible.
//
// Two timestamps because success and "I tried" need to be distinguished:
//   - CheckedAt advances ONLY on a successful fetch — it backs the TTL freshness check.
//   - LastFetchAttemptAt advances on EVERY refresh call (success or failure) — it backs the failure-backoff so an offline session doesn't hammer GitHub on every command.
//
// Concurrent CLI invocations can race on this file (last-writer wins). A picked-skip in process A could be overwritten by process B's refresh write that pre-dated A's skip. Edge case — accepted as not worth a file lock for a UX-hint cache.
type updateCheckCache struct {
	CheckedAt          int64  `json:"checked_at"`
	LastFetchAttemptAt int64  `json:"last_fetch_attempt_at,omitempty"`
	LatestVersion      string `json:"latest_version"`
	SkippedVersion     string `json:"skipped_version,omitempty"`
	LastPromptedAt     int64  `json:"last_prompted_at,omitempty"`
}

func updateCheckCachePath() (string, error) {
	dir, err := config.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, updateCheckFilename), nil
}

func loadUpdateCheckCache() (*updateCheckCache, error) {
	path, err := updateCheckCachePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var c updateCheckCache
	if err := json.Unmarshal(data, &c); err != nil {
		// Corrupt cache (hand-edited / partial write) — treat as "no cache" so the next refresh rebuilds it cleanly.
		return nil, nil
	}
	return &c, nil
}

func saveUpdateCheckCache(c *updateCheckCache) error {
	path, err := updateCheckCachePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	// 0o600 to match the parent dir (0o700) and credentials.json sitting next to it. The contents aren't secret, but consistency keeps the permission audit clean.
	return os.WriteFile(path, data, 0o600)
}

// reportCacheSaveError surfaces a save failure once to stderr. Silent-fail would loop the user through the same prompt on every command if the cache dir isn't writable (permission / disk-full), so we want them to know — but we don't want to bury the actual command output, so one line on stderr, no stack trace.
func reportCacheSaveError(err error) {
	if err == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "everyapi: update-check cache write failed: %v\n", err)
}

// updateCheckSkipCommands is the set of top-level command names that skip the auto-update check. `update` does its own live poll; `version` is a fast info command users expect to print one line and exit; `mcp` (no args) runs as a stdio JSON-RPC server where interactive prompts would corrupt the protocol; help flags should stay quiet.
var updateCheckSkipCommands = map[string]struct{}{
	"update":    {},
	"version":   {},
	"--version": {},
	"-v":        {},
	"mcp":       {},
	"help":      {},
	"--help":    {},
	"-h":        {},
}

// promptChoice — the picker presents three rows in a fixed order; indexing them by name makes the dispatch arm in handleUpdatePrompt resilient to future re-ordering of the choices slice. iota stays glued to the slice's index order; changing the slice without renumbering breaks the test for the dispatch logic.
const (
	choiceUpdate = iota
	choiceLater
	choiceSkip
)

// Test seams. Production callers and tests both go through these vars (which default to the real implementations); tests reassign them with a defer-restore. Kept as a small set so the swap surface stays bounded.
//
//   - isInteractiveFn   — lets tests bypass the TTY check so the later guards (env / commandName / dev) can actually be exercised. Without injection every guard test would short-circuit on the TTY-false branch and prove nothing.
//   - resolveVersionFn  — `go test` binaries report Version="dev" which short-circuits MaybePromptUpdate; tests that need to exercise the cache / refresh / prompt paths swap in a real- looking semver string.
//   - fetchLatestTagFn  — swaps the live GitHub poll for a stub so the refresh path doesn't escape the test sandbox. The auto-check only needs the tag (to compare + cache), so it routes through fetchLatestTag — the github.com redirect that never spends the api.github.com 60/hour rate-limit bucket. The changelog body (API-only) is the manual `update` flow's concern, not the silent check's.
//   - promptFn          — the update picker. A var so a test can assert MaybePromptUpdate REACHED the prompt (e.g. the launcher path) without a TTY; the real cliprompt.Pick blocks on absent input.
var (
	isInteractiveFn  = cliprompt.IsInteractive
	resolveVersionFn = version.Resolve
	fetchLatestTagFn = fetchLatestTag
	promptFn         = cliprompt.Pick
)

// MaybePromptUpdate runs the auto-update check before the user's command. Returns true if the caller should SKIP the original command — happens only when the user picked "update now" and we dispatched the upgrade ourselves (running an old binary right after kicking off a brew upgrade would be confusing at best).
//
// Skipped scenarios short-circuit without disk or network I/O:
//   - non-TTY (CI / piped invocation)
//   - dev builds (no release to compare against meaningfully)
//   - EVERYAPI_NO_UPDATE_CHECK=1
//   - command is in updateCheckSkipCommands
//
// Polling policy: every qualifying invocation hits GitHub. Users expect "I just ran everyapi, if there's a new release I want to know" to mean exactly that — within a single command, not "within the next 4-24h once some background TTL expires". The /releases/latest 302 redirect (post-#374) has no rate limit, the updateRefreshTimeout caps the wait at 2.5s, and on any failure updateCheckFailureBackoff (1h) absorbs the repeat cost so an offline machine doesn't pay 2.5s on every command. The 12h LastPromptedAt cooldown still gates how often the user sees a picker — this function only governs the network call.
//
// The refresh is SYNCHRONOUS. Async was tempting but the goroutine would race the user's command finishing on fast paths (status, version, …) and the cache would never get written.
func MaybePromptUpdate(commandName string) (skipOriginal bool) {
	if !isInteractiveFn() {
		return false
	}
	if os.Getenv("EVERYAPI_NO_UPDATE_CHECK") == "1" {
		return false
	}
	if _, skip := updateCheckSkipCommands[commandName]; skip {
		return false
	}
	ver, _ := resolveVersionFn()
	if ver == "dev" {
		return false
	}

	cache, _ := loadUpdateCheckCache()

	// `lastAttemptFailed` is the only short-circuit on the refresh — it covers offline / GitHub-down by remembering that the previous attempt failed and giving the network 1h to recover.
	//
	// Distinguishing success from failure matters: refreshUpdateCheck- CacheSync stamps `LastFetchAttemptAt = now` on BOTH paths, and `CheckedAt = now` only on success. So `LastFetchAttemptAt > CheckedAt` is exactly the "the last attempt was a failure" signal. A naive "any recent attempt" check would treat a successful fetch as a reason to skip the next one, silently degrading "poll every invocation" into "poll every 1h" — which is the bug review caught before this landed.
	lastAttemptFailed := cache != nil &&
		cache.LastFetchAttemptAt > 0 &&
		cache.LastFetchAttemptAt > cache.CheckedAt &&
		time.Since(time.Unix(cache.LastFetchAttemptAt, 0)) < updateCheckFailureBackoff

	if !lastAttemptFailed {
		if refreshed := refreshUpdateCheckCacheSync(cache); refreshed != nil {
			cache = refreshed
		}
	}

	if !updatePromptable(ver, cache) {
		return false
	}
	return handleUpdatePrompt(ver, cache)
}

// updatePromptable is the pure decision function for "should we show the update prompt?". Extracted from MaybePromptUpdate so the four gates (no cache / not newer / version-skipped / cooldown active) can be unit-tested in isolation, without touching TTY, network, or disk. The caller still owns the cache load and refresh — this just says yes/no on a fully-populated cache.
func updatePromptable(currentVer string, cache *updateCheckCache) bool {
	if cache == nil || cache.LatestVersion == "" {
		return false
	}
	if compareSemver(currentVer, cache.LatestVersion) >= 0 {
		return false
	}
	if cache.SkippedVersion == cache.LatestVersion {
		return false
	}
	if cache.LastPromptedAt > 0 &&
		time.Since(time.Unix(cache.LastPromptedAt, 0)) < updateCheckPromptCooldown {
		return false
	}
	return true
}

// refreshUpdateCheckCacheSync polls GitHub for the latest release and rewrites the cache, returning the new cache.
//
// On SUCCESS: writes CheckedAt = now, LastFetchAttemptAt = now, LatestVersion = rel.Tag, and carries forward LastPromptedAt and a version-matching SkippedVersion.
//
// On FAILURE (offline, timeout, rate-limited, 5xx): writes a marker cache where LastFetchAttemptAt = now but CheckedAt and LatestVersion are carried from prev (or zeroed if prev was nil), then returns that cache. The marker drives the failure-backoff so the next 1h of commands skip the network call entirely.
func refreshUpdateCheckCacheSync(prev *updateCheckCache) *updateCheckCache {
	ctx, cancel := context.WithTimeout(context.Background(), updateRefreshTimeout)
	defer cancel()
	tag, err := fetchLatestTagFn(ctx)
	now := time.Now().Unix()

	if err != nil {
		c := &updateCheckCache{LastFetchAttemptAt: now}
		if prev != nil {
			c.CheckedAt = prev.CheckedAt
			c.LatestVersion = prev.LatestVersion
			c.SkippedVersion = prev.SkippedVersion
			c.LastPromptedAt = prev.LastPromptedAt
		}
		if err := saveUpdateCheckCache(c); err != nil {
			reportCacheSaveError(err)
		}
		return c
	}

	c := &updateCheckCache{
		CheckedAt:          now,
		LastFetchAttemptAt: now,
		LatestVersion:      tag,
	}
	if prev != nil {
		// Carry forward the per-version skip and the last-prompted timestamp. If GitHub published a newer release than the one the user actively skipped, clear the skip so the new version gets its own prompt — explicit SkippedVersion match against the *new* tag.
		c.LastPromptedAt = prev.LastPromptedAt
		if prev.SkippedVersion != "" && prev.SkippedVersion == tag {
			c.SkippedVersion = prev.SkippedVersion
		}
	}
	if err := saveUpdateCheckCache(c); err != nil {
		reportCacheSaveError(err)
	}
	return c
}

// handleUpdatePrompt shows the 3-choice picker and dispatches the user's selection. Returns true iff the original command should be skipped (we ran `update` instead).
//
// last_prompted_at is stamped BEFORE the picker renders so a Ctrl-C / Esc out of the picker still applies the cooldown — otherwise the prompt would re-appear on the next invocation seconds later.
func handleUpdatePrompt(currentVer string, cache *updateCheckCache) bool {
	cache.LastPromptedAt = time.Now().Unix()
	if err := saveUpdateCheckCache(cache); err != nil {
		reportCacheSaveError(err)
	}

	// The picker's title combines the "version → version" notice and the question, so the user sees one rendering instead of a separate Printf line followed by a picker. Keeps the layout compact on small terminals.
	title := fmt.Sprintf(i18n.T("update.notice"), currentVer, cache.LatestVersion) +
		"\n" + i18n.T("update.choice_prompt")
	choices := []string{
		choiceUpdate: i18n.T("update.choice_yes"),
		choiceLater:  i18n.T("update.choice_later"),
		choiceSkip:   i18n.T("update.choice_skip"),
	}
	idx, err := promptFn(title, choices)
	if err != nil {
		// Esc / Ctrl-C / unknown selection — treat as "later". The cooldown stamp above ensures we don't loop the prompt.
		return false
	}
	switch idx {
	case choiceUpdate:
		dispatched, err := updateRun(nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "update: %v\n", err)
			return false
		}
		// Only swallow the user's original command when an upgrade was actually dispatched (updateRun's real outcome, not a re-derived install method): the script/unknown paths just print a hint, and even brew/go can short-circuit without running anything ("already up to date" after a stale cache). In all those cases the command the user actually typed must still run.
		return dispatched
	case choiceSkip:
		cache.SkippedVersion = cache.LatestVersion
		if err := saveUpdateCheckCache(cache); err != nil {
			reportCacheSaveError(err)
		}
	}
	return false
}
