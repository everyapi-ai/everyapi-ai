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
// One command, one user action: typing `everyapi update` should
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
//   - anything else        → unknown (curl / manual)
//
// Detected install methods exec the right tool's upgrade flow —
// `brew update && brew upgrade everyapi` or `go install …@latest`.
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

	ctx, cancel := context.WithTimeout(cliout.WithCtx(), 10*time.Second)
	defer cancel()

	// fetchLatestRelease also pulls the body so the outdated
	// branch below can render the changelog before handing off
	// to brew / go install. --check and dev-build paths only
	// look at .Tag — the extra body payload is the cost of not
	// branching on which fields the caller needs.
	rel, err := fetchLatestRelease(ctx)
	if err != nil {
		return fmt.Errorf("check latest version: %w", err)
	}
	latest := rel.Tag

	ver, commit := version.Resolve()

	// Dev builds (local `go build` without -ldflags stamping) shouldn't
	// be force-marched onto a release tarball — the natural upgrade is
	// `git pull && go build`, not `curl | tar` overwriting their dev
	// binary. Detect and short-circuit with a tailored message.
	if ver == "dev" {
		if *checkOnly {
			cliout.Printf(i18n.T("update.dev_build_check"), commit, latest)
			return nil
		}
		cliout.Printf(i18n.T("update.dev_build_intro"), commit, latest)
		cliout.Println("")
		cliout.Println(i18n.T("update.rebuild_header"))
		cliout.Println(i18n.T("update.rebuild_cmd"))
		cliout.Println("")
		cliout.Println(i18n.T("update.switch_header"))
		cliout.Println(i18n.T("update.switch_cmd_brew"))
		cliout.Println(i18n.T("update.switch_cmd_go"))
		return nil
	}

	cmp := compareSemver(ver, latest)

	if *checkOnly {
		if cmp >= 0 {
			cliout.Printf(i18n.T("update.up_to_date_check")+"\n", ver)
			return nil
		}
		cliout.Printf(i18n.T("update.available")+"\n", ver, latest)
		os.Exit(1)
		return nil // unreachable
	}

	if cmp > 0 {
		cliout.Printf("\n"+i18n.T("update.prerelease")+"\n", ver, latest)
		cliout.Println(i18n.T("update.nothing_to_do"))
		return nil
	}
	if cmp == 0 {
		cliout.Printf("\n"+i18n.T("update.up_to_date")+"\n", ver)
		return nil
	}

	// cmp < 0: outdated. Pick a method based on where the binary lives.
	method := detectInstallMethod()
	cliout.Printf("\n"+i18n.T("update.update_available")+"\n", ver, latest)
	renderChangelog(rel)
	cliout.Printf(i18n.T("update.install_method")+"\n\n", method)

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
	// Resolve symlinks: brew puts /opt/homebrew/bin/everyapi as a
	// symlink to /opt/homebrew/Cellar/everyapi/X.Y.Z/bin/everyapi;
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
	cliout.Printf("\nDone. Run `everyapi version` to confirm.\n")
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
	cliout.Printf("\nDone. Run `everyapi version` to confirm.\n")
	return nil
}

func printUnknownInstallHint(latest string) {
	binaryPath := guessBinaryPath()
	cliout.Printf("%s\n", i18n.T("update.cant_auto"))

	// Install-script path comes first because it's the most common
	// install method for users who land in the "unknown" branch —
	// `curl … install.sh | bash` lands the binary in ~/.local/bin,
	// which doesn't match the brew /Cellar/ or go install $GOBIN
	// prefixes detectInstallMethod() looks for. Re-running the same
	// curl command upgrades in place, so it's the closest thing to
	// a one-liner upgrade for non-brew/non-go users.
	cliout.Printf("  # Install script (Linux / macOS — re-run to upgrade in place)\n")
	cliout.Printf("  curl -fsSL https://everyapi.ai/install.sh | bash\n\n")
	cliout.Printf("  # Homebrew (after `brew tap everyapi-ai/tap`)\n")
	cliout.Printf("  brew update && brew upgrade everyapi\n\n")
	cliout.Printf("  # go install\n")
	cliout.Printf("  go install github.com/everyapi-ai/everyapi-ai@latest\n\n")
	cliout.Printf("  # Direct binary (current platform: %s/%s)\n", runtime.GOOS, runtime.GOARCH)
	cliout.Printf("  curl -L -o %s.new \\\n", binaryPath)
	cliout.Printf("    https://github.com/everyapi-ai/everyapi-ai/releases/download/%s/everyapi_%s_%s.tar.gz\n",
		latest, runtime.GOOS, runtime.GOARCH)
	cliout.Printf("  # verify SHA256 + cosign per README before replacing the binary\n")

	cliout.Printf("\nRelease notes: https://github.com/everyapi-ai/everyapi-ai/releases/tag/%s\n", latest)
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
// rate-limited but don't require auth, which is what `everyapi update`
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
				cliout.Printf("  %s\n", line)
			}
		}
	}
	if rel.HTMLURL != "" {
		cliout.Printf("\n"+i18n.T("update.full_release")+"\n", rel.HTMLURL)
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
