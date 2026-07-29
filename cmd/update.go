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
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/internal/version"
)

// Update — check the GitHub mirror for a newer release and run the
// matching upgrade command on the install method we can detect.
//
// One command, one user action: typing `everyapi version update` should
// finish the upgrade, not hand the user a list of commands to
// copy-paste.
//
// Detection is path-based: os.Executable() resolves symlinks so we
// see the actual binary path, then we match against:
//
//   - "/Cellar/"           → Homebrew (any platform: /opt/homebrew/
//     on Apple Silicon, /usr/local/Cellar/
//     on Intel, /home/linuxbrew/ on Linux)
//   - $GOBIN / $GOPATH/bin → `go install`
//   - the install script's default bin dir → install script
//     (%LOCALAPPDATA%\everyapi\bin from install.ps1 on Windows,
//     ~/.local/bin from install.sh elsewhere)
//   - anything else        → unknown (curl / manual)
//
// Detected package managers exec the right tool's upgrade flow —
// `brew update && brew upgrade everyapi` or `go install …@latest`.
// We never self-replace the binary: brew + go's own verification
// (SHA / module checksum) is more battle-tested than anything we
// could re-implement here, and self-replacing a running executable
// is platform-hostile (Windows in particular).
//
// Install-script installs run the published installer after the user
// explicitly chooses "Update now" (or invokes `version update`). The
// installers already handle replacement safely: install.sh uses an
// atomic rename and install.ps1 moves a locked running executable to
// .old before copying the new binary into place.
//
// For unknown install methods we fall back to printing the manual
// download + SHA256 + cosign flow from the README — that's the same
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
	_, err := updateRun(args)
	return err
}

// updateRun is Update's body. The extra `dispatched` return reports
// whether an upgrade was ACTUALLY handed to an install manager (brew /
// go install ran, not dry-run): every informational early return —
// up-to-date, prerelease, dev build, hint-only script/unknown paths —
// is false. handleUpdatePrompt keys "swallow the user's original
// command" off this real outcome; re-deriving it from
// detectInstallMethod() would swallow the command even when Update
// printed "up to date" and ran nothing.
func updateRun(args []string) (dispatched bool, err error) {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	checkOnly := fs.Bool("check", false, "exit 0 if up-to-date, 1 if a newer version exists; no other output")
	dryRun := fs.Bool("dry-run", false, "print the upgrade command instead of running it")
	if err := fs.Parse(args); err != nil {
		return false, err
	}
	if err := rejectFlagPositionals(fs); err != nil {
		return false, err
	}

	ctx, cancel := context.WithTimeout(cliout.WithCtx(), 10*time.Second)
	defer cancel()

	// Resolve the latest tag via the github.com redirect, NOT the
	// api.github.com JSON endpoint: the redirect isn't subject to the
	// 60-req/hour unauthenticated API rate limit, so `update` keeps
	// working on a shared NAT whose API bucket is already drained by
	// other users behind the same IP. The changelog body is API-only
	// and fetched best-effort in the outdated branch below.
	latest, err := fetchLatestTag(ctx)
	if err != nil {
		if *checkOnly {
			// --check is cron/CI-facing: exit 0 = up-to-date, 1 = outdated.
			// A lookup failure must NOT masquerade as "outdated" (exit 1), or
			// a transient network blip reads as an available upgrade. Reserve
			// exit 2 for "couldn't determine" and say why on stderr.
			fmt.Fprintf(os.Stderr, "could not check latest version: %v\n", err)
			os.Exit(2)
		}
		return false, fmt.Errorf("check latest version: %w", err)
	}

	ver, commit := version.Resolve()

	// Dev builds (local `go build` without -ldflags stamping) shouldn't
	// be force-marched onto a release tarball — the natural upgrade is
	// `git pull && go build`, not `curl | tar` overwriting their dev
	// binary. Detect and short-circuit with a tailored message.
	if ver == "dev" {
		if *checkOnly {
			// Script mode is deliberately silent; callers consume only the
			// documented exit status. A dev build has no ordered release
			// version to compare, so preserve the existing success status.
			return false, nil
		}
		cliout.Printf(i18n.T("update.dev_build_intro"), commit, latest)
		cliout.Println("")
		cliout.Println(i18n.T("update.rebuild_header"))
		cliout.Println(i18n.T("update.rebuild_cmd"))
		cliout.Println("")
		cliout.Println(i18n.T("update.switch_header"))
		cliout.Println(i18n.T("update.switch_cmd_brew"))
		cliout.Println(i18n.T("update.switch_cmd_go"))
		return false, nil
	}

	cmp := compareSemver(ver, latest)

	if *checkOnly {
		if cmp >= 0 {
			return false, nil
		}
		os.Exit(1)
		return false, nil // unreachable
	}

	if cmp > 0 {
		cliout.Printf("\n"+i18n.T("update.prerelease")+"\n", ver, latest)
		cliout.Println(i18n.T("update.nothing_to_do"))
		return false, nil
	}
	if cmp == 0 {
		cliout.Printf("\n"+i18n.T("update.up_to_date")+"\n", ver)
		return false, nil
	}

	// cmp < 0: outdated. Pick a method based on where the binary lives.
	method := detectInstallMethod()
	cliout.Printf("\n"+i18n.T("update.update_available")+"\n", ver, latest)
	renderChangelog(changelogRelease(ctx, latest))
	cliout.Printf(i18n.T("update.install_method")+"\n\n", methodDisplayName(method))

	switch method {
	case installMethodBrew:
		// dry-run only prints the command — nothing was dispatched.
		return !*dryRun, runBrewUpgrade(*dryRun)
	case installMethodGoInstall:
		return !*dryRun, runGoInstallUpgrade(*dryRun)
	case installMethodScript:
		return !*dryRun, runInstallScriptUpgrade(*dryRun)
	default:
		// Unknown install path — we can't safely auto-replace the
		// binary because we don't know where the user wants it.
		// Print the manual flow (download + SHA256 + cosign) so they
		// can finish by hand without losing the README's verify
		// step.
		printUnknownInstallHint(latest)
		return false, nil
	}
}

