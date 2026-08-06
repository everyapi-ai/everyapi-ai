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

// TestResolveExec_ExtraBinDirs covers the curl|bash cohort the npm
// fallback can't reach: Antigravity's installer writes `agy` into
// ~/.local/bin, which is routinely absent from the $PATH everyapi
// inherits (a GUI-launched terminal, or an rc file we never source).
// Without ExtraBinDirs the post-install re-check misses and `use` loops
// forever offering to reinstall.
func TestResolveExec_ExtraBinDirs(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const exe = "everyapi-fake-agy-zzz"
	writeFakeExec(t, binDir, exe)
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	}

	tool := &Tool{
		Name:               "geminiish",
		ExecName:           exe,
		InstallCmd:         "curl -fsSL https://example.com/install.sh | bash",
		InstallCmdUnixOnly: true,
		ExtraBinDirs:       []string{".local/bin"},
	}
	got, err := ResolveExec(tool)
	if err != nil {
		t.Fatalf("ResolveExec via ExtraBinDirs errored: %v", err)
	}
	assertResolvesInto(t, got, binDir)

	// IsInstalled rides the same resolver, so the install prompt agrees.
	if !IsInstalled(tool) {
		t.Error("IsInstalled = false for a tool present in its ExtraBinDirs")
	}

	// An unregistered exec name has no entry to consult, so LookupExecName
	// must miss: ExtraBinDirs is reached through the registry, never by
	// scanning ~/.local/bin for whatever happens to be there.
	if p, _, ok := LookupExecName(exe); ok {
		t.Errorf("LookupExecName(%q) = %q, want miss for an unregistered exec name", exe, p)
	}
}

// TestLookupExecName_MatchesExecNameNotRegistryKey pins the lookup key.
// `use gemini` launches Antigravity's `agy`, while the mcp subcommands
// pass the literal "gemini" for Google's separate gemini CLI. Matching on
// the registry key instead of ExecName would hand that unrelated binary
// agy's install directories.
func TestLookupExecName_MatchesExecNameNotRegistryKey(t *testing.T) {
	if got := toolByExecName("agy"); got == nil {
		t.Error(`toolByExecName("agy") = nil, want the gemini entry`)
	} else if got.Name != "gemini" {
		t.Errorf(`toolByExecName("agy").Name = %q, want gemini`, got.Name)
	}
	if got := toolByExecName("gemini"); got != nil {
		t.Errorf(`toolByExecName("gemini") = %q, want nil (that is a registry key, not an ExecName)`, got.Name)
	}
	if got := toolByExecName(""); got != nil {
		t.Errorf(`toolByExecName("") = %q, want nil`, got.Name)
	}
}

// TestToolByExecName_CoversWholeRegistry checks the baseline: every
// shipped tool is reachable by its own ExecName. It cannot detect the
// Registry-vs-Names() regression on its own — Names() and Registry list
// the same tools today, so an implementation iterating either one passes
// this. TestToolByExecName_ReachesEntriesMissingFromNames is the test
// that actually pins that distinction.
func TestToolByExecName_CoversWholeRegistry(t *testing.T) {
	for key, tool := range Registry {
		got := toolByExecName(tool.ExecName)
		if got == nil {
			t.Errorf("toolByExecName(%q) = nil, but %q is registered", tool.ExecName, key)
			continue
		}
		if got.ExecName != tool.ExecName {
			t.Errorf("toolByExecName(%q) resolved to a tool running %q", tool.ExecName, got.ExecName)
		}
	}
}

