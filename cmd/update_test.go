package cmd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// compareSemver is the only piece of `everyapi update` worth unit- testing in isolation: the GitHub roundtrip is integration-shaped (handled in the CI smoke test against a real release), but the version comparison is pure logic + drives the "outdated vs up-to-date" branch the user actually sees.
func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		// Plain semver
		{"v0.1.0", "v0.1.0", 0},
		{"v0.1.0", "v0.1.1", -1},
		{"v0.1.1", "v0.1.0", 1},
		{"v0.1.10", "v0.1.2", 1}, // numeric, not string — "10" > "2"
		{"v0.2.0", "v0.1.99", 1},
		{"v1.0.0", "v0.99.99", 1},

		// Missing segments → treated as -1 → sort before any real release
		{"dev", "v0.1.0", -1},
		{"unknown", "v0.1.0", -1},
		{"v0.1.0", "dev", 1},

		// Pre-release suffix stripped — only X.Y.Z compared. semver strictly considers a pre-release LESS than the same base version, but for this CLI's purpose ("can I `update`?") rounding to equal is safer — telling a -rc1 user to upgrade to the same .0 release would confuse more than help.
		{"v0.2.0-rc1", "v0.2.0", 0},
		{"v0.2.0", "v0.2.0-rc1", 0},
		{"v0.2.0+meta", "v0.2.0", 0},

		// Leading whitespace / 'v' tolerance
		{"  v0.1.0  ", "0.1.0", 0},
		{"0.1.0", "v0.1.0", 0},
	}
	for _, tc := range cases {
		got := compareSemver(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("compareSemver(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestParseSemver_NonNumericFails guards the "non-numeric segments fall back to -1" branch — important because that's what makes `dev` / `unknown` always lose to real tags.
func TestParseSemver_NonNumericFails(t *testing.T) {
	got := parseSemver("v0.x.0")
	// First segment parses (0), second is non-numeric (x), third parses (0) but we expect at most the first non-numeric to taint subsequent reads — the function is per-segment so 0.x.0 → [0, -1, 0].
	if got != [3]int{0, -1, 0} {
		t.Errorf("parseSemver(v0.x.0) = %v, want [0 -1 0]", got)
	}

	got = parseSemver("")
	if got != [3]int{-1, -1, -1} {
		t.Errorf("parseSemver(empty) = %v, want all -1", got)
	}
}

// TestGoBinDirs_RespectsEnv covers the precedence order so a future rewrite doesn't accidentally let GOPATH override GOBIN.
func TestGoBinDirs_RespectsEnv(t *testing.T) {
	// filepath.SplitList / filepath.Join so the expectations hold on Windows too (`;` list separator, `\` joins) — goBinDirs itself is platform-neutral.
	t.Setenv("GOBIN", "/tmp/test-gobin")
	t.Setenv("GOPATH", "/tmp/test-gopath"+string(os.PathListSeparator)+"/tmp/test-gopath2")
	dirs := goBinDirs()
	if len(dirs) < 3 {
		t.Fatalf("expected at least 3 candidate dirs (GOBIN + GOPATH × 2), got %v", dirs)
	}
	if dirs[0] != "/tmp/test-gobin" {
		t.Errorf("GOBIN should come first, got %q", dirs[0])
	}
	// Both GOPATH entries should show up before the $HOME/go/bin fallback.
	gopath1 := filepath.Join("/tmp/test-gopath", "bin")
	gopath2 := filepath.Join("/tmp/test-gopath2", "bin")
	if dirs[1] != gopath1 || dirs[2] != gopath2 {
		t.Errorf("GOPATH split order wrong: got %v, want %q then %q", dirs[1:3], gopath1, gopath2)
	}
}

func TestGoBinDirs_EmptyEnvFallsBackToHome(t *testing.T) {
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", "")
	dirs := goBinDirs()
	// Without env vars we should still get $HOME/go/bin as the last resort. Exact path depends on the test runner's $HOME; just assert non-empty + ends in "go/bin".
	if len(dirs) == 0 {
		t.Fatal("expected $HOME/go/bin fallback, got empty slice")
	}
	last := dirs[len(dirs)-1]
	if last == "" || !endsWith(last, filepath.Join("go", "bin")) {
		t.Errorf("last entry should be $HOME/go/bin, got %q", last)
	}
}

func endsWith(s, suffix string) bool {
	if len(s) < len(suffix) {
		return false
	}
	return s[len(s)-len(suffix):] == suffix
}

// TestGithubAPIError covers the three branches githubAPIError walks through. The rate-limit path is the one users actually hit (60 req shared per IP, exhausted on busy NATs) and the error message has to be specific enough to point at the GITHUB_TOKEN workaround.
func TestGithubAPIError(t *testing.T) {
	t.Run("rate-limit exhausted with reset header", func(t *testing.T) {
		reset := time.Now().Add(15 * time.Minute).Unix()
		resp := &http.Response{
			StatusCode: http.StatusForbidden,
			Header:     http.Header{},
		}
		resp.Header.Set("X-RateLimit-Remaining", "0")
		resp.Header.Set("X-RateLimit-Reset", strconv.FormatInt(reset, 10))
		err := githubAPIError(resp)
		if err == nil {
			t.Fatal("want error")
		}
		msg := err.Error()
		if !strings.Contains(msg, "rate-limit exhausted") {
			t.Errorf("missing rate-limit text: %q", msg)
		}
		if !strings.Contains(msg, "GITHUB_TOKEN") {
			t.Errorf("missing GITHUB_TOKEN hint: %q", msg)
		}
		if !strings.Contains(msg, "resets in") {
			t.Errorf("missing 'resets in' duration: %q", msg)
		}
	})

	t.Run("rate-limit exhausted without reset header", func(t *testing.T) {
		// Some proxies strip the rate-limit headers; fall back to a shorter message that still names the bucket + workaround.
		resp := &http.Response{StatusCode: http.StatusForbidden, Header: http.Header{}}
		resp.Header.Set("X-RateLimit-Remaining", "0")
		err := githubAPIError(resp)
		if err == nil || !strings.Contains(err.Error(), "GITHUB_TOKEN") {
			t.Errorf("want GITHUB_TOKEN hint without reset header, got %v", err)
		}
	})

	t.Run("403 that is NOT a rate-limit case falls through generic", func(t *testing.T) {
		// Could be auth (private repo + bad token) or abuse detection. Don't claim rate-limit when there's no header proof.
		resp := &http.Response{StatusCode: http.StatusForbidden, Header: http.Header{}}
		// X-RateLimit-Remaining absent or non-zero → not the bucket case; surface raw status so the user sees the real story.
		err := githubAPIError(resp)
		if err == nil || !strings.Contains(err.Error(), "returned 403") {
			t.Errorf("want generic 403, got %v", err)
		}
	})

	t.Run("non-403 status passes through generic", func(t *testing.T) {
		resp := &http.Response{StatusCode: http.StatusBadGateway, Header: http.Header{}}
		err := githubAPIError(resp)
		if err == nil || !strings.Contains(err.Error(), "returned 502") {
			t.Errorf("want generic 502, got %v", err)
		}
	})
}

// TestFetchLatestRelease_Auth covers the header-injection plumbing — the actual behavioural change of the rate-limit fix. Without these checks a refactor could silently drop the Authorization header and the loud-fail-on-403 message would still test green while users still got rate-limited.
func TestFetchLatestRelease_Auth(t *testing.T) {
	t.Run("no env vars → no Authorization header", func(t *testing.T) {
		t.Setenv("GH_TOKEN", "")
		t.Setenv("GITHUB_TOKEN", "")
		var seen string
		srv := newReleaseTestServer(func(r *http.Request) (int, string) {
			seen = r.Header.Get("Authorization")
			return 200, `{"tag_name":"v1.2.3"}`
		})
		defer srv.Close()
		swapPollURL(t, srv.URL+"/releases/latest")
		if _, err := fetchLatestRelease(testCtx(t)); err != nil {
			t.Fatalf("fetchLatestRelease: %v", err)
		}
		if seen != "" {
			t.Errorf("unexpected Authorization header sent: %q", seen)
		}
	})

	t.Run("GITHUB_TOKEN set → sent as Bearer", func(t *testing.T) {
		t.Setenv("GH_TOKEN", "")
		t.Setenv("GITHUB_TOKEN", "ghp_classic_xyz")
		var seen string
		srv := newReleaseTestServer(func(r *http.Request) (int, string) {
			seen = r.Header.Get("Authorization")
			return 200, `{"tag_name":"v1.2.3"}`
		})
		defer srv.Close()
		swapPollURL(t, srv.URL+"/releases/latest")
		if _, err := fetchLatestRelease(testCtx(t)); err != nil {
			t.Fatalf("fetchLatestRelease: %v", err)
		}
		if seen != "Bearer ghp_classic_xyz" {
			t.Errorf("Authorization header = %q, want Bearer ghp_classic_xyz", seen)
		}
	})

	t.Run("GH_TOKEN wins over GITHUB_TOKEN", func(t *testing.T) {
		// gh CLI sets GH_TOKEN; CI sets GITHUB_TOKEN. When both are set the user's gh login should take precedence.
		t.Setenv("GH_TOKEN", "ghp_from_gh_cli")
		t.Setenv("GITHUB_TOKEN", "ghp_from_ci")
		var seen string
		srv := newReleaseTestServer(func(r *http.Request) (int, string) {
			seen = r.Header.Get("Authorization")
			return 200, `{"tag_name":"v1.2.3"}`
		})
		defer srv.Close()
		swapPollURL(t, srv.URL+"/releases/latest")
		if _, err := fetchLatestRelease(testCtx(t)); err != nil {
			t.Fatalf("fetchLatestRelease: %v", err)
		}
		if seen != "Bearer ghp_from_gh_cli" {
			t.Errorf("Authorization header = %q, want gh CLI token to win", seen)
		}
	})

	t.Run("whitespace-only env is treated as unset", func(t *testing.T) {
		t.Setenv("GH_TOKEN", "   ")
		t.Setenv("GITHUB_TOKEN", "")
		var seen string
		srv := newReleaseTestServer(func(r *http.Request) (int, string) {
			seen = r.Header.Get("Authorization")
			return 200, `{"tag_name":"v1.2.3"}`
		})
		defer srv.Close()
		swapPollURL(t, srv.URL+"/releases/latest")
		if _, err := fetchLatestRelease(testCtx(t)); err != nil {
			t.Fatalf("fetchLatestRelease: %v", err)
		}
		if seen != "" {
			t.Errorf("whitespace token leaked into Authorization: %q", seen)
		}
	})
}

// --- test helpers below; kept package-local because they touch
// the file-scope latestReleasePollURL var and shouldn't leak.

func newReleaseTestServer(handler func(r *http.Request) (status int, body string)) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st, body := handler(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(st)
		_, _ = w.Write([]byte(body))
	}))
}

