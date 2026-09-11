package tools

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Node bootstrap: when a tool's installer is `npm install -g …` and the user has no npm, EveryAPI downloads a private Node runtime instead of failing with "install npm, then re-run".
//
// Why a private runtime rather than a system package manager: winget/choco/brew/apt each need elevation, differ per platform, and mutate state the user did not ask us to touch. A verified archive unpacked into an EveryAPI-owned directory needs no privileges, works identically everywhere, and is trivially removable.
//
// SECURITY INVARIANT: nodeVersion, the archive file names and their SHA-256 digests are compile-time literals, the same rule Tool.InstallCmd carries. The digests below were read from https://nodejs.org/dist/v24.21.0/SHASUMS256.txt. They are pinned in source ON PURPOSE and the published SHASUMS256.txt is deliberately NOT fetched at runtime: a checksum retrieved from the same origin as the payload authenticates nothing, because whoever can replace the archive can replace the checksum beside it. Pinning moves that trust to code review, where a digest change is a visible diff.
const nodeVersion = "v24.21.0"

// Official origin first, npmmirror second. Both were verified to serve every archive named below. The mirror exists because nodejs.org is slow-to-unreachable from mainland China, the same reason the npm installers carry registry fallbacks.
var nodeArchiveBaseURLs = []string{
	"https://nodejs.org/dist/" + nodeVersion + "/",
	"https://cdn.npmmirror.com/binaries/node/" + nodeVersion + "/",
}

const (
	nodeDownloadTimeout = 5 * time.Minute
	nodeMaxArchiveBytes = 128 << 20
)

type nodeArchive struct {
	file   string
	sha256 string
}

// nodeArchives maps GOOS/GOARCH to its pinned archive. Linux uses .tar.gz rather than the smaller .tar.xz because Go's standard library has no xz decoder and a private Node runtime is not worth a new dependency for. Windows on arm64 is included deliberately: it is the platform where the PowerShell mirror installers cannot help at all.
var nodeArchives = map[string]nodeArchive{
	"windows/amd64": {"node-" + nodeVersion + "-win-x64.zip", "158f7685b44de51f6c0df1d153526cbcd3e1bc739a8dfc607721cef75de9e541"},
	"windows/arm64": {"node-" + nodeVersion + "-win-arm64.zip", "8779b1bde1d39f8d420e3b57aa657b39891af434d3de44a919044cec06785921"},
	"darwin/amd64":  {"node-" + nodeVersion + "-darwin-x64.tar.gz", "1462cb3b3046b815cf8ea436d3da450ec1a9f11dac7e5a46b0ada5305d7e8097"},
	"darwin/arm64":  {"node-" + nodeVersion + "-darwin-arm64.tar.gz", "bed7eea5325e1108f32ce5228ddd6a5f0f08a499ee42aa7442aea583702f6057"},
	"linux/amd64":   {"node-" + nodeVersion + "-linux-x64.tar.gz", "6e1db87ef58b8819e5d5402eff1536491b18edd8eb7bee5ef7897876e88dc5ff"},
	"linux/arm64":   {"node-" + nodeVersion + "-linux-arm64.tar.gz", "724282c3b43aec998aa9527380465b45d229e021b58035f5f4f63095eabfe5d5"},
}

// ErrNodeUnsupportedPlatform reports that no Node archive is pinned for the running platform, so the npm bootstrap cannot run and the caller must fall back to telling the user to install Node themselves.
var ErrNodeUnsupportedPlatform = errors.New("no pinned Node.js archive for this platform")

// managedNodeRoot is the EveryAPI-owned directory holding the bootstrapped runtime. It follows the same conventions as the rest of the CLI: %LOCALAPPDATA%\EveryAPI on Windows (matching WindowsLocalAppDataBinDirs) and ~/.local/share/everyapi elsewhere (matching the edge workdir).
func managedNodeRoot() (string, error) {
	if runtime.GOOS == "windows" {
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			return filepath.Join(localAppData, "EveryAPI", "node"), nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "everyapi", "node"), nil
}