// TestResolveExec_ExtraBinDirsRejectsAbsolute pins the $HOME-relative
// contract: an absolute entry is ignored rather than searched, so the
// resolved location always stays anchored under the user's home dir.
func TestResolveExec_ExtraBinDirsRejectsAbsolute(t *testing.T) {
	outside := t.TempDir()
	const exe = "everyapi-fake-abs-zzz"
	writeFakeExec(t, outside, exe)
	t.Setenv("HOME", t.TempDir())
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", t.TempDir())
	}

	tool := &Tool{
		Name:         "absish",
		ExecName:     exe,
		ExtraBinDirs: []string{outside, ""},
	}
	if got, err := ResolveExec(tool); err == nil {
		t.Errorf("ResolveExec = %q, want not found (absolute ExtraBinDirs must be ignored)", got)
	}
}

// TestExtraBinDirsRejectsHomeEscape covers the other half of the
// home-anchored contract. filepath.Join Cleans its result, so a relative
// entry containing ".." resolves outside $HOME without ever tripping the
// IsAbs check — "sub/../../x" doesn't even start with "..". Containment
// has to be checked on the joined path, not the raw entry.
func TestExtraBinDirsRejectsHomeEscape(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	}

	for _, rel := range []string{
		"../escaped/bin",
		"sub/../../escaped/bin",
		"..",
		".",     // resolves to $HOME itself; scanning the home dir is not intended
		"a/../", // resolves to $HOME as well
	} {
		got := extraBinDirs(&Tool{Name: "escapist", ExecName: "x", ExtraBinDirs: []string{rel}})
		if len(got) != 0 {
			t.Errorf("extraBinDirs(%q) = %v, want none (must stay strictly under $HOME)", rel, got)
		}
	}

	// A normal nested entry still resolves, so the guard isn't over-broad.
	got := extraBinDirs(&Tool{Name: "ok", ExecName: "x", ExtraBinDirs: []string{"nested/deep/bin"}})
	want := filepath.Join(home, "nested", "deep", "bin")
	if len(got) != 1 || got[0] != want {
		t.Errorf("extraBinDirs(nested/deep/bin) = %v, want [%s]", got, want)
	}
}

// TestToolByExecName_DeterministicOnDuplicates pins the tie-break. Registry
// is a map, so without an explicit ordering the winner among entries that
// share an ExecName varies per run — and with it, whose ExtraBinDirs get
// searched. Several entries running one binary is a legitimate shape: the
// retired provider presets all had ExecName "claude".
func TestToolByExecName_DeterministicOnDuplicates(t *testing.T) {
	const exe = "everyapi-dup-exec-zzz"
	for _, key := range []string{"zzz-dup-b", "zzz-dup-a", "zzz-dup-c"} {
		Registry[key] = &Tool{Name: key, ExecName: exe}
		defer delete(Registry, key)
	}

	first := toolByExecName(exe)
	if first == nil {
		t.Fatal("toolByExecName returned nil for a registered duplicate ExecName")
	}
	// Keys are sorted, so the lexicographically smallest key always wins.
	if first.Name != "zzz-dup-a" {
		t.Errorf("toolByExecName picked %q, want the lowest-sorting key zzz-dup-a", first.Name)
	}
	for i := 0; i < 50; i++ {
		if got := toolByExecName(exe); got != first {
			t.Fatalf("toolByExecName is nondeterministic: got %q then %q", first.Name, got.Name)
		}
	}
}

// TestToolByExecName_ReachesEntriesMissingFromNames is the regression this
// helper's Registry-over-Names() choice exists for. Iterating Names() would
// skip a registered tool that nobody added to the picker ordering, silently
// dropping its ExtraBinDirs; the two lists happen to agree today, so the
// gap only shows up with an entry deliberately absent from Names().
func TestToolByExecName_ReachesEntriesMissingFromNames(t *testing.T) {
	const key = "zzz-unlisted"
	const exe = "everyapi-unlisted-exec-zzz"
	Registry[key] = &Tool{Name: key, ExecName: exe, ExtraBinDirs: []string{".local/bin"}}
	defer delete(Registry, key)

	for _, listed := range Names() {
		if listed == key {
			t.Fatalf("%q leaked into Names(); this test needs an entry that is NOT listed", key)
		}
	}
	if got := toolByExecName(exe); got == nil {
		t.Errorf("toolByExecName(%q) = nil for a Registry entry missing from Names()", exe)
	}
}