func swapPollURL(t *testing.T, url string) {
	t.Helper()
	prev := latestReleasePollURL
	latestReleasePollURL = url
	t.Cleanup(func() { latestReleasePollURL = prev })
}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// TestCleanReleaseNotes verifies cleanReleaseNotes turns goreleaser's GitHub-flavoured-markdown body into terminal-readable plain text. The CLI can't render markdown, so these cases pin the de-markdowning (headings/bold/code/links/bullets) plus the noise-trimming (fenced code blocks, "Full diff" line, horizontal rules) the upgrade box relies on.
func TestCleanReleaseNotes(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string // joined with "\n"
	}{
		{
			name: "empty body yields nothing",
			body: "",
			want: "",
		},
		{
			// The exact shape goreleaser emits for this repo: headings, a bullet, an HR, the Full-diff line, then a fenced install snippet. Everything but the changelog proper is noise inside an upgrade flow the CLI runs itself.
			name: "real goreleaser body strips to changelog only",
			body: "## Changelog\n" +
				"### Other changes\n" +
				"*  release: develop → main (cli #345 / #346 / #347) [skip ci]\n" +
				"\n\n---\n\n" +
				"**Full diff:** https://github.com/everyapi-ai/everyapi-ai/compare/backend-v0.2.32...v0.2.0\n\n" +
				"**Install / upgrade:**\n" +
				"```\n" +
				"brew upgrade everyapi              # macOS / Linux (Homebrew)\n" +
				"go install github.com/everyapi-ai/everyapi-ai@v0.2.0   # Go users\n" +
				"everyapi update                    # already installed\n" +
				"```",
			want: "Changelog\n" +
				"Other changes\n" +
				"• release: develop → main (cli #345 / #346 / #347) [skip ci]",
		},
		{
			name: "inline markdown is stripped",
			body: "## What's new\n" +
				"- fixed the **login** flow and the `proxy` daemon\n" +
				"- see [the docs](https://example.com/docs) for details",
			want: "What's new\n" +
				"• fixed the login flow and the proxy daemon\n" +
				"• see the docs (https://example.com/docs) for details",
		},
		{
			name: "body of pure noise yields nothing",
			body: "---\n\n```\nsome code\n```\n\n***",
			want: "",
		},
		{
			name: "interior blank lines collapse, edges trimmed",
			body: "\n\nfirst\n\n\n\nsecond\n\n",
			want: "first\n\nsecond",
		},
		{
			// Pins the prefix drops in isolation (the real-body case covers them transitively): a reworded template that breaks this filter fails here directly. The bold markers must be stripped before the prefix check matches.
			name: "Full diff / Install-upgrade lead-ins dropped, kept line survives",
			body: "actual change here\n\n" +
				"**Full diff:** https://example.com/compare/aaa...bbb\n\n" +
				"**Install / upgrade:**",
			want: "actual change here",
		},
		{
			// Triple-marker bold-italic must not leave stray asterisks (the double-marker pass alone would turn ***x*** into *x*).
			name: "triple-marker bold-italic strips clean",
			body: "- bumped to ***v2*** today",
			want: "• bumped to v2 today",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(cleanReleaseNotes(tc.body), "\n")
			if got != tc.want {
				t.Errorf("cleanReleaseNotes(%q):\n got: %q\nwant: %q", tc.body, got, tc.want)
			}
		})
	}
}

