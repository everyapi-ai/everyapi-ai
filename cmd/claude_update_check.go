package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

const (
	claudeUpdateCheckFilename  = "claude_update_check.json"
	claudeLatestVersionURL     = "https://downloads.claude.ai/claude-code-releases/latest"
	claudeUpdateCheckTimeout   = 2500 * time.Millisecond
	claudeUpdateFailureBackoff = time.Hour
	claudeUpdateNoticeCooldown = 12 * time.Hour
)

var claudeVersionPattern = regexp.MustCompile(`(?:^|[^0-9])v?([0-9]+\.[0-9]+\.[0-9]+)(?:[^0-9]|$)`)

type claudeUpdateCheckCache struct {
	CheckedAt          int64  `json:"checked_at,omitempty"`
	LastFetchAttemptAt int64  `json:"last_fetch_attempt_at,omitempty"`
	LatestVersion      string `json:"latest_version,omitempty"`
	NotifiedVersion    string `json:"notified_version,omitempty"`
	LastNotifiedAt     int64  `json:"last_notified_at,omitempty"`
}

var (
	claudeUpdateInteractiveFn = cliprompt.IsInteractive
	installedClaudeVersionFn  = installedClaudeVersion
	latestClaudeVersionFn     = latestClaudeVersion
	claudeUpdatePromptFn      = promptClaudeUpdate
	runClaudeUpdateFn         = runClaudeUpdate
	claudeUpdateErrorFn       = reportClaudeUpdateError
)

// maybeNotifyClaudeUpdate checks only the plain `everyapi use claude` launch. It is deliberately best-effort: version-command, network, and cache failures never prevent the requested Claude session from starting.
func maybeNotifyClaudeUpdate(toolName string) {
	if !strings.EqualFold(toolName, "claude") ||
		!claudeUpdateInteractiveFn() ||
		os.Getenv("EVERYAPI_NO_UPDATE_CHECK") == "1" {
		return
	}

	current, err := installedClaudeVersionFn()
	if err != nil || current == "" {
		return
	}

	cache, _ := loadClaudeUpdateCheckCache()
	latest := ""
	now := time.Now()
	lastAttemptFailed := cache != nil &&
		cache.LastFetchAttemptAt > cache.CheckedAt &&
		now.Sub(time.Unix(cache.LastFetchAttemptAt, 0)) < claudeUpdateFailureBackoff
	if lastAttemptFailed {
		latest = cache.LatestVersion
	} else {
		latest, err = latestClaudeVersionFn()
		cache = recordClaudeUpdateFetch(cache, latest, err, now)
		_ = saveClaudeUpdateCheckCache(cache)
	}

	if !claudeUpdatePromptable(current, latest, cache, now) {
		return
	}
	cache.NotifiedVersion = latest
	cache.LastNotifiedAt = now.Unix()
	_ = saveClaudeUpdateCheckCache(cache)
	updateNow, err := claudeUpdatePromptFn(current, latest)
	if err != nil || !updateNow {
		return
	}
	if err := runClaudeUpdateFn(); err != nil {
		claudeUpdateErrorFn(err)
	}
}

func reportClaudeUpdateError(err error) {
	fmt.Fprintf(os.Stderr, i18n.T("use.claude_update_failed")+"\n", err)
}

func promptClaudeUpdate(current, latest string) (bool, error) {
	title := fmt.Sprintf(i18n.T("use.claude_update_available"), current, latest) +
		"\n" + i18n.T("update.choice_prompt")
	idx, err := cliprompt.Pick(title, []string{
		i18n.T("update.choice_yes"),
		i18n.T("update.choice_later"),
	})
	return idx == 0, err
}

func runClaudeUpdate() error {
	c := exec.Command("claude", "update")
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	return c.Run()
}

func installedClaudeVersion() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), claudeUpdateCheckTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "claude", "--version").CombinedOutput()
	if err != nil {
		return "", err
	}
	version := parseClaudeVersion(string(out))
	if version == "" {
		return "", errors.New("unrecognized Claude Code version output")
	}
	return version, nil
}

func latestClaudeVersion() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), claudeUpdateCheckTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, claudeLatestVersionURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", errors.New("Claude Code latest-version endpoint returned " + resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 128))
	if err != nil {
		return "", err
	}
	version := parseClaudeVersion(string(body))
	if version == "" {
		return "", errors.New("Claude Code latest-version endpoint returned an invalid version")
	}
	return version, nil
}

func parseClaudeVersion(output string) string {
	match := claudeVersionPattern.FindStringSubmatch(output)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

func claudeUpdatePromptable(current, latest string, cache *claudeUpdateCheckCache, now time.Time) bool {
	if current == "" || latest == "" || compareSemver(current, latest) >= 0 {
		return false
	}
	return cache == nil || cache.NotifiedVersion != latest ||
		now.Sub(time.Unix(cache.LastNotifiedAt, 0)) >= claudeUpdateNoticeCooldown
}

func recordClaudeUpdateFetch(prev *claudeUpdateCheckCache, latest string, fetchErr error, now time.Time) *claudeUpdateCheckCache {
	cache := &claudeUpdateCheckCache{LastFetchAttemptAt: now.Unix()}
	if prev != nil {
		*cache = *prev
		cache.LastFetchAttemptAt = now.Unix()
	}
	if fetchErr == nil {
		cache.CheckedAt = now.Unix()
		cache.LatestVersion = latest
	}
	return cache
}

func claudeUpdateCheckCachePath() (string, error) {
	dir, err := config.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, claudeUpdateCheckFilename), nil
}

func loadClaudeUpdateCheckCache() (*claudeUpdateCheckCache, error) {
	path, err := claudeUpdateCheckCachePath()
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
	var cache claudeUpdateCheckCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, nil
	}
	return &cache, nil
}

func saveClaudeUpdateCheckCache(cache *claudeUpdateCheckCache) error {
	path, err := claudeUpdateCheckCachePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(cache)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
