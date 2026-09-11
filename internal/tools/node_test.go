package tools

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// The pinned digests are the whole security story of the Node bootstrap: the published SHASUMS256.txt is deliberately not fetched, so a wrong or malformed digest here is not caught anywhere else.
func TestNodeArchivePinsAreWellFormed(t *testing.T) {
	digest := regexp.MustCompile(`^[0-9a-f]{64}$`)
	if !strings.HasPrefix(nodeVersion, "v") {
		t.Fatalf("nodeVersion = %q, want a leading v", nodeVersion)
	}
	seen := make(map[string]string, len(nodeArchives))
	for platform, archive := range nodeArchives {
		if !digest.MatchString(archive.sha256) {
			t.Errorf("%s: sha256 = %q, want 64 lowercase hex characters", platform, archive.sha256)
		}
		if !strings.Contains(archive.file, nodeVersion) {
			t.Errorf("%s: file %q does not carry version %s, so the pin and the URL would disagree", platform, archive.file, nodeVersion)
		}
		if !strings.HasSuffix(archive.file, ".zip") && !strings.HasSuffix(archive.file, ".tar.gz") {
			t.Errorf("%s: file %q is neither .zip nor .tar.gz; Go's standard library cannot unpack anything else and this must not need a new dependency", platform, archive.file)
		}
		if other, dup := seen[archive.sha256]; dup {
			t.Errorf("%s and %s share a digest, so at least one pin is wrong", platform, other)
		}
		seen[archive.sha256] = platform
	}
}

// Every platform Connect ships on must be bootstrappable. ARM64 Windows is listed explicitly because it is the platform the PowerShell mirror installers cannot serve at all.
func TestNodeArchivesCoverEveryShippedPlatform(t *testing.T) {
	for _, platform := range []string{
		"windows/amd64", "windows/arm64",
		"darwin/amd64", "darwin/arm64",
		"linux/amd64", "linux/arm64",
	} {
		if _, ok := nodeArchives[platform]; !ok {
			t.Errorf("no pinned Node archive for %s, so npm cannot be bootstrapped there", platform)
		}
	}
}

func TestNodeArchiveBaseURLsPreferOfficialThenMirror(t *testing.T) {
	if len(nodeArchiveBaseURLs) < 2 {
		t.Fatalf("nodeArchiveBaseURLs = %q, want an official origin and a mirror", nodeArchiveBaseURLs)
	}
	if !strings.HasPrefix(nodeArchiveBaseURLs[0], "https://nodejs.org/") {
		t.Errorf("first Node origin = %q, want the official nodejs.org", nodeArchiveBaseURLs[0])
	}
	if !strings.Contains(strings.Join(nodeArchiveBaseURLs, " "), "npmmirror.com") {
		t.Errorf("Node origins have no China-reachable mirror: %q", nodeArchiveBaseURLs)
	}
	for _, url := range nodeArchiveBaseURLs {
		if !strings.HasSuffix(url, "/") {
			t.Errorf("base URL %q must end in / so the file name appends cleanly", url)
		}
		if !strings.Contains(url, nodeVersion) {
			t.Errorf("base URL %q does not pin %s", url, nodeVersion)
		}
	}
}

// The Windows zip puts node.exe and npm.cmd at the archive root while the Unix tarballs use bin/. Getting this wrong means the bootstrap downloads 50 MB and then reports that npm is missing.
func TestManagedNodeBinDirMatchesTheArchiveLayout(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	binDir := managedNodeBinDir()
	if binDir == "" {
		t.Skip("no pinned Node archive for this platform")
	}
	unpacked := strings.TrimSuffix(strings.TrimSuffix(nodeArchives[runtime.GOOS+"/"+runtime.GOARCH].file, ".zip"), ".tar.gz")
	if runtime.GOOS == "windows" {
		if filepath.Base(binDir) != unpacked {
			t.Errorf("Windows bin dir = %q, want the archive root %q", binDir, unpacked)
		}
		return
	}
	if filepath.Base(binDir) != "bin" || filepath.Base(filepath.Dir(binDir)) != unpacked {
		t.Errorf("Unix bin dir = %q, want %s/bin", binDir, unpacked)
	}
}