// TestFetchLatestTag pins the github.com redirect parsing — the rate-limit-safe tag lookup that backs both `update` and the silent auto-check. The whole point of routing through the redirect is to avoid the api.github.com 60/hour bucket, so the cases that matter are: a normal 302 → tag, a missing/indexed redirect → errNoReleaseYet (no published release), a 404 → errNoReleaseYet, and a non-redirect status → hard error. A regression here silently reintroduces the rate-limited API path's failure mode.
func TestFetchLatestTag(t *testing.T) {
	cases := []struct {
		name        string
		status      int
		location    string
		wantTag     string
		wantErr     error
		wantHardErr bool // expect a non-nil error that is NOT errNoReleaseYet
	}{
		{name: "302 with tag", status: 302, location: "https://github.com/everyapi-ai/everyapi-ai/releases/tag/v0.2.6", wantTag: "v0.2.6"},
		{name: "302 with trailing slash", status: 302, location: "https://github.com/x/y/releases/tag/v1.2.3/", wantTag: "v1.2.3"},
		{name: "302 to releases index (no release yet)", status: 302, location: "https://github.com/x/y/releases", wantErr: errNoReleaseYet},
		{name: "404 (no release yet)", status: 404, wantErr: errNoReleaseYet},
		{name: "unexpected 200", status: 200, wantHardErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.location != "" {
					w.Header().Set("Location", tc.location)
				}
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()
			swapRedirectURL(t, srv.URL+"/releases/latest")

			tag, err := fetchLatestTag(testCtx(t))
			switch {
			case tc.wantTag != "":
				if err != nil {
					t.Fatalf("fetchLatestTag: unexpected error %v", err)
				}
				if tag != tc.wantTag {
					t.Errorf("tag = %q, want %q", tag, tc.wantTag)
				}
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("err = %v, want %v", err, tc.wantErr)
				}
			case tc.wantHardErr:
				if err == nil || errors.Is(err, errNoReleaseYet) {
					t.Errorf("err = %v, want a generic non-errNoReleaseYet error", err)
				}
			}
		})
	}
}