// managedNodeBinDir returns the directory holding node/npm inside the unpacked archive, or "" when this platform has no pinned archive or the home dir cannot be determined. The layout differs by platform: the Windows zip puts node.exe and npm.cmd at the archive root, while the Unix tarballs use a bin/ subdirectory.
//
// It reports the path whether or not anything is installed there — callers that need "is it actually present" use findExecutable on the result, which is also what lets resolveExecDirs list this directory as a candidate without ever triggering a download.
func managedNodeBinDir() string {
	archive, ok := nodeArchives[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok {
		return ""
	}
	root, err := managedNodeRoot()
	if err != nil {
		return ""
	}
	unpacked := filepath.Join(root, strings.TrimSuffix(strings.TrimSuffix(archive.file, ".zip"), ".tar.gz"))
	if runtime.GOOS == "windows" {
		return unpacked
	}
	return filepath.Join(unpacked, "bin")
}

// managedNpmPrefix is the --prefix handed to a bootstrapped `npm install -g`, and managedNpmBinDir is where that prefix puts executables.
//
// These deliberately point at directories the rest of EveryAPI ALREADY searches — ~/.local/bin via Tool.ExtraBinDirs and %LOCALAPPDATA%\EveryAPI\bin via Tool.WindowsLocalAppDataBinDirs, both of which Connect's own detection candidates also list. Letting npm use its default prefix would instead bury the package inside the versioned Node directory, a location nothing else knows about, so an install would "succeed" and every subsystem would still report the tool as missing. npm's layout differs per platform: POSIX puts binaries in <prefix>/bin, Windows puts them directly in <prefix>.
func managedNpmPrefix() (string, error) {
	if runtime.GOOS == "windows" {
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			return filepath.Join(localAppData, "EveryAPI", "bin"), nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(home, "AppData", "Local", "EveryAPI", "bin"), nil
	}
	return filepath.Join(home, ".local"), nil
}

func managedNpmBinDir() string {
	prefix, err := managedNpmPrefix()
	if err != nil {
		return ""
	}
	if runtime.GOOS == "windows" {
		return prefix
	}
	return filepath.Join(prefix, "bin")
}

// managedNodeInstalled reports whether the bootstrapped runtime is already unpacked and usable, which makes EnsureNode idempotent and keeps a second install attempt from re-downloading 50 MB.
func managedNodeInstalled() (string, bool) {
	binDir := managedNodeBinDir()
	if binDir == "" {
		return "", false
	}
	if _, ok := findExecutable(binDir, "npm"); !ok {
		return "", false
	}
	return binDir, true
}

// EnsureNode makes a Node runtime available to EveryAPI and returns the directory holding node and npm. An already-bootstrapped runtime is returned without touching the network. Progress lines go to progress, which may be nil.
//
// The runtime is private: nothing is added to the user's PATH, no shell profile is edited, and no system package manager runs. Callers put the returned directory on the PATH of the child process they are about to spawn — installEnv for the installer itself, withManagedNodeOnPath for every later launch of a tool installed this way — and nowhere else. On Windows in particular, writing the user PATH would not help the already-running process anyway — environment changes only reach processes started afterwards.
func EnsureNode(ctx context.Context, progress io.Writer) (string, error) {
	if binDir, ok := managedNodeInstalled(); ok {
		return binDir, nil
	}
	archive, ok := nodeArchives[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok {
		return "", fmt.Errorf("%w (%s/%s)", ErrNodeUnsupportedPlatform, runtime.GOOS, runtime.GOARCH)
	}
	root, err := managedNodeRoot()
	if err != nil {
		return "", fmt.Errorf("locate the EveryAPI Node directory: %w", err)
	}

	printProgress(progress, fmt.Sprintf("Downloading Node.js %s (%s)…", nodeVersion, archive.file))
	downloadCtx, cancel := context.WithTimeout(ctx, nodeDownloadTimeout)
	defer cancel()
	payload, err := downloadNodeArchive(downloadCtx, archive)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", root, err)
	}
	// Unpack into a staging directory and rename into place, so an interrupted or failed extraction can never leave a half-written runtime that managedNodeInstalled would then report as usable.
	staging, err := os.MkdirTemp(root, ".unpack-*")
	if err != nil {
		return "", fmt.Errorf("create a staging directory under %s: %w", root, err)
	}
	defer os.RemoveAll(staging)

	printProgress(progress, "Verifying and unpacking…")
	if err := extractNodeArchive(payload, archive.file, staging); err != nil {
		return "", err
	}

	unpackedName := strings.TrimSuffix(strings.TrimSuffix(archive.file, ".zip"), ".tar.gz")
	extracted := filepath.Join(staging, unpackedName)
	if _, err := os.Stat(extracted); err != nil {
		return "", fmt.Errorf("the Node.js archive did not contain %s: %w", unpackedName, err)
	}
	target := filepath.Join(root, unpackedName)
	_ = os.RemoveAll(target)
	if err := os.Rename(extracted, target); err != nil {
		return "", fmt.Errorf("install the Node.js runtime into %s: %w", target, err)
	}

	binDir, ok := managedNodeInstalled()
	if !ok {
		return "", fmt.Errorf("unpacked Node.js into %s but npm is not there", target)
	}
	printProgress(progress, fmt.Sprintf("Node.js %s ready in %s", nodeVersion, target))
	return binDir, nil
}

// downloadNodeArchive tries each base URL in order and verifies the pinned digest before accepting the payload. A mismatch is fatal for that origin but not for the attempt: the next mirror still gets a turn, so one poisoned or truncated mirror cannot block the bootstrap.
func downloadNodeArchive(ctx context.Context, archive nodeArchive) ([]byte, error) {
	var failures []error
	for _, base := range nodeArchiveBaseURLs {
		url := base + archive.file
		payload, err := httpGetBytes(ctx, url, nodeMaxArchiveBytes)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", url, err))
			continue
		}
		sum := sha256.Sum256(payload)
		if digest := hex.EncodeToString(sum[:]); digest != archive.sha256 {
			failures = append(failures, fmt.Errorf("%s: checksum mismatch (expected %s, got %s)", url, archive.sha256, digest))
			continue
		}
		return payload, nil
	}
	return nil, fmt.Errorf("download Node.js %s: %w", nodeVersion, errors.Join(failures...))
}

