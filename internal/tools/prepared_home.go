package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/procstate"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

const (
	preparedHomeMarker = "__EVERYAPI_PREPARED_HOME"
	preparedArgsMarker = "__EVERYAPI_PREPARED_SETTINGS_ARG"
	preparedArgvMarker = "__EVERYAPI_PREPARED_ARGV_JSON"
)

const (
	// preparedHomeOwnerFile records, one PID per line, the processes a prepared home belongs to — the `everyapi` process that created it and then the tool it launched — so a later launch can tell a home that is still in use from one orphaned by a hard kill.
	preparedHomeOwnerFile = ".everyapi-owner"
	// preparedHomeReapPrefix renames a condemned home out of the way. Tool prefixes passed to newPreparedHome are exec names, so no live home can start with a dot and collide.
	preparedHomeReapPrefix = ".reaping-"
)

// unownedPreparedHomeAge is how old a prepared home with no readable owner PID must be before the sweep reaps it. Two things produce one: a home created by a CLI build from before owner files existed, and a launch killed in the window between MkdirTemp and the owner write. Neither is distinguishable from a live session, so the floor has to outlast the longest such a session plausibly runs — a week is far past any real `everyapi use` sitting, and this rule only has to cover the upgrade window before every home carries an owner.
const unownedPreparedHomeAge = 7 * 24 * time.Hour

// abandonedPreparedHomeAge backstops PID reuse. A live-looking owner PID normally means the session is still working, but after a reboot the OS hands that same number to an unrelated process and the home would otherwise be pinned forever. Nothing keeps one `everyapi use` child alive for a month, so past this the "alive" answer is reuse rather than the owner.
const abandonedPreparedHomeAge = 30 * 24 * time.Hour

// preparedHomeRoot resolves the directory holding every process-scoped client home.
func preparedHomeRoot() (string, error) {
	root, err := config.ConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve everyapi config dir: %w", err)
	}
	return filepath.Join(root, "sessions"), nil
}

// newPreparedHome creates a process-scoped client home. Live catalog and loopback proxy configuration must not be shared between concurrent launches using different relay keys or groups.
func newPreparedHome(prefix string) (string, error) {
	root, err := preparedHomeRoot()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("create prepared client home root: %w", err)
	}
	// Reclaim homes orphaned by an earlier launch that never got to run its cleanup. Runs before MkdirTemp so our own fresh home is never a sweep candidate. Best-effort; see sweepStalePreparedHomes.
	sweepStalePreparedHomes(root)
	home, err := os.MkdirTemp(root, prefix+"-")
	if err != nil {
		return "", fmt.Errorf("create prepared %s home: %w", prefix, err)
	}
	// Stamp ownership immediately: from here on, a hard kill leaves a home the next launch can identify as orphaned instead of one that has to age out. A failed write is not fatal — the home just falls back to the unowned age rule.
	_ = os.WriteFile(filepath.Join(home, preparedHomeOwnerFile), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600)
	// Every process-scoped home gets the agent context files, here rather than in each adapter, because this is the single door all of them come through: a client added later inherits the reach without anyone remembering to wire it.
	//
	// This is reach, not a support claim. A client that reads the AGENTS.md convention out of its own home picks it up for free; one that does not sees an extra markdown file in a directory deleted when it exits. Clients whose documented surface is a named configuration field still point at EVERYAPI.md explicitly — that is the path that is actually guaranteed to be read.
	addAgentContextToHome(home)
	return home, nil
}

// launchPreparedHome carries the home from TakePreparedCleanup, which is the last place that knows it, to ExecWithOptions, which is the first place that knows the child's PID. One slot covers it: a CLI process launches exactly one tool.
var launchPreparedHome string

// adoptPreparedHome adds the launched tool to the home's owners.
//
// It is what makes the sweep's rule match the cleanup's. Cleanup removes a home once the CHILD has exited — see removePreparedHomeAfterQuiet, which even outlasts a worker recreating the directory. If the sweep only knew the parent, a parent killed with SIGKILL would hand its still-running tool's home to the next launch to delete; the child is reparented to init and keeps its own session and process group, so nothing else connects it back.
//
// A failed write is not fatal. The parent PID stays on file and the home just becomes reclaimable earlier than it should.
func adoptPreparedHome(childPID int) {
	if launchPreparedHome == "" || childPID <= 0 {
		return
	}
	file, err := os.OpenFile(filepath.Join(launchPreparedHome, preparedHomeOwnerFile), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(file, "%d\n", childPID)
	_ = file.Close()
}

// TakePreparedArgs removes the internal settings-path marker before the child receives its environment and returns the fixed argv prefix for tools whose official runtime-config surface is a command-line option.
func TakePreparedArgs(env map[string]string) []string {
	encoded := env[preparedArgvMarker]
	delete(env, preparedArgvMarker)
	if encoded != "" {
		var args []string
		if err := json.Unmarshal([]byte(encoded), &args); err == nil {
			return args
		}
		return nil
	}
	path := env[preparedArgsMarker]
	delete(env, preparedArgsMarker)
	if path == "" {
		return nil
	}
	return []string{"--settings", path}
}

func preparedHomeEnv(key, home string) map[string]string {
	return map[string]string{key: home, preparedHomeMarker: home}
}

// TakePreparedCleanup removes the internal marker before the child receives its environment and returns an idempotent cleanup for the generated home.
func TakePreparedCleanup(env map[string]string) func() {
	home := env[preparedHomeMarker]
	delete(env, preparedHomeMarker)
	if home == "" {
		return nil
	}
	root, err := preparedHomeRoot()
	if err != nil {
		return nil
	}
	rel, err := filepath.Rel(root, home)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return nil
	}
	launchPreparedHome = home
	var once sync.Once
	return func() {
		once.Do(func() {
			if err := removePreparedCodexProfile(home); err != nil {
				return
			}
			removePreparedHomeAfterQuiet(home)
		})
	}
}