// The whole point of setting npm_config_prefix is that a bootstrapped install lands where everything else already looks. If the prefix and the resolver ever drift apart, an install "succeeds" and the tool still reports as missing — the exact loop this work exists to close.
func TestBootstrappedNpmPrefixIsResolvable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))

	binDir := managedNpmBinDir()
	if binDir == "" {
		t.Fatal("managedNpmBinDir is empty, so a bootstrapped install would be unresolvable")
	}
	prefix, err := managedNpmPrefix()
	if err != nil {
		t.Fatalf("managedNpmPrefix: %v", err)
	}
	// npm puts binaries in <prefix>/bin on POSIX and directly in <prefix> on Windows. Encoding the wrong one silently breaks every bootstrapped install.
	wantBin := filepath.Join(prefix, "bin")
	if runtime.GOOS == "windows" {
		wantBin = prefix
	}
	if binDir != wantBin {
		t.Fatalf("managedNpmBinDir = %q, want %q for npm's %s layout", binDir, wantBin, runtime.GOOS)
	}

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A registered npm tool, because both resolvers reach the bootstrapped prefix through the registry. Empty PATH so a real installation on the developer's machine cannot answer instead.
	t.Setenv("PATH", t.TempDir())
	tool, err := Lookup("opencode")
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(binDir, tool.ExecName)
	if runtime.GOOS == "windows" {
		exe += ".cmd"
	}
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveExec(tool)
	if err != nil {
		t.Fatalf("ResolveExec did not find a tool installed at the bootstrapped npm prefix %s: %v", binDir, err)
	}
	if got != exe {
		t.Errorf("ResolveExec = %q, want %q", got, exe)
	}
	if _, _, ok := LookupExecName(tool.ExecName); !ok {
		t.Errorf("LookupExecName cannot see %s at the bootstrapped npm prefix", tool.ExecName)
	}
}