func httpGetBytes(ctx context.Context, url string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %s", response.Status)
	}
	// Read one byte past the cap so exceeding it is an explicit error rather than a silent truncation. A truncated payload would fail the digest check on every mirror and be reported as "checksum mismatch", sending the reader after a supply-chain problem that isn't there.
	payload, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > limit {
		return nil, fmt.Errorf("response exceeds the %d-byte limit", limit)
	}
	return payload, nil
}

func extractNodeArchive(payload []byte, name, destDir string) error {
	if strings.HasSuffix(name, ".zip") {
		return extractNodeZip(payload, destDir)
	}
	return extractNodeTarGz(payload, destDir)
}

func extractNodeZip(payload []byte, destDir string) error {
	reader, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return err
	}
	for _, file := range reader.File {
		targetPath, err := safeArchivePath(destDir, file.Name)
		if err != nil {
			return err
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}
		mode := file.Mode()
		// A symlink entry's body is its target path, not file content. Writing it through as a regular file would produce a plausible-looking runtime whose "node.exe" is a few bytes of text. Node's Windows zips carry none, so refusing is the honest response to an archive that changed shape under a pin we still matched.
		if mode&os.ModeSymlink != 0 {
			return fmt.Errorf("archive entry %s is a symlink, which the zip extractor does not support", file.Name)
		}
		if mode.Perm() == 0 {
			mode = 0o644
		}
		entry, err := file.Open()
		if err != nil {
			return err
		}
		err = writeArchiveFile(targetPath, entry, mode)
		entry.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// extractNodeTarGz must handle symlinks, not skip them: the Unix tarballs ship bin/npm and bin/npx as links into lib/node_modules, so an extractor that drops link entries produces a runtime with no usable npm — the exact thing this bootstrap exists to provide.
func extractNodeTarGz(payload []byte, destDir string) error {
	gzipReader, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		targetPath, err := safeArchivePath(destDir, header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return err
			}
			if err := writeArchiveFile(targetPath, reader, os.FileMode(header.Mode).Perm()); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return err
			}
			// A symlink target is not a path we write to, but it can still point outside the destination. Only relative links that stay inside the extraction root are accepted; Node's own tarballs only ever use those.
			if filepath.IsAbs(header.Linkname) {
				return fmt.Errorf("archive symlink %s has an absolute target %s", header.Name, header.Linkname)
			}
			if _, err := safeArchivePath(destDir, filepath.Join(filepath.Dir(header.Name), header.Linkname)); err != nil {
				return fmt.Errorf("archive symlink %s escapes the destination: %w", header.Name, err)
			}
			_ = os.Remove(targetPath)
			if err := os.Symlink(header.Linkname, targetPath); err != nil {
				return err
			}
		}
	}
}

// safeArchivePath joins an archive entry name onto destDir and rejects anything that escapes it, which is the zip-slip / tar-slip guard. filepath.Join cleans its result, so a "../" prefix alone is not a reliable test; comparing the joined path back against the destination is.
func safeArchivePath(destDir, name string) (string, error) {
	targetPath := filepath.Join(destDir, name)
	if targetPath != filepath.Clean(destDir) && !strings.HasPrefix(targetPath, filepath.Clean(destDir)+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive entry escapes destination: %s", name)
	}
	return targetPath, nil
}

func writeArchiveFile(targetPath string, source io.Reader, mode os.FileMode) error {
	if mode == 0 {
		mode = 0o644
	}
	out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, source)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func printProgress(progress io.Writer, line string) {
	if progress == nil {
		return
	}
	fmt.Fprintln(progress, line)
}