// TestChangelogRelease covers the best-effort changelog resolution in the outdated `update` branch: a working API yields the real release body; a drained / erroring API degrades to a body-less release whose link is derived from the tag (so renderChangelog still shows a URL rather than the upgrade aborting).
func TestChangelogRelease(t *testing.T) {
	t.Run("API ok -> real release with body", func(t *testing.T) {
		srv := newReleaseTestServer(func(r *http.Request) (int, string) {
			return 200, `{"tag_name":"v0.2.6","body":"notes here","html_url":"https://example.test/r/v0.2.6"}`
		})
		defer srv.Close()
		swapPollURL(t, srv.URL+"/releases/latest")

		rel := changelogRelease(testCtx(t), "v0.2.6")
		if rel.Body != "notes here" {
			t.Errorf("Body = %q, want the API body", rel.Body)
		}
		if rel.HTMLURL != "https://example.test/r/v0.2.6" {
			t.Errorf("HTMLURL = %q, want the API url", rel.HTMLURL)
		}
	})

	t.Run("API rate-limited -> tag-derived fallback, no body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.WriteHeader(403)
		}))
		defer srv.Close()
		swapPollURL(t, srv.URL+"/releases/latest")

		rel := changelogRelease(testCtx(t), "v0.2.6")
		if rel.Tag != "v0.2.6" {
			t.Errorf("Tag = %q, want v0.2.6", rel.Tag)
		}
		if rel.Body != "" {
			t.Errorf("Body = %q, want empty on API failure", rel.Body)
		}
		if rel.HTMLURL != releaseTagURL("v0.2.6") {
			t.Errorf("HTMLURL = %q, want %q", rel.HTMLURL, releaseTagURL("v0.2.6"))
		}
	})
}

