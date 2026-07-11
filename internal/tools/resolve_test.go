package tools

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeFakeExec drops an executable-looking file named for `name` into
// dir and returns its path. On Unix it's chmod 0755; on Windows it's a
// `.cmd` shim (matching what npm writes), which findExecutable finds via
// PATHEXT. Mode bits are ignored on Windows, which is fine.
func writeFakeExec(t *testing.T, dir, name string) string {
	t.Helper()
	fname := name
	if runtime.GOOS == "windows" {
		fname = name + ".cmd"
	}
	p := filepath.Join(dir, fname)
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake exec: %v", err)
	}
	return p
}

// assertResolvesInto fails unless got is a real file living directly in
// dir. Avoids brittle exact-string equality: on Windows findExecutable
// appends a PATHEXT extension whose case may differ from the file on
// disk, so we assert by directory + stat-ability instead.
func assertResolvesInto(t *testing.T, got, dir string) {
	t.Helper()
	if got == "" {
		t.Fatal("ResolveExec returned an empty path")
	}
	if d := filepath.Dir(got); d != dir {
		t.Errorf("resolved into %q, want a file in %q", d, dir)
	}
	if fi, err := os.Stat(got); err != nil || fi.IsDir() {
		t.Errorf("resolved path %q is not a file (err=%v)", got, err)
	}
}

// TestFindExecutable_FoundAndMissing checks the leaf lookup: an
// executable present in the dir is found; an absent name isn't.
func TestFindExecutable_FoundAndMissing(t *testing.T) {
	dir := t.TempDir()
	writeFakeExec(t, dir, "widget")

	if got, ok := findExecutable(dir, "widget"); !ok {
		t.Errorf("findExecutable(widget) = (%q, false), want found", got)
	} else {
		assertResolvesInto(t, got, dir)
	}
	if got, ok := findExecutable(dir, "not-there-zzz"); ok {
		t.Errorf("findExecutable(not-there) = (%q, true), want not found", got)
	}
	// A non-executable file must NOT count as a hit on Unix (Windows keys
	// off the extension, not the mode bit, so skip the assertion there).
	if runtime.GOOS != "windows" {
		plain := filepath.Join(dir, "plainfile")
		if err := os.WriteFile(plain, []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, ok := findExecutable(dir, "plainfile"); ok {
			t.Error("findExecutable matched a non-executable file")
		}
	}
}

// TestResolveExec_OnPath verifies the fast path: a tool whose ExecName is
// a real system binary resolves via $PATH with no npm involvement.
func TestResolveExec_OnPath(t *testing.T) {
	tool := &Tool{Name: "shell", ExecName: "sh"}
	if runtime.GOOS == "windows" {
		tool.ExecName = "cmd"
	}
	got, err := ResolveExec(tool)
	if err != nil {
		t.Fatalf("ResolveExec(%s) errored: %v", tool.ExecName, err)
	}
	if got == "" {
		t.Fatal("ResolveExec returned empty path for a system binary")
	}
}

// TestResolveExec_NpmFallbackViaEnvBinDir is the core regression guard:
// a tool that ISN'T on $PATH but WAS globally npm-installed must still
// resolve, via the version-manager bin dir exported in NVM_BIN. This is
// the exact `everyapi use codex` wedge — npm's global bin missing from
// $PATH — that previously looped forever on "not installed".
func TestResolveExec_NpmFallbackViaEnvBinDir(t *testing.T) {
	binDir := t.TempDir()
	const exe = "everyapi-fake-codex-zzz" // vanishingly unlikely to be on real PATH
	writeFakeExec(t, binDir, exe)

	// Point NVM_BIN at our fake bin dir; clear the other prefix signals so
	// nothing else can shadow the assertion.
	t.Setenv("NVM_BIN", binDir)
	t.Setenv("VOLTA_HOME", "")
	t.Setenv("FNM_MULTISHELL_PATH", "")
	t.Setenv("npm_config_prefix", "")
	t.Setenv("NPM_CONFIG_PREFIX", "")
	t.Setenv("PREFIX", "")

	tool := &Tool{Name: "codexish", ExecName: exe, InstallCmd: "npm install -g @openai/codex"}
	got, err := ResolveExec(tool)
	if err != nil {
		t.Fatalf("ResolveExec fallback errored: %v", err)
	}
	assertResolvesInto(t, got, binDir)

	// IsInstalled rides the same resolver, so it must agree.
	if !IsInstalled(tool) {
		t.Error("IsInstalled = false for an npm-global tool present in NVM_BIN")
	}
}

// TestResolveExec_NonNpmToolSkipsNpmDirs pins that the npm-global search
// is gated to npm installers: a curl|bash tool that isn't on $PATH must
// NOT be "found" just because a same-named binary happens to sit in an
// npm bin dir. Keeps the fallback from mis-resolving unrelated tools.
func TestResolveExec_NonNpmToolSkipsNpmDirs(t *testing.T) {
	binDir := t.TempDir()
	const exe = "everyapi-fake-curltool-zzz"
	writeFakeExec(t, binDir, exe)
	t.Setenv("NVM_BIN", binDir)

	tool := &Tool{
		Name:               "curlish",
		ExecName:           exe,
		InstallCmd:         "curl -fsSL https://example.com/install.sh | bash",
		InstallCmdUnixOnly: true,
	}
	if got, err := ResolveExec(tool); err == nil {
		t.Errorf("ResolveExec(non-npm) = %q, want not found (npm dirs must be skipped)", got)
	}
}

// TestBinSubdir pins the prefix→bin mapping that differs by platform:
// <prefix>/bin on Unix, the prefix itself on Windows.
func TestBinSubdir(t *testing.T) {
	if got := binSubdir(""); got != "" {
		t.Errorf("binSubdir(\"\") = %q, want empty", got)
	}
	got := binSubdir("/opt/node")
	want := filepath.Join("/opt/node", "bin")
	if runtime.GOOS == "windows" {
		want = "/opt/node"
	}
	if got != want {
		t.Errorf("binSubdir = %q, want %q", got, want)
	}
}

// TestDedupeStrings covers order-preserving de-duplication and that the
// input slice isn't aliased into the output (a shared backing array
// would let a later append corrupt the caller's data).
func TestDedupeStrings(t *testing.T) {
	in := []string{"a", "b", "a", "c", "b"}
	got := dedupeStrings(in)
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("dedupeStrings = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("dedupeStrings = %v, want %v", got, want)
		}
	}
}