// ---- install-method detection --------------------------------------

type installMethod string

// installMethod values are internal identifiers. Brew and go-install
// double as display labels (proper names, identical in every language);
// script/unknown display text lives in the update.method_* locale keys
// via methodDisplayName — don't put user-facing prose here.
const (
	installMethodBrew      installMethod = "Homebrew"
	installMethodGoInstall installMethod = "go install"
	installMethodScript    installMethod = "script"
	installMethodUnknown   installMethod = "unknown"
)

// methodDisplayName localizes the install-method label shown after
// "Install method:". The installMethod constants stay English — they
// are internal identifiers — and "Homebrew" / "go install" are proper
// names that read the same in every language, so only the script and
// unknown labels get a translation.
func methodDisplayName(m installMethod) string {
	switch m {
	case installMethodScript:
		return i18n.T("update.method_script")
	case installMethodUnknown:
		return i18n.T("update.method_unknown")
	default:
		return string(m)
	}
}

func detectInstallMethod() installMethod {
	exe, err := os.Executable()
	if err != nil {
		return installMethodUnknown
	}
	// Resolve symlinks: brew puts /opt/homebrew/bin/everyapi as a
	// symlink to /opt/homebrew/Cellar/everyapi/X.Y.Z/bin/everyapi;
	// without resolving we'd match "/bin/" not "/Cellar/".
	if resolved, lerr := filepath.EvalSymlinks(exe); lerr == nil {
		exe = resolved
	}
	return classifyExePath(exe)
}

