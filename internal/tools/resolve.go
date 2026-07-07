package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ResolveExec returns the absolute path to the tool's executable.
//
// It first consults $PATH via exec.LookPath — the fast, universal case
// that every already-on-PATH tool hits without any extra work. When that
// fails AND the tool is installed with `npm install -g`, it falls back to
// the locations a global npm install ACTUALLY writes to: the bin dir a
// Node version manager (nvm/volta/fnm) exports into the environment, an
// explicit npm prefix, `npm prefix -g`, and the common static prefixes.
//
// This is the whole point of the fallback: `npm install -g @openai/codex`
// drops `codex` into npm's global bin directory, and on a great many
// setups (nvm, a hand-set prefix, Linux distros that don't add it by
// default) that directory is NOT on the $PATH everyapi inherits. Relying
// on exec.LookPath alone then makes the tool permanently invisible — the
// install "succeeds", the re-check still can't see it, and `everyapi use`
// loops forever offering to reinstall. Resolving the npm global bin dir
// directly and launching by absolute path removes that failure mode on
// every platform.
//
// Returns os.ErrNotExist when nothing resolves.
func ResolveExec(t *Tool) (string, error) {
	if t == nil {
		return "", os.ErrNotExist
	}
	if p, err := exec.LookPath(t.ExecName); err == nil {
		return p, nil
	}
	if installUsesNpm(t) {
		// Cheap, subprocess-free candidates first (env vars only), so the
		// common version-manager case resolves without ever shelling out.
		for _, dir := range npmEnvBinDirs() {
			if p, ok := findExecutable(dir, t.ExecName); ok {
				return p, nil
			}
		}
		// Last resort: ask npm itself where its global root is. Spawns a
		// process, so it only runs when the free lookups above all miss.
		if dir := npmPrefixBinDir(); dir != "" {
			if p, ok := findExecutable(dir, t.ExecName); ok {
				return p, nil
			}
		}
	}
	return "", os.ErrNotExist
}

// installUsesNpm reports whether the tool's auto-installer is a global
// npm install — the only class for which the npm-global-bin fallback in
// ResolveExec makes sense (a curl|bash installer writes elsewhere).
func installUsesNpm(t *Tool) bool {
	return installRequires(t) == "npm"
}

// npmSearchDirs returns the npm global bin directories ResolveExec would
// consult for t (env-derived plus `npm prefix -g`), so an
// "installed but still not found" message can name concrete directories
// to add to PATH instead of a vague hint. Empty for non-npm tools. This
// is the error-path variant — it always resolves the (subprocess-backed)
// prefix dir because we're already failing and want the fullest picture.
func npmSearchDirs(t *Tool) []string {
	if !installUsesNpm(t) {
		return nil
	}
	dirs := npmEnvBinDirs()
	if pd := npmPrefixBinDir(); pd != "" {
		dirs = append(dirs, pd)
	}
	return dedupeStrings(dirs)
}

// npmEnvBinDirs lists candidate global-bin directories derivable from the
// environment ALONE (no subprocess). Node version managers export their
// active install's bin dir, and everyapi inherits those even when the
// shell rc that would add them to $PATH wasn't sourced (login vs.
// non-login shells, GUI-launched terminals, etc.) — which is exactly the
// "installed but not on PATH" situation this rescues.
func npmEnvBinDirs() []string {
	var dirs []string
	add := func(d string) {
		if d != "" {
			dirs = append(dirs, d)
		}
	}

	add(os.Getenv("NVM_BIN")) // nvm: already a bin dir, not a prefix
	if v := os.Getenv("VOLTA_HOME"); v != "" {
		add(filepath.Join(v, "bin")) // volta
	}
	if f := os.Getenv("FNM_MULTISHELL_PATH"); f != "" {
		add(binSubdir(f)) // fnm
	}
	// Explicit npm prefix override (any of the three spellings npm honors).
	if p := firstNonEmptyEnv("npm_config_prefix", "NPM_CONFIG_PREFIX", "PREFIX"); p != "" {
		add(binSubdir(p))
	}
	// Common hand-configured global prefixes under $HOME.
	if home, err := os.UserHomeDir(); err == nil {
		add(binSubdir(filepath.Join(home, ".npm-global")))
		add(binSubdir(filepath.Join(home, ".npm-packages")))
	}
	// Platform defaults.
	if runtime.GOOS == "windows" {
		if ad := os.Getenv("APPDATA"); ad != "" {
			add(filepath.Join(ad, "npm")) // npm's default Windows global dir
		}
	} else {
		add("/usr/local/bin")
	}
	return dedupeStrings(dirs)
}

// npmPrefixBinDir resolves npm's global bin directory by asking npm
// itself (`npm prefix -g`). Returns "" when npm isn't a resolvable
// binary on $PATH — the classic version-manager case where `npm` exists
// only as a shell function, in which case the env-derived dirs above are
// the only signal we have. Time-boxed so a wedged npm can't hang launch.
func npmPrefixBinDir() string {
	npm, err := exec.LookPath("npm")
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, npm, "prefix", "-g").Output()
	if err != nil {
		return ""
	}
	return binSubdir(strings.TrimSpace(string(out)))
}

// binSubdir maps an npm PREFIX to the directory its executables live in:
// <prefix>/bin on Unix, and the prefix itself on Windows (npm writes its
// .cmd/.ps1 shims directly into the prefix there, not a bin subdir).
func binSubdir(prefix string) string {
	if prefix == "" {
		return ""
	}
	if runtime.GOOS == "windows" {
		return prefix
	}
	return filepath.Join(prefix, "bin")
}

// findExecutable looks for an executable named `name` in `dir`, returning
// its absolute path and true on the first hit.
//
// On Unix it requires a non-directory file with an execute bit set. On
// Windows it tries each PATHEXT extension (npm writes a `name.cmd` shim),
// since a bare extensionless name there is typically the git-bash shell
// script the OS can't CreateProcess.
func findExecutable(dir, name string) (string, bool) {
	if dir == "" || name == "" {
		return "", false
	}
	if runtime.GOOS == "windows" {
		for _, ext := range windowsExecExts(name) {
			p := filepath.Join(dir, name+ext)
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				return p, true
			}
		}
		return "", false
	}
	p := filepath.Join(dir, name)
	if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode().Perm()&0o111 != 0 {
		return p, true
	}
	return "", false
}

// windowsExecExts returns the filename extensions to try for `name` on
// Windows: the standard PATHEXT list (npm's shims are `.cmd`/`.exe`), plus
// a bare "" only when `name` already carries its own extension.
func windowsExecExts(name string) []string {
	var exts []string
	if filepath.Ext(name) != "" {
		exts = append(exts, "")
	}
	pathext := os.Getenv("PATHEXT")
	if pathext == "" {
		pathext = ".COM;.EXE;.BAT;.CMD"
	}
	for _, e := range strings.Split(pathext, ";") {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		exts = append(exts, e)
	}
	return exts
}

// firstNonEmptyEnv returns the value of the first set, non-empty env var
// among names, or "".
func firstNonEmptyEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

// dedupeStrings returns s with duplicates removed, preserving first-seen
// order.
func dedupeStrings(s []string) []string {
	seen := make(map[string]struct{}, len(s))
	out := s[:0:0]
	for _, v := range s {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
