package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/relaya-ai/relaya-ai/internal/version"
)

// Update — check the GitHub mirror for a newer release and run the
// matching upgrade command on the install method we can detect.
//
// One command, one user action: typing `relaya update` should
// finish the upgrade, not hand the user a list of commands to
// copy-paste.
//
// Detection is path-based: os.Executable() resolves symlinks so we
// see the actual binary path, then we match against:
//
//   - "/Cellar/"           → Homebrew (any platform: /opt/homebrew/
//                            on Apple Silicon, /usr/local/Cellar/
//                            on Intel, /home/linuxbrew/ on Linux)
//   - $GOBIN / $GOPATH/bin → `go install`
//   - anything else        → unknown (curl / manual)
//
// Detected install methods exec the right tool's upgrade flow —
// `brew update && brew upgrade relaya` or `go install …@latest`.
// We never self-replace the binary: brew + go's own verification
// (SHA / module checksum) is more battle-tested than anything we
// could re-implement here, and self-replacing a running executable
// is platform-hostile (Windows in particular).
//
// For unknown install methods we fall back to printing the manual
// curl + SHA256 + cosign flow from the README — that's the same
// content as `--dry-run` but only shown when there's no install
// manager to delegate to.
//
// Flags:
//
//	--check     Don't exec anything, just compare versions and exit
//	            0 (up-to-date) or 1 (outdated). Suited for cron / CI.
//	--dry-run   Print what would be run, don't actually run it.
//	            Same content the previous version of this command
//	            always printed; kept as an escape hatch for users
//	            who want to inspect the command before running it.
func Update(args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	checkOnly := fs.Bool("check", false, "exit 0 if up-to-date, 1 if a newer version exists; no other output")
	dryRun := fs.Bool("dry-run", false, "print the upgrade command instead of running it")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(withCtx(), 10*time.Second)
	defer cancel()

	latest, err := fetchLatestVersion(ctx)
	if err != nil {
		return fmt.Errorf("check latest version: %w", err)
	}

	cmp := compareSemver(version.Version, latest)

	if *checkOnly {
		if cmp >= 0 {
			printf("up to date (%s)\n", version.Version)
			return nil
		}
		printf("update available: %s → %s\n", version.Version, latest)
		os.Exit(1)
		return nil // unreachable
	}

	if cmp > 0 {
		printf("\nYou're running a pre-release: %s (latest tag: %s)\n", version.Version, latest)
		printf("Nothing to do.\n")
		return nil
	}
	if cmp == 0 {
		printf("\nrelaya %s — up to date.\n", version.Version)
		return nil
	}

	// cmp < 0: outdated. Pick a method based on where the binary lives.
	method := detectInstallMethod()
	printf("\nUpdate available: %s → %s\n", version.Version, latest)
	printf("Install method:   %s\n\n", method)

	switch method {
	case installMethodBrew:
		return runBrewUpgrade(*dryRun)
	case installMethodGoInstall:
		return runGoInstallUpgrade(*dryRun)
	default:
		// Unknown install path — we can't safely auto-replace the
		// binary because we don't know where the user wants it.
		// Print the manual flow (curl + SHA256 + cosign) so they
		// can finish by hand without losing the README's verify
		// step.
		printUnknownInstallHint(latest)
		return nil
	}
}

// ---- install-method detection --------------------------------------

type installMethod string

const (
	installMethodBrew      installMethod = "Homebrew"
	installMethodGoInstall installMethod = "go install"
	installMethodUnknown   installMethod = "unknown (curl / manual)"
)

func detectInstallMethod() installMethod {
	exe, err := os.Executable()
	if err != nil {
		return installMethodUnknown
	}
	// Resolve symlinks: brew puts /opt/homebrew/bin/relaya as a
	// symlink to /opt/homebrew/Cellar/relaya/X.Y.Z/bin/relaya;
	// without resolving we'd match "/bin/" not "/Cellar/".
	if resolved, lerr := filepath.EvalSymlinks(exe); lerr == nil {
		exe = resolved
	}
	if strings.Contains(exe, string(os.PathSeparator)+"Cellar"+string(os.PathSeparator)) {
		return installMethodBrew
	}
	for _, dir := range goBinDirs() {
		if dir == "" {
			continue
		}
		if strings.HasPrefix(exe, dir+string(os.PathSeparator)) {
			return installMethodGoInstall
		}
	}
	return installMethodUnknown
}

// goBinDirs returns the candidate directories `go install` may have
// placed the binary in, ordered from most-specific to fallback. We
// don't shell out to `go env GOPATH` because that requires the user
// to have go installed on the running machine, which is not true for
// release binary users.
func goBinDirs() []string {
	out := []string{}
	if v := os.Getenv("GOBIN"); v != "" {
		out = append(out, v)
	}
	if v := os.Getenv("GOPATH"); v != "" {
		for _, p := range filepath.SplitList(v) {
			out = append(out, filepath.Join(p, "bin"))
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, "go", "bin"))
	}
	return out
}

// ---- upgrade runners -----------------------------------------------