// classifyExePath maps a resolved binary path to an install method.
// Split from detectInstallMethod so tests can feed synthetic paths —
// a `go test` binary's own os.Executable() never lands in any of the
// recognised locations.
func classifyExePath(exe string) installMethod {
	exe = filepath.Clean(exe)
	if strings.Contains(exe, string(os.PathSeparator)+"Cellar"+string(os.PathSeparator)) {
		return installMethodBrew
	}
	// Only classify as go-install when a Go toolchain is actually on
	// PATH: a binary can sit under ~/go/bin (a common ad-hoc bin dir)
	// on a machine with no Go, and `runGoInstallUpgrade` would then exec
	// `go install …` and die with `exec: "go": executable file not
	// found`. Without a toolchain, fall through to the manual hint.
	if _, gerr := exec.LookPath("go"); gerr == nil {
		for _, dir := range goBinDirs() {
			if dir == "" {
				continue
			}
			if hasDirPrefix(exe, dir) {
				return installMethodGoInstall
			}
		}
	}
	// Install-script default target dirs. Checked AFTER go install so a
	// GOBIN deliberately pointed at one of these still upgrades via
	// `go install` (which is what would actually overwrite the binary).
	exeDir := filepath.Dir(exe)
	for _, dir := range scriptBinDirs() {
		if pathsEqual(exeDir, dir) {
			return installMethodScript
		}
	}
	return installMethodUnknown
}

// scriptBinDirs returns the DEFAULT bin dir the platform's install
// script drops the binary into: %LOCALAPPDATA%\everyapi\bin from
// install.ps1 (falling back to <home>\AppData\Local like the script
// does), ~/.local/bin from install.sh.
//
// Unix deliberately omits /usr/local/bin: install.sh does target it
// when run as root, but it's also where manual tarball installs
// conventionally land, and misclassifying those as "install script"
// would hint a non-sudo re-run that installs a SECOND binary into
// ~/.local/bin instead of upgrading the one in place. ~/.local/bin is
// unambiguous. Custom --prefix / -Prefix installs are undetectable by
// design and stay "unknown".
func scriptBinDirs() []string {
	if runtime.GOOS == "windows" {
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil
			}
			base = filepath.Join(home, "AppData", "Local")
		}
		return []string{filepath.Join(base, "everyapi", "bin")}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{filepath.Join(home, ".local", "bin")}
}

// pathsEqual compares two cleaned paths for equality — case-
// insensitively on Windows, where the filesystem is case-preserving
// but case-insensitive and %LOCALAPPDATA% casing varies by source.
func pathsEqual(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// hasDirPrefix reports whether exe (already Clean'd) sits under dir at
// any depth, with the same normalization pathsEqual applies: dir is
// Clean'd (a raw GOBIN can carry a trailing separator that would
// otherwise never match) and the comparison folds case on Windows,
// where EvalSymlinks returns on-disk casing while the user's env keeps
// whatever casing they typed.
func hasDirPrefix(exe, dir string) bool {
	prefix := filepath.Clean(dir) + string(os.PathSeparator)
	if len(exe) < len(prefix) {
		return false
	}
	head := exe[:len(prefix)]
	if runtime.GOOS == "windows" {
		return strings.EqualFold(head, prefix)
	}
	return head == prefix
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
		cliout.Printf("  brew update && brew upgrade everyapi\n")
		return nil
	}
	// `brew update` first — without it `brew upgrade everyapi`
	// reads the local cached formula and reports "already
	// installed at <old version>" even when a newer release is
	// public. Two separate exec calls (not `bash -c "… && …"`)
	// so the user can see each step's output cleanly.
	cliout.Printf("$ brew update\n")
	if err := runCmd("brew", "update"); err != nil {
		return fmt.Errorf("brew update: %w", err)
	}
	cliout.Printf("\n$ brew upgrade everyapi\n")
	if err := runCmd("brew", "upgrade", "everyapi"); err != nil {
		return fmt.Errorf("brew upgrade everyapi: %w", err)
	}
	cliout.Printf("\n%s\n", i18n.T("update.done_confirm"))
	return nil
}

func runGoInstallUpgrade(dryRun bool) error {
	const pkg = "github.com/everyapi-ai/everyapi-ai@latest"
	if dryRun {
		cliout.Printf("  go install %s\n", pkg)
		return nil
	}
	cliout.Printf("$ go install %s\n", pkg)
	if err := runCmd("go", "install", pkg); err != nil {
		return fmt.Errorf("go install: %w", err)
	}
	cliout.Printf("\n%s\n", i18n.T("update.done_confirm"))
	return nil
}

