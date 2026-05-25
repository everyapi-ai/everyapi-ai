package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/everyapi-ai/everyapi-ai/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/internal/version"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

// updateCheckFilename is the on-disk cache name. Stored alongside
// credentials.json under ConfigDir so logout doesn't wipe it — the
// reminder cadence is a UX preference, not a credential.
const updateCheckFilename = "update_check.json"

// updateCheckCacheTTL caps how often we hit GitHub's releases API
// from the auto-check path. Manual `everyapi update` always polls
// the live endpoint; this only governs the silent pre-command poll.
const updateCheckCacheTTL = 24 * time.Hour

// updateCheckPromptCooldown — when the user picks "remind me later",
// don't re-prompt within this window even if the cache is still
// reporting a newer version. Without this the prompt would reappear
// on every single command invocation for 24h.
//
// The cooldown deliberately bridges across versions (a newer release
// during the window still respects "later"), to avoid a prompt
// firehose right after a release lands on a user who just dismissed
// the previous one. SkippedVersion is the per-version escape hatch
// for users who never want that specific version.
const updateCheckPromptCooldown = 12 * time.Hour

// updateCheckFailureBackoff — after a failed fetch (offline, slow
// network, rate-limited), don't retry within this window. Without
// it, an offline laptop pays `updateRefreshTimeout` on every single
// command for the entire offline session, because `loadUpdateCheckCache`
// keeps returning nil and `cacheFresh` keeps being false.
//
// 1h is a compromise: short enough that "GitHub was briefly down"
// recovers quickly, long enough that an actually-offline machine
// stops eating latency.
const updateCheckFailureBackoff = 1 * time.Hour

// updateRefreshTimeout caps the synchronous GitHub poll on the
// auto-check path. Tight by design: this latency hits at most once
// per `updateCheckCacheTTL` (cache miss / stale) AND not within
// `updateCheckFailureBackoff` after a failure, so the worst case
// is one slow command every 1-24h. Any timeout silently writes a
// failure marker (CheckedAt unchanged, LastFetchAttemptAt = now) so
// the backoff kicks in.
const updateRefreshTimeout = 2500 * time.Millisecond

// updateCheckCache mirrors the JSON cache file. Hand-rolled tags so a
// future schema change can stay backwards-compatible.
//
// Two timestamps because success and "I tried" need to be distinguished:
//   - CheckedAt advances ONLY on a successful fetch — it backs the
//     TTL freshness check.
//   - LastFetchAttemptAt advances on EVERY refresh call (success or
//     failure) — it backs the failure-backoff so an offline session
//     doesn't hammer GitHub on every command.
//
// Concurrent CLI invocations can race on this file (last-writer
// wins). A picked-skip in process A could be overwritten by process
// B's refresh write that pre-dated A's skip. Edge case — accepted as
// not worth a file lock for a UX-hint cache.
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
		// Corrupt cache (hand-edited / partial write) — treat as
		// "no cache" so the next refresh rebuilds it cleanly.
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
	return os.WriteFile(path, data, 0o644)
}

// reportCacheSaveError surfaces a save failure once to stderr.
// Silent-fail would loop the user through the same prompt on every
// command if the cache dir isn't writable (permission / disk-full),
// so we want them to know — but we don't want to bury the actual
// command output, so one line on stderr, no stack trace.
func reportCacheSaveError(err error) {
	if err == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "everyapi: update-check cache write failed: %v\n", err)
}

// updateCheckSkipCommands is the set of top-level command names that
// skip the auto-update check. `update` does its own live poll;
// `version` is a fast info command users expect to print one line
// and exit; `mcp` (no args) runs as a stdio JSON-RPC server where
// interactive prompts would corrupt the protocol; help flags should
// stay quiet.
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

// promptChoice — the picker presents three rows in a fixed order;
// indexing them by name makes the dispatch arm in handleUpdatePrompt
// resilient to future re-ordering of the choices slice. iota stays
// glued to the slice's index order; changing the slice without
// renumbering breaks the test for the dispatch logic.
const (
	choiceUpdate = iota
	choiceLater
	choiceSkip
)

// Test seams. Production callers and tests both go through these
// vars (which default to the real implementations); tests reassign
// them with a defer-restore. Kept as a small set so the swap surface
// stays bounded.
//
//   - isInteractiveFn   — lets tests bypass the TTY check so the
//                         later guards (env / commandName / dev) can
//                         actually be exercised. Without injection
//                         every guard test would short-circuit on
//                         the TTY-false branch and prove nothing.
//   - resolveVersionFn  — `go test` binaries report Version="dev"
//                         which short-circuits MaybePromptUpdate;
//                         tests that need to exercise the cache /
//                         refresh / prompt paths swap in a real-
//                         looking semver string.
//   - fetchLatestReleaseFn — swaps the live GitHub poll for a stub
//                         so the refresh path doesn't escape the
//                         test sandbox.
var (
	isInteractiveFn      = cliprompt.IsInteractive
	resolveVersionFn     = version.Resolve
	fetchLatestReleaseFn = fetchLatestRelease
)