func swapRedirectURL(t *testing.T, url string) {
	t.Helper()
	prev := latestReleaseRedirectURL
	latestReleaseRedirectURL = url
	t.Cleanup(func() { latestReleaseRedirectURL = prev })
}

// TestScriptBinDirs pins the install script's default target dir per platform — the dir classifyExePath must recognise so script installs stop reading as "unknown (curl / manual)".
func TestScriptBinDirs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", `C:\Users\test\AppData\Local`)
		dirs := scriptBinDirs()
		want := filepath.Join(`C:\Users\test\AppData\Local`, "everyapi", "bin")
		if len(dirs) != 1 || dirs[0] != want {
			t.Fatalf("scriptBinDirs() = %v, want [%s]", dirs, want)
		}
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	dirs := scriptBinDirs()
	want := filepath.Join(home, ".local", "bin")
	if len(dirs) != 1 || dirs[0] != want {
		t.Fatalf("scriptBinDirs() = %v, want [%s]", dirs, want)
	}
}

// TestClassifyExePath_ScriptInstall is the regression guard for the "installed by install.sh / install.ps1 but reported as unknown" bug: a binary in the script's default bin dir must classify as the script method (and get the script's one-liner), not the unknown grab-bag whose brew/bash entries don't even fit the platform. The Windows path deliberately varies the casing — NTFS is case-insensitive, and %LOCALAPPDATA% casing differs by source.
func TestClassifyExePath_ScriptInstall(t *testing.T) {
	var exe string
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", `C:\Users\test\AppData\Local`)
		exe = `C:\Users\test\appdata\local\everyapi\bin\everyapi.exe`
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skipf("no home dir: %v", err)
		}
		exe = filepath.Join(home, ".local", "bin", "everyapi")
	}
	// Point the go-install candidates away from exe so a dev machine's GOBIN/GOPATH can't shadow the branch under test.
	t.Setenv("GOBIN", filepath.Join(t.TempDir(), "gobin"))
	t.Setenv("GOPATH", filepath.Join(t.TempDir(), "gopath"))
	if got := classifyExePath(exe); got != installMethodScript {
		t.Errorf("classifyExePath(%q) = %q, want %q", exe, got, installMethodScript)
	}
}

func TestRunInstallScriptUpgrade_DispatchesInstaller(t *testing.T) {
	var gotName string
	var gotArgs []string
	previous := updateCommandRunner
	updateCommandRunner = func(name string, args ...string) error {
		gotName = name
		gotArgs = append([]string(nil), args...)
		return nil
	}
	t.Cleanup(func() { updateCommandRunner = previous })

	if err := runInstallScriptUpgrade(false); err != nil {
		t.Fatalf("runInstallScriptUpgrade(false): %v", err)
	}

	wantName := "bash"
	wantArgs := []string{"-o", "pipefail", "-c", "curl -fsSL https://dl.everyapi.ai/install.sh | bash"}
	if runtime.GOOS == "windows" {
		wantName = "powershell.exe"
		wantArgs = []string{"-NoProfile", "-Command", "irm https://dl.everyapi.ai/install.ps1 | iex"}
	}
	if gotName != wantName || strings.Join(gotArgs, "\x00") != strings.Join(wantArgs, "\x00") {
		t.Fatalf("installer command = %q %q, want %q %q", gotName, gotArgs, wantName, wantArgs)
	}
}