// Installing through the bootstrap is only half the job: `npm install -g` writes a launcher, not a self-contained binary, so the tool it installs still needs `node` at every launch. A user who had no npm has no node either, and the bootstrapped runtime lives in a directory deliberately on nobody's PATH — so unless the launcher's PATH carries it, the install succeeds and the first run dies with `env: node: No such file or directory`.
func TestLaunchEnvCarriesTheBootstrappedNodeRuntime(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("PATH", t.TempDir())

	nodeBin := managedNodeBinDir()
	if nodeBin == "" {
		t.Skip("no pinned Node archive for this platform")
	}
	if err := os.MkdirAll(nodeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	node := filepath.Join(nodeBin, "node")
	if runtime.GOOS == "windows" {
		node += ".exe"
	}
	if err := os.WriteFile(node, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	npmBin := managedNpmBinDir()
	if err := os.MkdirAll(npmBin, 0o755); err != nil {
		t.Fatal(err)
	}
	tool, err := Lookup("opencode")
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(npmBin, tool.ExecName)
	if runtime.GOOS == "windows" {
		exe += ".cmd"
	}
	if err := os.WriteFile(exe, []byte("#!/usr/bin/env node\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, env, ok := LookupExecName(tool.ExecName)
	if !ok {
		t.Fatalf("LookupExecName cannot see %s at the bootstrapped npm prefix", tool.ExecName)
	}
	if !envPathContains(env, nodeBin) {
		t.Errorf("launch PATH does not carry the bootstrapped Node runtime %s, so the tool's shebang cannot find node: %q", nodeBin, env)
	}

	// The same must hold when the tool itself IS on PATH: ~/.local/bin is commonly on a user's PATH while EveryAPI's private Node directory never is.
	t.Setenv("PATH", npmBin)
	_, env, ok = LookupExecName(tool.ExecName)
	if !ok {
		t.Fatalf("LookupExecName cannot see %s on PATH", tool.ExecName)
	}
	if !envPathContains(env, nodeBin) {
		t.Errorf("an on-PATH tool installed through the bootstrap still needs node: %q", env)
	}
}

func envPathContains(env []string, dir string) bool {
	for _, kv := range env {
		i := strings.IndexByte(kv, '=')
		if i < 0 || !pathKeyEquals(kv[:i]) {
			continue
		}
		for _, entry := range filepath.SplitList(kv[i+1:]) {
			if entry != "" && samePath(entry, dir) {
				return true
			}
		}
	}
	return false
}

// The bootstrapped prefix resolves to a shared directory, so it must be reached through the registry only — never by scanning it for whatever binary happens to share a name.
func TestBootstrappedNpmPrefixIsNotScannedForUnregisteredNames(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("PATH", t.TempDir())

	binDir := managedNpmBinDir()
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const unregistered = "everyapi-not-a-registered-tool-zzz"
	exe := filepath.Join(binDir, unregistered)
	if runtime.GOOS == "windows" {
		exe += ".cmd"
	}
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if p, _, ok := LookupExecName(unregistered); ok {
		t.Errorf("LookupExecName(%q) = %q, want a miss: the prefix must not be scanned for unregistered names", unregistered, p)
	}
}

func TestEnsureNodeRejectsUnsupportedPlatformsWithoutDownloading(t *testing.T) {
	if _, ok := nodeArchives[runtime.GOOS+"/"+runtime.GOARCH]; !ok {
		if _, err := EnsureNode(t.Context(), nil); !errors.Is(err, ErrNodeUnsupportedPlatform) {
			t.Errorf("EnsureNode on an unpinned platform = %v, want ErrNodeUnsupportedPlatform", err)
		}
		return
	}
	if !canBootstrapNode() {
		t.Error("canBootstrapNode disagrees with nodeArchives for the running platform")
	}
}

// Node's Unix tarballs ship bin/npm as a symlink into lib/node_modules. An extractor that drops link entries yields a runtime with no usable npm, which defeats the entire bootstrap.
func TestExtractNodeTarGzPreservesSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows; the Windows path uses zip")
	}
	archive := buildTarGz(t, []tarEntry{
		{name: "node-test/lib/npm-cli.js", contents: "console.log(1)"},
		{name: "node-test/bin/npm", link: "../lib/npm-cli.js"},
	})
	dest := t.TempDir()
	if err := extractNodeTarGz(archive, dest); err != nil {
		t.Fatalf("extractNodeTarGz: %v", err)
	}
	link := filepath.Join(dest, "node-test/bin/npm")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("Readlink(%s): %v — npm was not extracted as a symlink", link, err)
	}
	if target != "../lib/npm-cli.js" {
		t.Errorf("symlink target = %q, want ../lib/npm-cli.js", target)
	}
	if _, err := os.Stat(link); err != nil {
		t.Errorf("extracted npm symlink does not resolve: %v", err)
	}
}

func TestExtractNodeTarGzRejectsPathEscape(t *testing.T) {
	archive := buildTarGz(t, []tarEntry{{name: "../escaped", contents: "pwned"}})
	dest := t.TempDir()
	if err := extractNodeTarGz(archive, dest); err == nil {
		t.Fatal("extractNodeTarGz accepted an entry escaping the destination")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dest), "escaped")); err == nil {
		t.Error("a tar entry escaped the extraction directory")
	}
}

func TestExtractNodeTarGzRejectsAbsoluteAndEscapingSymlinks(t *testing.T) {
	for name, entry := range map[string]tarEntry{
		"absolute": {name: "node-test/bin/npm", link: "/etc/passwd"},
		"escaping": {name: "node-test/bin/npm", link: "../../../../etc/passwd"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := extractNodeTarGz(buildTarGz(t, []tarEntry{entry}), t.TempDir()); err == nil {
				t.Errorf("extractNodeTarGz accepted a %s symlink target %q", name, entry.link)
			}
		})
	}
}

func TestExtractNodeZipRejectsZipSlip(t *testing.T) {
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	entry, err := writer.Create("../escaped.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("pwned")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if err := extractNodeZip(buf.Bytes(), dest); err == nil {
		t.Fatal("extractNodeZip accepted an entry escaping the destination")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dest), "escaped.txt")); err == nil {
		t.Error("a zip entry escaped the extraction directory")
	}
}

