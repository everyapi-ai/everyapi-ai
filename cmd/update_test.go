package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// compareSemver is the only piece of `everyapi update` worth unit-
// testing in isolation: the GitHub roundtrip is integration-shaped
// (handled in the CI smoke test against a real release), but the
// version comparison is pure logic + drives the "outdated vs
// up-to-date" branch the user actually sees.
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

		// Pre-release suffix stripped — only X.Y.Z compared. semver
		// strictly considers a pre-release LESS than the same base
		// version, but for this CLI's purpose ("can I `update`?")
		// rounding to equal is safer — telling a -rc1 user to
		// upgrade to the same .0 release would confuse more than help.
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

// TestParseSemver_NonNumericFails guards the "non-numeric segments
// fall back to -1" branch — important because that's what makes
// `dev` / `unknown` always lose to real tags.
func TestParseSemver_NonNumericFails(t *testing.T) {
	got := parseSemver("v0.x.0")
	// First segment parses (0), second is non-numeric (x), third parses (0)
	// but we expect at most the first non-numeric to taint subsequent reads —
	// the function is per-segment so 0.x.0 → [0, -1, 0].
	if got != [3]int{0, -1, 0} {
		t.Errorf("parseSemver(v0.x.0) = %v, want [0 -1 0]", got)
	}

	got = parseSemver("")
	if got != [3]int{-1, -1, -1} {
		t.Errorf("parseSemver(empty) = %v, want all -1", got)
	}
}

// TestGoBinDirs_RespectsEnv covers the precedence order so a future
// rewrite doesn't accidentally let GOPATH override GOBIN.
func TestGoBinDirs_RespectsEnv(t *testing.T) {
	t.Setenv("GOBIN", "/tmp/test-gobin")
	t.Setenv("GOPATH", "/tmp/test-gopath:/tmp/test-gopath2")
	dirs := goBinDirs()
	if len(dirs) < 3 {
		t.Fatalf("expected at least 3 candidate dirs (GOBIN + GOPATH × 2), got %v", dirs)
	}
	if dirs[0] != "/tmp/test-gobin" {
		t.Errorf("GOBIN should come first, got %q", dirs[0])
	}
	// Both GOPATH entries should show up before the $HOME/go/bin fallback.
	gopath1 := "/tmp/test-gopath/bin"
	gopath2 := "/tmp/test-gopath2/bin"
	if dirs[1] != gopath1 || dirs[2] != gopath2 {
		t.Errorf("GOPATH split order wrong: got %v, want %q then %q", dirs[1:3], gopath1, gopath2)
	}
}

func TestGoBinDirs_EmptyEnvFallsBackToHome(t *testing.T) {
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", "")
	dirs := goBinDirs()
	// Without env vars we should still get $HOME/go/bin as the last
	// resort. Exact path depends on the test runner's $HOME; just
	// assert non-empty + ends in "go/bin".
	if len(dirs) == 0 {
		t.Fatal("expected $HOME/go/bin fallback, got empty slice")
	}
	last := dirs[len(dirs)-1]
	if last == "" || !endsWith(last, "go/bin") {
		t.Errorf("last entry should be $HOME/go/bin, got %q", last)
	}
}

func endsWith(s, suffix string) bool {
	if len(s) < len(suffix) {
		return false
	}
	return s[len(s)-len(suffix):] == suffix
}

// TestGithubAPIError covers the three branches githubAPIError walks
// through. The rate-limit path is the one users actually hit (60 req
// shared per IP, exhausted on busy NATs) and the error message has to
// be specific enough to point at the GITHUB_TOKEN workaround.
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
		// Some proxies strip the rate-limit headers; fall back to a
		// shorter message that still names the bucket + workaround.
		resp := &http.Response{StatusCode: http.StatusForbidden, Header: http.Header{}}
		resp.Header.Set("X-RateLimit-Remaining", "0")
		err := githubAPIError(resp)
		if err == nil || !strings.Contains(err.Error(), "GITHUB_TOKEN") {
			t.Errorf("want GITHUB_TOKEN hint without reset header, got %v", err)
		}
	})

	t.Run("403 that is NOT a rate-limit case falls through generic", func(t *testing.T) {
		// Could be auth (private repo + bad token) or abuse detection.
		// Don't claim rate-limit when there's no header proof.
		resp := &http.Response{StatusCode: http.StatusForbidden, Header: http.Header{}}
		// X-RateLimit-Remaining absent or non-zero → not the bucket
		// case; surface raw status so the user sees the real story.
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

// TestFetchLatestRelease_Auth covers the header-injection plumbing
// — the actual behavioural change of the rate-limit fix. Without
// these checks a refactor could silently drop the Authorization
// header and the loud-fail-on-403 message would still test green
// while users still got rate-limited.
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
		// gh CLI sets GH_TOKEN; CI sets GITHUB_TOKEN. When both are
		// set the user's gh login should take precedence.
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