func TestInstallScriptCommandForPlatform(t *testing.T) {
	tests := []struct {
		goos     string
		wantName string
		wantArgs []string
	}{
		{"linux", "bash", []string{"-o", "pipefail", "-c", "curl -fsSL https://dl.everyapi.ai/install.sh | bash"}},
		{"darwin", "bash", []string{"-o", "pipefail", "-c", "curl -fsSL https://dl.everyapi.ai/install.sh | bash"}},
		{"windows", "powershell.exe", []string{"-NoProfile", "-Command", "irm https://dl.everyapi.ai/install.ps1 | iex"}},
	}
	for _, tc := range tests {
		t.Run(tc.goos, func(t *testing.T) {
			gotName, gotArgs := installScriptCommandFor(tc.goos)
			if gotName != tc.wantName || strings.Join(gotArgs, "\x00") != strings.Join(tc.wantArgs, "\x00") {
				t.Fatalf("installScriptCommandFor(%q) = %q %q, want %q %q", tc.goos, gotName, gotArgs, tc.wantName, tc.wantArgs)
			}
		})
	}
}

func TestRunInstallScriptUpgrade_DryRunDoesNotDispatch(t *testing.T) {
	previous := updateCommandRunner
	called := false
	updateCommandRunner = func(string, ...string) error {
		called = true
		return nil
	}
	t.Cleanup(func() { updateCommandRunner = previous })

	if err := runInstallScriptUpgrade(true); err != nil {
		t.Fatalf("runInstallScriptUpgrade(true): %v", err)
	}
	if called {
		t.Fatal("dry-run dispatched the installer command")
	}
}

// A GOBIN deliberately pointed at the script dir must classify as go install — `go install` is what actually overwrites that binary, so its upgrade flow is the right one to run.
func TestClassifyExePath_GoInstallWinsOverScriptDir(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain on PATH")
	}
	var dir string
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", `C:\Users\test\AppData\Local`)
		dir = filepath.Join(`C:\Users\test\AppData\Local`, "everyapi", "bin")
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skipf("no home dir: %v", err)
		}
		dir = filepath.Join(home, ".local", "bin")
	}
	t.Setenv("GOBIN", dir)
	exe := filepath.Join(dir, "everyapi")
	if got := classifyExePath(exe); got != installMethodGoInstall {
		t.Errorf("classifyExePath(%q) = %q, want %q", exe, got, installMethodGoInstall)
	}
}

func TestClassifyExePath_BrewAndUnknown(t *testing.T) {
	sep := string(os.PathSeparator)
	brewExe := strings.Join([]string{"", "opt", "homebrew", "Cellar", "everyapi", "0.1.0", "bin", "everyapi"}, sep)
	if got := classifyExePath(brewExe); got != installMethodBrew {
		t.Errorf("classifyExePath(%q) = %q, want %q", brewExe, got, installMethodBrew)
	}

	t.Setenv("GOBIN", filepath.Join(t.TempDir(), "gobin"))
	t.Setenv("GOPATH", filepath.Join(t.TempDir(), "gopath"))
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", `C:\Users\test\AppData\Local`)
	}
	other := filepath.Join(t.TempDir(), "elsewhere", "everyapi")
	if got := classifyExePath(other); got != installMethodUnknown {
		t.Errorf("classifyExePath(%q) = %q, want %q", other, got, installMethodUnknown)
	}
}

// TestReleaseAssetName pins the zip-vs-tar.gz split (.goreleaser.yml format_overrides): the old hint hardcoded .tar.gz, which on Windows pointed at an asset that doesn't exist (the release ships a .zip).
func TestReleaseAssetName(t *testing.T) {
	got := releaseAssetName()
	wantExt := ".tar.gz"
	if runtime.GOOS == "windows" {
		wantExt = ".zip"
	}
	if !strings.HasSuffix(got, wantExt) {
		t.Errorf("releaseAssetName() = %q, want %q suffix", got, wantExt)
	}
	if !strings.Contains(got, runtime.GOOS) || !strings.Contains(got, runtime.GOARCH) {
		t.Errorf("releaseAssetName() = %q, want it to name %s/%s", got, runtime.GOOS, runtime.GOARCH)
	}
}

func TestReleaseAssetNameForWindowsArm64(t *testing.T) {
	if got, want := releaseAssetNameFor("windows", "arm64"), "everyapi_windows_arm64.zip"; got != want {
		t.Errorf("releaseAssetNameFor(windows, arm64) = %q, want %q", got, want)
	}
}