// updateCommandRunner is replaceable in tests so the install-script path can
// assert real dispatch without downloading or executing a remote script.
var updateCommandRunner = runCmd

func runInstallScriptUpgrade(dryRun bool) error {
	if dryRun {
		cliout.Printf("  %s\n", installScriptOneLiner())
		return nil
	}

	name, args := installScriptCommand()
	cliout.Printf("$ %s\n", installScriptOneLiner())
	if err := updateCommandRunner(name, args...); err != nil {
		return fmt.Errorf("install script: %w", err)
	}
	cliout.Printf("\n%s\n", i18n.T("update.done_confirm"))
	return nil
}

// installScriptCommand turns the documented one-liner into the platform's
// foreground shell invocation. The command text is a fixed application
// constant; no release metadata or user input is interpolated into it.
func installScriptCommand() (string, []string) {
	return installScriptCommandFor(runtime.GOOS)
}

func installScriptCommandFor(goos string) (string, []string) {
	oneLiner := installScriptOneLinerFor(goos)
	if goos == "windows" {
		return "powershell.exe", []string{"-NoProfile", "-Command", oneLiner}
	}
	return "bash", []string{"-o", "pipefail", "-c", oneLiner}
}

// installScriptOneLiner returns the platform's copy-paste install/
// upgrade command — the same one the README and docs publish.
func installScriptOneLiner() string {
	return installScriptOneLinerFor(runtime.GOOS)
}

func installScriptOneLinerFor(goos string) string {
	if goos == "windows" {
		return "irm https://dl.everyapi.ai/install.ps1 | iex"
	}
	return "curl -fsSL https://dl.everyapi.ai/install.sh | bash"
}

// releaseAssetName is the goreleaser archive for the running platform
// — zip on Windows, tar.gz elsewhere (.goreleaser.yml format_overrides).
func releaseAssetName() string {
	return releaseAssetNameFor(runtime.GOOS, runtime.GOARCH)
}

func releaseAssetNameFor(goos, goarch string) string {
	if goos == "windows" {
		return fmt.Sprintf("everyapi_windows_%s.zip", goarch)
	}
	return fmt.Sprintf("everyapi_%s_%s.tar.gz", goos, goarch)
}

func printUnknownInstallHint(latest string) {
	// Install-script path comes first because it's the most common
	// install method for users who land in the "unknown" branch (a
	// custom --prefix / -Prefix puts the binary outside every dir
	// detectInstallMethod() knows). Re-running it upgrades in place,
	// so it's the closest thing to a one-liner upgrade for
	// non-brew/non-go users. Everything here is per-platform: bash
	// script + brew mean nothing on Windows, install.ps1 means
	// nothing elsewhere.
	// No `go install` entry: it's the nichest of the install paths, and
	// anyone who actually installed that way sits under $GOBIN and never
	// reaches this hint — detectInstallMethod dispatches their upgrade
	// through `go install` automatically. The comment lines are i18n'd;
	// the commands and URLs themselves are not (they're literal input).
	if runtime.GOOS == "windows" {
		cliout.Printf("%s\n", i18n.T("update.cant_auto_windows"))
		cliout.Printf("%s\n", i18n.T("update.hint_script_windows"))
		cliout.Printf("  irm https://dl.everyapi.ai/install.ps1 | iex\n\n")
	} else {
		cliout.Printf("%s\n", i18n.T("update.cant_auto"))
		cliout.Printf("%s\n", i18n.T("update.hint_script_unix"))
		cliout.Printf("  curl -fsSL https://dl.everyapi.ai/install.sh | bash\n\n")
		cliout.Printf("%s\n", i18n.T("update.hint_brew"))
		cliout.Printf("  brew update && brew upgrade everyapi\n\n")
	}
	// A bare URL, not a constructed curl command: the download is an
	// ARCHIVE (zip/tar.gz), not the binary itself, so the old
	// `curl -o <exe>.new <…tar.gz>` line was wrong twice over — and
	// POSIX line continuations don't paste into PowerShell anyway.
	cliout.Printf(i18n.T("update.hint_direct")+"\n", runtime.GOOS, runtime.GOARCH)
	cliout.Printf(i18n.T("update.hint_direct_replace")+"\n", guessBinaryPath())
	cliout.Printf("  https://github.com/everyapi-ai/everyapi-ai/releases/download/%s/%s\n", latest, releaseAssetName())

	cliout.Printf("\n"+i18n.T("update.release_notes")+"\n", releaseTagURL(latest))
}