// runCmd executes a foreground command, inheriting stdio. Returns
// the underlying exec error verbatim (caller wraps for context).
func runCmd(name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

func runBrewUpgrade(dryRun bool) error {
	if dryRun {
		printf("  brew update && brew upgrade relaya\n")
		return nil
	}
	// `brew update` first — without it `brew upgrade relaya`
	// reads the local cached formula and reports "already
	// installed at <old version>" even when a newer release is
	// public. Two separate exec calls (not `bash -c "… && …"`)
	// so the user can see each step's output cleanly.
	printf("$ brew update\n")
	if err := runCmd("brew", "update"); err != nil {
		return fmt.Errorf("brew update: %w", err)
	}
	printf("\n$ brew upgrade relaya\n")
	if err := runCmd("brew", "upgrade", "relaya"); err != nil {
		return fmt.Errorf("brew upgrade relaya: %w", err)
	}
	printf("\nDone. Run `relaya version` to confirm.\n")
	return nil
}

func runGoInstallUpgrade(dryRun bool) error {
	const pkg = "github.com/relaya-ai/relaya-ai@latest"
	if dryRun {
		printf("  go install %s\n", pkg)
		return nil
	}
	printf("$ go install %s\n", pkg)
	if err := runCmd("go", "install", pkg); err != nil {
		return fmt.Errorf("go install: %w", err)
	}
	printf("\nDone. Run `relaya version` to confirm.\n")
	return nil
}

func printUnknownInstallHint(latest string) {
	binaryPath := guessBinaryPath()
	printf("Can't auto-upgrade — your binary isn't under a Cellar/ (brew) or\n")
	printf("$GOBIN/$GOPATH/bin path we recognise. Pick one:\n\n")

	printf("  # Homebrew (after `brew tap relaya-ai/tap`)\n")
	printf("  brew update && brew upgrade relaya\n\n")
	printf("  # go install\n")
	printf("  go install github.com/relaya-ai/relaya-ai@latest\n\n")
	printf("  # Direct binary (current platform: %s/%s)\n", runtime.GOOS, runtime.GOARCH)
	printf("  curl -L -o %s.new \\\n", binaryPath)
	printf("    https://github.com/relaya-ai/relaya-ai/releases/download/%s/relaya_%s_%s.tar.gz\n",
		latest, runtime.GOOS, runtime.GOARCH)
	printf("  # verify SHA256 + cosign per README before replacing the binary\n")

	printf("\nRelease notes: https://github.com/relaya-ai/relaya-ai/releases/tag/%s\n", latest)
}

// guessBinaryPath returns the binary's full on-disk path for use in
// the manual upgrade hint. Falls back to a generic placeholder if
// os.Executable fails. Purely cosmetic.
func guessBinaryPath() string {
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return "~/.local/bin/relaya"
}

// ---- latest version lookup -----------------------------------------

// latestReleasePollURL is the GitHub Releases API endpoint for the
// public mirror. Pinned to the mirror (not the monorepo) because
// the monorepo is private and would 404 for token-less callers.
const latestReleasePollURL = "https://api.github.com/repos/relaya-ai/relaya-ai/releases/latest"

// errNoReleaseYet surfaces when the mirror's Releases endpoint is
// empty (pre-v0.1.0 state, or a brand-new install pre-first-release).
// Distinct sentinel so the caller can print "no releases yet" rather
// than the raw `tag_name field empty` error.
var errNoReleaseYet = errors.New("no public release tagged yet")

func fetchLatestVersion(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", latestReleasePollURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	// User-Agent is recommended by the GitHub API even for
	// unauthenticated callers; an empty UA can trip the abuse rate
	// limit harder than a recognisable one.
	req.Header.Set("User-Agent", "relaya-cli/"+version.Version)

	hc := &http.Client{Timeout: 10 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", errNoReleaseYet
	}
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("github releases API returned %d", resp.StatusCode)
	}
	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	tag := strings.TrimSpace(payload.TagName)
	if tag == "" {
		return "", errNoReleaseYet
	}
	return tag, nil
}

// ---- semver compare ------------------------------------------------

// compareSemver returns -1 / 0 / +1 in the same direction as
// strings.Compare, treating each numeric dotted segment of the
// version as a number rather than a string.
//
//   - "dev" / "unknown" / anything non-`vX.Y.Z` sorts BEFORE every
//     real release, so `update` from a dev build always reports
//     "update available" — the right thing.
//   - Pre-release suffixes (-rc1, -beta) are ignored for ordering;
//     the comparison stops at the first three numeric segments.
//     A v0.2.0-rc1 binary running `update` will be told v0.2.0 is
//     newer, which is correct under semver.
//
// We don't import a semver library because the CLI's whole module
// has zero third-party deps and we'd like to keep it that way for
// supply-chain reasons.
func compareSemver(a, b string) int {
	pa := parseSemver(a)
	pb := parseSemver(b)
	for i := 0; i < 3; i++ {
		if pa[i] < pb[i] {
			return -1
		}
		if pa[i] > pb[i] {
			return 1
		}
	}
	return 0
}

// parseSemver extracts up to three numeric segments. A leading 'v'
// is stripped; pre-release / build suffix is discarded at the first
// '-' or '+'. Non-numeric or missing segments come back as -1 so
// they sort before any real release.
func parseSemver(s string) [3]int {
	out := [3]int{-1, -1, -1}
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	// Strip pre-release / build metadata.
	for _, sep := range []string{"-", "+"} {
		if i := strings.Index(s, sep); i >= 0 {
			s = s[:i]
		}
	}
	parts := strings.SplitN(s, ".", 3)
	for i, p := range parts {
		if i >= 3 {
			break
		}
		n := 0
		ok := false
		for _, r := range p {
			if r < '0' || r > '9' {
				ok = false
				break
			}
			n = n*10 + int(r-'0')
			ok = true
		}
		if ok {
			out[i] = n
		}
	}
	return out
}