// TestCurlInstallersDeclareTheirOutputDir is the breadth check the
// ExtraBinDirs mechanism needs to stay honest. Every curl|bash entry in
// the Registry installs to a fixed directory rather than to $PATH, so
// each one needs ExtraBinDirs or it keeps the reinstall loop this
// mechanism was added to kill — the gemini fix alone left claude and
// hermes broken. npm tools are exempt: npmEnvBinDirs already covers them.
func TestCurlInstallersDeclareTheirOutputDir(t *testing.T) {
	for key, tool := range Registry {
		if tool.InstallCmd == "" || installUsesNpm(tool) {
			continue
		}
		if len(tool.ExtraBinDirs) == 0 {
			t.Errorf("%s has a non-npm installer (%q) but no ExtraBinDirs: a successful "+
				"install that lands off $PATH will re-prompt forever", key, tool.InstallCmd)
		}
	}
}

// TestNonNpmToolsResolveFromTheirInstallDir is the behavioral counterpart:
// with the binary in the declared directory and $PATH unable to see it,
// resolution must succeed for every non-npm tool — the exact post-install
// re-check RunInstall performs.
func TestNonNpmToolsResolveFromTheirInstallDir(t *testing.T) {
	for key, tool := range Registry {
		if tool.InstallCmd == "" || installUsesNpm(tool) || len(tool.ExtraBinDirs) == 0 {
			continue
		}
		t.Run(key, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			if runtime.GOOS == "windows" {
				t.Setenv("USERPROFILE", home)
			}
			// Empty PATH so only the ExtraBinDirs search can succeed.
			t.Setenv("PATH", "")

			dir := filepath.Join(home, filepath.FromSlash(tool.ExtraBinDirs[0]))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			writeFakeExec(t, dir, tool.ExecName)

			got, err := ResolveExec(tool)
			if err != nil {
				t.Fatalf("ResolveExec(%s) errored with the binary in %s: %v", key, dir, err)
			}
			assertResolvesInto(t, got, dir)
			if !IsInstalled(tool) {
				t.Errorf("IsInstalled(%s) = false despite the binary being in its install dir", key)
			}
		})
	}
}

// TestGeminiAutoInstall pins the registry wiring that makes
// `everyapi use gemini` self-install: without an InstallCmd,
// CanAutoInstall is false and the user only ever sees the hint.
func TestGeminiAutoInstall(t *testing.T) {
	tool, err := Lookup("gemini")
	if err != nil {
		t.Fatalf("Lookup(gemini): %v", err)
	}
	if tool.ExecName != "agy" {
		t.Errorf("ExecName = %q, want agy", tool.ExecName)
	}
	if runtime.GOOS != "windows" && !CanAutoInstall(tool) {
		t.Error("CanAutoInstall(gemini) = false, want true so `use` can offer the install")
	}
	// The installer is a remote shell pipeline, so a bare Enter must not
	// run it (InstallPromptDefault is derived from InstallCmdUnixOnly).
	if !tool.InstallCmdUnixOnly {
		t.Error("InstallCmdUnixOnly = false; curl|bash must stay Unix-gated and default the prompt to N")
	}
	if tool.InstallPromptDefault() {
		t.Error("InstallPromptDefault = true; a remote install script must not run on a bare Enter")
	}
	// ~/.local/bin is where Antigravity's install.sh writes the binary;
	// dropping it would reintroduce the reinstall loop.
	if len(tool.ExtraBinDirs) == 0 {
		t.Fatal("ExtraBinDirs is empty, want the installer's output dir")
	}
	if tool.ExtraBinDirs[0] != ".local/bin" {
		t.Errorf("ExtraBinDirs[0] = %q, want .local/bin", tool.ExtraBinDirs[0])
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