// guessBinaryPath returns the binary's full on-disk path for use in
// the manual upgrade hint. Falls back to a generic placeholder if
// os.Executable fails. Purely cosmetic.
func guessBinaryPath() string {
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return "~/.local/bin/everyapi"
}

// ---- latest version lookup -----------------------------------------

// latestReleasePollURL is the GitHub Releases API endpoint for the
// public CLI repo. Token-less GETs against this endpoint are
// rate-limited but don't require auth, which is what `everyapi version update`
// relies on for the unauthenticated version check.
// latestReleasePollURL hits the PUBLIC CLI mirror (everyapi-ai/
// everyapi-ai) where the release pipeline actually publishes
// tags + binary assets. The monorepo at everyapi-ai/everyapi has
// no v* tags or releases — it's the source of truth, the mirror
// is the distribution channel. cli-release.yml's mirror step
// pushes the rewritten clients/cli/ tree + a v* tag and
// GoReleaser attaches the platform tarballs there.
// var, not const, so update_test.go can swap it to an httptest
// server URL. Production callers never reassign.
var latestReleasePollURL = "https://api.github.com/repos/everyapi-ai/everyapi-ai/releases/latest"

// latestReleaseRedirectURL is the github.com (NOT api.github.com)
// "latest release" web endpoint. A GET returns a 302 whose Location
// header points at .../releases/tag/<tag>. Crucially this redirect is
// NOT subject to the api.github.com 60-req/hour unauthenticated rate
// limit, so the silent pre-command auto-check and `update`'s version
// compare can poll it freely even when the API bucket is exhausted by
// other users on a shared NAT. The cost: it yields only the tag, so
// the changelog body still comes from fetchLatestRelease (API),
// called best-effort.
// var, not const, so update_test.go can point it at an httptest
// server. Production callers never reassign.
var latestReleaseRedirectURL = "https://github.com/everyapi-ai/everyapi-ai/releases/latest"

// releaseTagURL builds the canonical release-page URL for a tag.
// Used as the HTMLURL fallback when the API body fetch is skipped /
// rate-limited, so the user still gets a link to the release notes.
func releaseTagURL(tag string) string {
	return "https://github.com/everyapi-ai/everyapi-ai/releases/tag/" + tag
}

// changelogRelease resolves the release whose body renderChangelog
// shows in the outdated branch. The body lives only behind the
// rate-limited API (fetchLatestRelease); best-effort here means a
// drained bucket — or any API error — degrades to a body-less release
// whose HTMLURL is derived from the tag, so renderChangelog falls
// through to its "no notes — see <url>" path instead of the whole
// upgrade aborting. Pulled out of Update so the fallback is unit-
// testable without execing brew / go install.
func changelogRelease(ctx context.Context, tag string) *latestRelease {
	if rel, err := fetchLatestRelease(ctx); err == nil {
		return rel
	}
	return &latestRelease{Tag: tag, HTMLURL: releaseTagURL(tag)}
}

// errNoReleaseYet surfaces when the mirror's Releases endpoint is
// empty (pre-v0.1.0 state, or a brand-new install pre-first-release).
// Distinct sentinel so the caller can print "no releases yet" rather
// than the raw `tag_name field empty` error.
var errNoReleaseYet = errors.New("no public release tagged yet")

// latestRelease is the subset of GitHub's release payload we surface
// in the update flow. Body is the auto-generated changelog goreleaser
// produced; HTMLURL points at the release page so a user who wants
// more context (compare link, assets) can open it.
type latestRelease struct {
	Tag     string
	Body    string
	HTMLURL string
}

