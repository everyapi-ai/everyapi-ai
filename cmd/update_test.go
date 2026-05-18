package cmd

import "testing"

// compareSemver is the only piece of `relaya update` worth unit-
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