// MaybePromptUpdate runs the auto-update check before the user's
// command. Returns true if the caller should SKIP the original
// command — happens only when the user picked "update now" and we
// dispatched the upgrade ourselves (running an old binary right after
// kicking off a brew upgrade would be confusing at best).
//
// Skipped scenarios short-circuit without disk or network I/O:
//   - non-TTY (CI / piped invocation)
//   - dev builds (no release to compare against meaningfully)
//   - EVERYAPI_NO_UPDATE_CHECK=1
//   - command is in updateCheckSkipCommands
//
// The cache-stale refresh is SYNCHRONOUS with a tight timeout
// (updateRefreshTimeout). It runs at most once per cache TTL on the
// success path, and at most once per failure-backoff on the offline
// path — so a flaky / offline network can't punish every command.
// Doing it async was tempting but the goroutine would race the
// user's command finishing on fast paths (status, version, …) and
// the cache would never get written.
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
	// `cacheFresh` requires BOTH a recent CheckedAt and a non-empty
	// LatestVersion — a partially-written cache (CheckedAt set but
	// LatestVersion empty) shouldn't lock us out of a refresh for 24h.
	cacheFresh := cache != nil &&
		cache.LatestVersion != "" &&
		time.Since(time.Unix(cache.CheckedAt, 0)) < updateCheckCacheTTL

	// `attemptedRecently` short-circuits the refresh after a failed
	// fetch. Pairs with refreshUpdateCheckCacheSync's failure-marker
	// write to bound the offline / rate-limited cost to one slow
	// command per `updateCheckFailureBackoff`.
	attemptedRecently := cache != nil &&
		cache.LastFetchAttemptAt > 0 &&
		time.Since(time.Unix(cache.LastFetchAttemptAt, 0)) < updateCheckFailureBackoff

	if !cacheFresh && !attemptedRecently {
		if refreshed := refreshUpdateCheckCacheSync(cache); refreshed != nil {
			cache = refreshed
		}
	}

	if !updatePromptable(ver, cache) {
		return false
	}
	return handleUpdatePrompt(ver, cache)
}

// updatePromptable is the pure decision function for "should we show
// the update prompt?". Extracted from MaybePromptUpdate so the four
// gates (no cache / not newer / version-skipped / cooldown active)
// can be unit-tested in isolation, without touching TTY, network,
// or disk. The caller still owns the cache load and refresh — this
// just says yes/no on a fully-populated cache.
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

// refreshUpdateCheckCacheSync polls GitHub for the latest release and
// rewrites the cache, returning the new cache.
//
// On SUCCESS: writes CheckedAt = now, LastFetchAttemptAt = now,
// LatestVersion = rel.Tag, and carries forward LastPromptedAt and a
// version-matching SkippedVersion.
//
// On FAILURE (offline, timeout, rate-limited, 5xx): writes a marker
// cache where LastFetchAttemptAt = now but CheckedAt and
// LatestVersion are carried from prev (or zeroed if prev was nil),
// then returns that cache. The marker drives the failure-backoff so
// the next 1h of commands skip the network call entirely.
func refreshUpdateCheckCacheSync(prev *updateCheckCache) *updateCheckCache {
	ctx, cancel := context.WithTimeout(context.Background(), updateRefreshTimeout)
	defer cancel()
	rel, err := fetchLatestReleaseFn(ctx)
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
		LatestVersion:      rel.Tag,
	}
	if prev != nil {
		// Carry forward the per-version skip and the last-prompted
		// timestamp. If GitHub published a newer release than the
		// one the user actively skipped, clear the skip so the new
		// version gets its own prompt — explicit SkippedVersion match
		// against the *new* tag.
		c.LastPromptedAt = prev.LastPromptedAt
		if prev.SkippedVersion != "" && prev.SkippedVersion == rel.Tag {
			c.SkippedVersion = prev.SkippedVersion
		}
	}
	if err := saveUpdateCheckCache(c); err != nil {
		reportCacheSaveError(err)
	}
	return c
}

// handleUpdatePrompt shows the 3-choice picker and dispatches the
// user's selection. Returns true iff the original command should be
// skipped (we ran `update` instead).
//
// last_prompted_at is stamped BEFORE the picker renders so a Ctrl-C
// / Esc out of the picker still applies the cooldown — otherwise the
// prompt would re-appear on the next invocation seconds later.
func handleUpdatePrompt(currentVer string, cache *updateCheckCache) bool {
	cache.LastPromptedAt = time.Now().Unix()
	if err := saveUpdateCheckCache(cache); err != nil {
		reportCacheSaveError(err)
	}

	// The picker's title combines the "version → version" notice and
	// the question, so the user sees one rendering instead of a
	// separate Printf line followed by a picker. Keeps the layout
	// compact on small terminals.
	title := fmt.Sprintf(i18n.T("update.notice"), currentVer, cache.LatestVersion) +
		"\n" + i18n.T("update.choice_prompt")
	choices := []string{
		choiceUpdate: i18n.T("update.choice_yes"),
		choiceLater:  i18n.T("update.choice_later"),
		choiceSkip:   i18n.T("update.choice_skip"),
	}
	idx, err := cliprompt.Pick(title, choices)
	if err != nil {
		// Esc / Ctrl-C / unknown selection — treat as "later". The
		// cooldown stamp above ensures we don't loop the prompt.
		return false
	}
	switch idx {
	case choiceUpdate:
		if err := Update(nil); err != nil {
			fmt.Fprintf(os.Stderr, "update: %v\n", err)
			return false
		}
		return true
	case choiceSkip:
		cache.SkippedVersion = cache.LatestVersion
		if err := saveUpdateCheckCache(cache); err != nil {
			reportCacheSaveError(err)
		}
	}
	return false
}