func removePreparedHomeAfterQuiet(home string) {
	deadline := time.Now().Add(time.Second)
	var absentSince time.Time
	for {
		_ = os.RemoveAll(home)
		if _, err := os.Stat(home); os.IsNotExist(err) {
			if absentSince.IsZero() {
				absentSince = time.Now()
			} else if time.Since(absentSince) >= 100*time.Millisecond {
				return
			}
		} else {
			absentSince = time.Time{}
		}
		if time.Now().After(deadline) {
			_ = os.RemoveAll(home)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// sweepStalePreparedHomes reclaims prepared client homes orphaned by an `everyapi` process that died without running its cleanup — SIGKILL, an OOM kill, a reboot. Without it the directory only grows: a normal exit (including the child dying on a signal) removes its own home through TakePreparedCleanup, but a hard-killed parent leaves a Codex home behind at tens of thousands of files apiece.
//
// Nothing reachable is lost. Every launch mints a fresh MkdirTemp home and no launch ever looks inside an older one. Durable Codex rollouts live directly under its persistent CODEX_HOME; only the per-launch model catalog and profile bookkeeping live here.
//
// Every error is ignored: a sweep failure must never block a launch.
func sweepStalePreparedHomes(root string) {
	condemned := condemnStalePreparedHomes(root, time.Now())
	if len(condemned) == 0 {
		return
	}
	// The rename is what makes a home unreachable and it is O(1); the recursive delete is not, and running it inline would stall every launch behind the whole backlog. Detached so the launch proceeds at once — the process outlives the child, and a delete cut short by an early exit leaves a .reaping- directory the next sweep finishes unconditionally.
	go func() {
		for _, path := range condemned {
			if err := removePreparedCodexProfile(path); err != nil {
				continue
			}
			_ = os.RemoveAll(path)
		}
	}()
}

// condemnStalePreparedHomes renames every reapable home under root out of the live namespace and returns the renamed paths. Split from the deletion so tests can drive the policy without racing a background goroutine.
func condemnStalePreparedHomes(root string, now time.Time) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var condemned []string
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() {
			continue
		}
		if strings.HasPrefix(name, preparedHomeReapPrefix) {
			// An earlier sweep already condemned this one and exited before the delete finished. It is out of the live namespace by definition, so no age or owner check applies.
			condemned = append(condemned, filepath.Join(root, name))
			continue
		}
		home := filepath.Join(root, name)
		if !preparedHomeIsStale(home, entry, now) {
			continue
		}
		target := filepath.Join(root, preparedHomeReapPrefix+name)
		if err := os.Rename(home, target); err != nil {
			continue
		}
		condemned = append(condemned, target)
	}
	return condemned
}

// preparedHomeIsStale reports whether a prepared home has stopped belonging to a working launch. Keeping is the safe answer at every branch: reaping a live home breaks a running tool, while keeping a dead one costs one launch's worth of disk until the next sweep.
func preparedHomeIsStale(home string, entry os.DirEntry, now time.Time) bool {
	info, err := entry.Info()
	if err != nil {
		return false
	}
	// A live session's own writes land in subdirectories and in SQLite files it opened at startup, so this ModTime tracks top-level entry churn rather than real idleness. It is only ever used as a floor next to the owner check, never as the primary signal.
	age := now.Sub(info.ModTime())
	owners := readPreparedHomeOwners(home)
	if len(owners) == 0 {
		return age >= unownedPreparedHomeAge
	}
	for _, owner := range owners {
		if preparedHomeOwnerAlive(owner) {
			return age >= abandonedPreparedHomeAge
		}
	}
	return true
}

// preparedHomeOwnerAlive is procstate.Alive behind a seam, so the sweep's policy can be exercised for both answers without spawning and reaping real processes to manufacture them.
var preparedHomeOwnerAlive = procstate.Alive

// readPreparedHomeOwners returns every PID recorded for a home. An empty result means the home is unowned as far as the sweep is concerned — no file, an unreadable one, or nothing parseable in it — and the age rule decides instead.
func readPreparedHomeOwners(home string) []int {
	body, err := os.ReadFile(filepath.Join(home, preparedHomeOwnerFile))
	if err != nil {
		return nil
	}
	var owners []int
	for _, line := range strings.Split(string(body), "\n") {
		pid, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil || pid <= 0 {
			continue
		}
		owners = append(owners, pid)
	}
	return owners
}