// pathValue returns env's PATH value under whatever casing the key uses
// ("Path" on Windows), or "" if absent.
func pathValue(env map[string]string) (string, bool) {
	for k, v := range env {
		if strings.EqualFold(k, "PATH") {
			return v, true
		}
	}
	return "", false
}

// TestWithExecDirOnPath is the guard for the interpreter-not-found
// regression: when a tool is resolved in a dir that ISN'T on $PATH, that
// dir (holding the tool's co-located `node`/siblings) must be APPENDED
// to the child's PATH — appended, not prepended, so it can't shadow a
// node/npm the user already has first — and when it's already on $PATH
// (even with a trailing separator), env is untouched.
func TestWithExecDirOnPath(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "gemini")

	// dir NOT on PATH → appended last, existing entries keep priority,
	// other vars preserved, input not mutated.
	first := filepath.Join(dir, "..", "elsewhere")
	t.Setenv("PATH", first)
	env := map[string]string{"FOO": "bar"}
	got := withExecDirOnPath(env, execPath)
	pv, ok := pathValue(got)
	if !ok {
		t.Fatal("expected a PATH entry to be injected")
	}
	entries := filepath.SplitList(pv)
	if len(entries) < 2 || entries[len(entries)-1] != dir {
		t.Errorf("PATH = %q, want %q appended last", pv, dir)
	}
	if entries[0] != first {
		t.Errorf("PATH = %q, want existing entry %q kept first", pv, first)
	}
	if got["FOO"] != "bar" {
		t.Error("unrelated env vars must be preserved")
	}
	if _, mutated := pathValue(env); mutated {
		t.Error("input env map must not be mutated")
	}

	// dir already on PATH → env returned unchanged (no PATH override added).
	t.Setenv("PATH", dir+string(os.PathListSeparator)+"/x")
	env2 := map[string]string{"FOO": "bar"}
	got2 := withExecDirOnPath(env2, execPath)
	if _, injected := pathValue(got2); injected {
		t.Errorf("dir already on PATH: expected no PATH override, got %v", got2)
	}

	// dir on PATH with a trailing separator → still recognised (entries
	// are Clean'd before comparing), so no override either.
	t.Setenv("PATH", dir+string(os.PathSeparator)+string(os.PathListSeparator)+"/x")
	got3 := withExecDirOnPath(map[string]string{}, execPath)
	if _, injected := pathValue(got3); injected {
		t.Error("trailing-separator PATH entry must still count as already present")
	}
}