func TestSafeArchivePathAllowsNestedEntriesAndRejectsTraversal(t *testing.T) {
	dest := filepath.Clean(t.TempDir())
	if _, err := safeArchivePath(dest, "node/bin/npm"); err != nil {
		t.Errorf("safeArchivePath rejected a legitimate nested entry: %v", err)
	}
	// Cleaned by filepath.Join, so a prefix check on the raw name would miss this one.
	for _, name := range []string{"../escaped", "node/../../escaped", "node/sub/../../../escaped"} {
		if _, err := safeArchivePath(dest, name); err == nil {
			t.Errorf("safeArchivePath accepted traversal entry %q", name)
		}
	}
}

// A missing npm must NOT gate the install on a platform that can bootstrap Node, because RunInstall is about to supply it. Every other prerequisite still gates.
func TestInstallerMissingDefersToTheNodeBootstrap(t *testing.T) {
	npmTool := &Tool{Name: "npm-tool", ExecName: "npm-tool", InstallCmd: "npm install -g npm-tool"}
	// Point PATH at an empty directory rather than skipping when the dev machine has npm: otherwise the branch this test exists for runs nowhere, least of all in CI.
	t.Setenv("PATH", t.TempDir())
	if _, err := exec.LookPath("npm"); err == nil {
		t.Fatal("npm still resolves after PATH was emptied; the test cannot exercise the bootstrap branch")
	}
	got := InstallerMissing(npmTool)
	if canBootstrapNode() && got != "" {
		t.Errorf("InstallerMissing = %q on a bootstrappable platform, want \"\" so the Node download can run", got)
	}
	if !canBootstrapNode() && got != "npm" {
		t.Errorf("InstallerMissing = %q with no pinned Node archive, want \"npm\"", got)
	}
	// An installer needing something we cannot download still has to gate.
	curlTool := &Tool{Name: "curl-tool", ExecName: "curl-tool", InstallCmd: "definitely-not-a-real-binary --install"}
	if got := InstallerMissing(curlTool); got != "definitely-not-a-real-binary" {
		t.Errorf("InstallerMissing(non-npm prerequisite) = %q, want the missing command", got)
	}
}

func TestErrInstallerExecBlockedExplainsThePolicyCause(t *testing.T) {
	tool := &Tool{Name: "claude", ExecName: "claude", InstallCmd: "curl -fsSL https://example.test/install.sh | bash"}
	err := &ErrInstallerExecBlocked{Tool: tool, Executable: "powershell", Err: os.ErrPermission}
	message := err.Error()
	for _, want := range []string{"claude", "powershell", "install.sh"} {
		if !strings.Contains(message, want) {
			t.Errorf("ErrInstallerExecBlocked message %q does not mention %q", message, want)
		}
	}
	if !errors.Is(err, os.ErrPermission) {
		t.Error("ErrInstallerExecBlocked must unwrap to the underlying permission error")
	}
}

// The bare os.ErrNotExist this replaced reached users as "file does not exist", naming neither the tool nor anywhere it looked.
func TestErrExecNotResolvedNamesTheToolAndSearchedDirs(t *testing.T) {
	err := &ErrExecNotResolved{Tool: &Tool{ExecName: "claude"}, Dirs: []string{"/home/u/.local/bin"}}
	message := err.Error()
	if !strings.Contains(message, "claude") || !strings.Contains(message, "/home/u/.local/bin") {
		t.Errorf("ErrExecNotResolved message %q must name the tool and the directories searched", message)
	}
	if message == os.ErrNotExist.Error() {
		t.Error("ErrExecNotResolved still reads as the bare 'file does not exist'")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Error("ErrExecNotResolved must keep unwrapping to os.ErrNotExist for existing callers")
	}
}

type tarEntry struct {
	name     string
	contents string
	link     string
}

func buildTarGz(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gzipWriter := gzip.NewWriter(&buf)
	writer := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		if entry.link != "" {
			if err := writer.WriteHeader(&tar.Header{
				Name: entry.name, Typeflag: tar.TypeSymlink, Linkname: entry.link, Mode: 0o777,
			}); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := writer.WriteHeader(&tar.Header{
			Name: entry.name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(entry.contents)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(entry.contents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