// fetchLatestRelease pulls /releases/latest from the mirror and
// returns the tag plus the body. Used by Update() to render the
// changelog BEFORE handing off to brew / go install, so the user
// gets the "what's new in this upgrade" preview the dashboard
// release page would normally give them.
//
// Auth: a GITHUB_TOKEN env var, when set, bumps the unauthenticated
// 60-req/hour rate limit to authenticated 5000 req/hour. Most CLI
// users won't have it set and the unauthenticated path stays the
// default — but on shared NATs (offices, CI, conference Wi-Fi) the
// 60/hour bucket is shared across every user behind the same IP and
// runs out fast. When that happens the rate-limit branch below
// surfaces a specific "rate limit exceeded; resets at HH:MM:SS"
// hint instead of a bare HTTP 403.
func fetchLatestRelease(ctx context.Context) (*latestRelease, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", latestReleasePollURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	// User-Agent is recommended by the GitHub API even for
	// unauthenticated callers; an empty UA can trip the abuse rate
	// limit harder than a recognisable one.
	req.Header.Set("User-Agent", "everyapi-cli/"+version.Version)
	// Mirror the gh CLI's env-var preference: GH_TOKEN wins over
	// GITHUB_TOKEN. Users who set up `gh auth login` end up with
	// GH_TOKEN in their shell; CI / GitHub Actions injects
	// GITHUB_TOKEN. Either should Just Work without the user
	// re-exporting between the two.
	if tok := strings.TrimSpace(firstNonEmpty(os.Getenv("GH_TOKEN"), os.Getenv("GITHUB_TOKEN"))); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	hc := &http.Client{Timeout: 10 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errNoReleaseYet
	}
	if resp.StatusCode/100 != 2 {
		return nil, githubAPIError(resp)
	}
	var payload struct {
		TagName string `json:"tag_name"`
		Body    string `json:"body"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	tag := strings.TrimSpace(payload.TagName)
	if tag == "" {
		return nil, errNoReleaseYet
	}
	return &latestRelease{Tag: tag, Body: payload.Body, HTMLURL: payload.HTMLURL}, nil
}

// fetchLatestTag resolves the latest release tag via the github.com
// redirect (latestReleaseRedirectURL) — the rate-limit-safe path that
// never touches api.github.com. Returns errNoReleaseYet when the repo
// has no published release (the redirect lands on the releases index
// or 404s instead of a /releases/tag/<tag> URL).
//
// Used by both the manual `update` flow (for the version compare) and
// the silent auto-check, so neither pays the API's 60/hour bucket for
// the cheap "what's the latest version" question.
func fetchLatestTag(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", latestReleaseRedirectURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "everyapi-cli/"+version.Version)
	// Don't follow the redirect — the tag is in the Location header of
	// the 302 itself; following it would fetch the full HTML release
	// page for nothing.
	//
	// This assumes a SINGLE hop: github.com answers /releases/latest
	// with one 302 straight to /releases/tag/<tag> (verified). If
	// GitHub ever inserts an intermediate redirect (scheme / host
	// normalization), we'd read that hop's Location, miss the
	// /releases/tag/ marker, and fall through to errNoReleaseYet —
	// `update` then reports "no release yet" (not a wrong version) and
	// the auto-check backs off. Acceptable; not worth chasing N hops
	// for a path GitHub serves in one.
	hc := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", errNoReleaseYet
	}
	loc := resp.Header.Get("Location")
	if resp.StatusCode/100 != 3 || loc == "" {
		return "", fmt.Errorf("github releases redirect returned %d", resp.StatusCode)
	}
	const marker = "/releases/tag/"
	i := strings.LastIndex(loc, marker)
	if i < 0 {
		// Redirected somewhere without a tag (e.g. the releases index)
		// — the repo has no published release yet.
		return "", errNoReleaseYet
	}
	tag := strings.Trim(strings.TrimSpace(loc[i+len(marker):]), "/")
	if tag == "" {
		return "", errNoReleaseYet
	}
	return tag, nil
}

// firstNonEmpty returns the first non-empty string argument. Used
// for the GH_TOKEN / GITHUB_TOKEN preference order; lifted into its
// own helper because the test wants to assert the preference too.
func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// githubAPIError wraps a non-2xx response into the most actionable
// error we can build from the headers. Specifically: a 403 with
// X-RateLimit-Remaining: 0 is the "you hit the unauthenticated
// 60/hour bucket" case — surface the reset time + the GITHUB_TOKEN
// escape hatch instead of the bare status code. Everything else
// falls through to a generic "github API returned N" string.
func githubAPIError(resp *http.Response) error {
	if resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0" {
		hint := "set GITHUB_TOKEN env var to authenticate (bumps the limit from 60 to 5000 req/hour)"
		if reset := resp.Header.Get("X-RateLimit-Reset"); reset != "" {
			if ts, err := strconv.ParseInt(reset, 10, 64); err == nil {
				wait := time.Until(time.Unix(ts, 0))
				if wait < 0 {
					wait = 0
				}
				return fmt.Errorf("github rate-limit exhausted; resets in %s (%s) — %s",
					wait.Round(time.Second), time.Unix(ts, 0).Format("15:04:05"), hint)
			}
		}
		return fmt.Errorf("github rate-limit exhausted — %s", hint)
	}
	return fmt.Errorf("github releases API returned %d", resp.StatusCode)
}

// renderChangelog prints the release body (goreleaser's auto-
// generated changelog) between "Update available:" and "Install
// method:" so the user gets a preview of what they're upgrading
// to without having to open GitHub.
//
// The body is GitHub-flavoured markdown, which a terminal can't
// render — dumped raw it shows literal "##", "**", and "```" fences.
// cleanReleaseNotes strips those down to plain text (and drops the
// install snippet + diff link the upgrade flow makes redundant); see
// its doc for the exact rules. We deliberately don't pull in a
// markdown renderer — a heavy dep for a few-line changelog box.
//
// Empty body — typical for backfilled releases or releases cut
// before goreleaser's changelog block was tightened up — we just
// print the release URL so the user can read the diff manually
// instead of staring at an empty paragraph.
func renderChangelog(rel *latestRelease) {
	cliout.Println("")
	cliout.Println(i18n.T("update.whats_new"))
	lines := cleanReleaseNotes(rel.Body)
	if len(lines) == 0 {
		cliout.Println(i18n.T("update.no_notes"))
	} else {
		// Indent each line so the changelog block is visually
		// distinct from the "Update available:" and "Install
		// method:" lines that bracket it. Blank separators stay
		// flush-left so the indent doesn't leave trailing spaces.
		for _, line := range lines {
			if line == "" {
				cliout.Println("")
			} else {
				// The release body is GitHub/goreleaser's changelog, built
				// from merged PR titles and commit subjects — server-relayed,
				// contributor-influenceable text. cleanReleaseNotes only
				// strips markdown, not ESC/CSI/OSC or C0/C1 controls, so
				// sanitize before printing like every other untrusted sink.
				cliout.Printf("  %s\n", cliout.Sanitize(line))
			}
		}
	}
	if rel.HTMLURL != "" {
		cliout.Printf("\n"+i18n.T("update.full_release")+"\n", cliout.Sanitize(rel.HTMLURL))
	}
	cliout.Println("──────────────────")
	cliout.Println("")
}

// Markdown constructs goreleaser actually emits in a release body.
// Compiled once at package load; cleanReleaseNotes applies them per
// line. Kept intentionally small — this is a plain-text reducer for a
// known generator, not a general markdown parser.
var (
	mdHeadingRe    = regexp.MustCompile(`^\s*#{1,6}\s+`)           // "## Foo"  -> "Foo"
	mdBulletRe     = regexp.MustCompile(`^(\s*)[-*+]\s+`)          // "*  x"    -> "• x"
	mdBoldItalicRe = regexp.MustCompile(`\*\*\*(.+?)\*\*\*`)       // "***x***" -> "x"
	mdBoldRe       = regexp.MustCompile(`\*\*(.+?)\*\*`)           // "**x**"   -> "x"
	mdCodeRe       = regexp.MustCompile("`([^`]+)`")               // "`x`"     -> "x"
	mdLinkRe       = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`) // "[t](u)"  -> "t (u)"
	mdHRRe         = regexp.MustCompile(`^(-{3,}|\*{3,}|_{3,})$`)  // "---" / "***" / "___"
)

// cleanReleaseNotes turns a GitHub release body (goreleaser-generated
// GitHub-flavoured markdown) into plain-text lines fit for a terminal.
// The CLI can't render markdown, so dumping the body raw shows literal
// "##", "**", "```" and horizontal-rule noise.
//
// Beyond de-markdowning we drop content that's redundant inside an
// upgrade flow the CLI is about to run itself:
//   - fenced code blocks (the "Install / upgrade" command snippet —
//     the CLI execs brew/go right after this box)
//   - the "Full diff" compare line (developer-facing)
//   - horizontal rules
//
// Returns the cleaned lines with interior blank runs collapsed to one
// and no leading/trailing blanks; an empty slice means "nothing worth
// showing", which the caller renders as the no-notes fallback.
func cleanReleaseNotes(body string) []string {
	var out []string
	inFence := false
	for _, raw := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(raw)

		// Fenced code block toggles. Drop the fences and everything
		// inside — in release notes that's the install snippet the
		// CLI supersedes by running the upgrade itself.
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence || mdHRRe.MatchString(trimmed) {
			continue
		}

		clean := stripInlineMarkdown(strings.TrimRight(raw, " \t"))
		ct := strings.TrimSpace(clean)

		// Drop developer-facing / redundant lead-in lines (checked
		// after stripping so "**Full diff:**" matches "Full diff").
		low := strings.ToLower(ct)
		if strings.HasPrefix(low, "full diff") ||
			strings.HasPrefix(low, "install / upgrade") ||
			strings.HasPrefix(low, "install/upgrade") {
			continue
		}

		if ct == "" {
			// Collapse blank runs; never lead with one.
			if len(out) > 0 && out[len(out)-1] != "" {
				out = append(out, "")
			}
			continue
		}
		out = append(out, clean)
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// stripInlineMarkdown reduces one line's markdown to plain text. Order
// matters: headings and bullets anchor on the line start, so they run
// before the inline passes; and the triple-marker bold-italic pass runs
// before the double-marker bold pass so "***x***" doesn't leave stray
// asterisks behind. Single-marker *italic* / _italic_ is intentionally
// left alone — stripping it would mangle snake_case and URLs for a
// construct goreleaser changelogs don't use.
func stripInlineMarkdown(line string) string {
	line = mdHeadingRe.ReplaceAllString(line, "")
	line = mdBulletRe.ReplaceAllString(line, "${1}• ")
	line = mdBoldItalicRe.ReplaceAllString(line, "$1")
	line = mdBoldRe.ReplaceAllString(line, "$1")
	line = mdCodeRe.ReplaceAllString(line, "$1")
	line = mdLinkRe.ReplaceAllString(line, "$1 ($2)")
	return line
}

// ---- semver compare ------------------------------------------------

// compareSemver returns -1 / 0 / +1 in the same direction as
// strings.Compare, treating each numeric dotted segment of the
// version as a number rather than a string.
//
//   - "dev" / "unknown" / anything non-`vX.Y.Z` sorts BEFORE every
//     real release, so `update` from a dev build always reports
//     "update available" — the right thing.
//   - Pre-release suffixes (-rc1, -beta) are DELIBERATELY rounded to
//     equal with the same X.Y.Z: strict semver sorts a pre-release
//     below its final release, but for this command's only question
//     ("can I update?") nudging a -rc1 user onto the same .0 release
//     would confuse more than help, so the comparison stops at the
//     first three numeric segments. (TestCompareSemver pins this.)
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
