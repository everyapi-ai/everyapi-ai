package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// ResolveExec returns the absolute path to the tool's executable.
//
// It first consults $PATH via exec.LookPath — the fast, universal case that every already-on-PATH tool hits without any extra work. When that misses it searches the tool's own ExtraBinDirs (where THIS tool's installer writes), and then, for `npm install -g` tools only, the locations a global npm install actually writes to: the bin dir a Node version manager (nvm/volta/fnm) exports into the environment, an explicit npm prefix, `npm prefix -g`, and the common static prefixes.
//
// This is the whole point of the fallback: an installer drops its binary somewhere the $PATH everyapi inherits doesn't cover — npm's global bin under nvm or a hand-set prefix, or ~/.local/bin added only by a shell rc file we never source. Relying on exec.LookPath alone then makes the tool permanently invisible: the install "succeeds", the re-check still can't see it, and `everyapi use` loops forever offering to reinstall. Resolving the installer's output directory directly and launching by absolute path removes that failure mode on every platform.
//
// Returns os.ErrNotExist when nothing resolves.
func ResolveExec(t *Tool) (string, error) {
	path, _, err := resolveExecDirs(t)
	return path, err
}

// LookupExecName resolves a bare executable name for launching, the way Exec launches tools: exec.LookPath first, then the ExtraBinDirs of the registry entry running that binary (if any), then the npm global bin dirs (env-derived, then `npm prefix -g`). The env return is ready for exec.Cmd.Env, with the resolved dir appended to the child's PATH so a co-located node/npm resolves — nil means "inherit unchanged" (the $PATH fast path). ok=false when nothing resolves; callers should keep the bare name so exec.Command's own not-found error still surfaces.
//
// The argument is an executable name, not a registry key: callers pass the binary they intend to run. This matters for entries whose names differ from their executable, such as Antigravity (`antigravity` -> `agy`).
//
// Exported for the mcp subcommands, which launch client CLIs outside the *Tool plumbing — without this they'd fail for exactly the off-PATH cohort `use` handles.
func LookupExecName(execName string) (path string, env []string, ok bool) {
	if p, err := exec.LookPath(execName); err == nil {
		return p, nil, true
	}
	// Mirror resolveExecDirs: an installer-specific dir beats npm's global root, and applies even when the tool isn't npm-installed.
	dirs := extraBinDirs(toolByExecName(execName))
	dirs = dedupeStrings(append(dirs, npmEnvBinDirs()...))
	if d := npmPrefixBinDir(); d != "" {
		dirs = dedupeStrings(append(dirs, d))
	}
	for _, dir := range dirs {
		if p, found := findExecutable(dir, execName); found {
			return p, mergeEnv(withExecDirOnPath(nil, p)), true
		}
	}
	return "", nil, false
}

// resolveExecDirs is ResolveExec plus the fallback directories it searched: the tool's ExtraBinDirs, plus the npm global bin candidates for npm-installed tools. The extra return lets the failure path (RunInstall) name the dirs in its error WITHOUT recomputing them — recomputing would spawn a second `npm prefix -g`. searched is nil when the $PATH fast path hits, and for a non-npm tool it holds just that tool's ExtraBinDirs (empty when it declares none).
func resolveExecDirs(t *Tool) (path string, searched []string, err error) {
	if t == nil {
		return "", nil, os.ErrNotExist
	}
	if p, ok := findToolOnPath(t); ok {
		return p, nil, nil
	}
	// Installer-specific directories come first: they're free to compute and they're where THIS tool's installer actually wrote the binary, which makes them a better guess than npm's global root. Applies to every tool, npm-installed or not — a curl|bash installer with a known output dir is exactly the case exec.LookPath alone strands.
	searched = extraBinDirs(t)
	for _, dir := range searched {
		if p, ok := findExecutable(dir, t.ExecName); ok {
			return p, searched, nil
		}
	}
	if !installUsesNpm(t) {
		return "", searched, os.ErrNotExist
	}
	// Cheap, subprocess-free candidates first (env vars only), so the common version-manager case resolves without ever shelling out.
	searched = dedupeStrings(append(searched, npmEnvBinDirs()...))
	for _, dir := range searched {
		if p, ok := findExecutable(dir, t.ExecName); ok {
			return p, searched, nil
		}
	}
	// Last resort: ask npm itself where its global root is. Spawns a process, so it only runs when the free lookups above all miss.
	if dir := npmPrefixBinDir(); dir != "" {
		searched = dedupeStrings(append(searched, dir))
		if p, ok := findExecutable(dir, t.ExecName); ok {
			return p, searched, nil
		}
	}
	return "", searched, os.ErrNotExist
}

// findToolOnPath mirrors exec.LookPath while allowing us to reject forwarding wrappers that are not installations of the requested tool. cmux bundles a `grok` shim in its own Resources/bin; that shim exits 127 unless a real Grok exists later on PATH. Treating the shim itself as Grok makes `everyapi use` skip the installer and fail at launch. Continue scanning so a real Grok later on PATH still wins.
func findToolOnPath(t *Tool) (string, bool) {
	if t == nil || t.ExecName == "" {
		return "", false
	}
	if t.ExecName != "grok" {
		path, err := exec.LookPath(t.ExecName)
		return path, err == nil
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if p, ok := findExecutable(dir, t.ExecName); ok && !isCmuxGrokWrapper(t, p) {
			return p, true
		}
	}
	return "", false
}

func isCmuxGrokWrapper(t *Tool, path string) bool {
	if t == nil || t.ExecName != "grok" || filepath.Base(path) != "grok" {
		return false
	}
	return filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(path))))) == "cmux.app"
}

// installUsesNpm reports whether the tool's auto-installer is a global npm install — the only class for which the npm-global-bin fallback in ResolveExec makes sense (a curl|bash installer writes elsewhere).
func installUsesNpm(t *Tool) bool {
	return installRequires(t) == "npm"
}

// toolByExecName finds the registry entry that launches execName, so callers holding only a bare binary name can still reach that tool's ExtraBinDirs. Returns nil when nothing matches — extraBinDirs treats that as "no extra candidates".
//
// Matching is on ExecName, not the registry key: Antigravity is registered as `antigravity` but launches `agy`, while Google's Gemini CLI launches the distinct `gemini` executable.
//
// Covers the whole Registry, not just Names() — the latter orders the interactive picker and would silently skip a registered tool that hadn't been added to it. Registry is a map, so the keys are sorted first: several entries sharing an ExecName is a legitimate shape (the retired provider presets all ran `claude`), and without an order the winner would vary per run and take its ExtraBinDirs with it.
func toolByExecName(execName string) *Tool {
	if execName == "" {
		return nil
	}
	keys := make([]string, 0, len(Registry))
	for name := range Registry {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		if t := Registry[name]; t != nil && t.ExecName == execName {
			return t
		}
	}
	return nil
}

// extraBinDirs resolves the tool's ExtraBinDirs against the user's home directory. Entries that are empty, absolute, or that escape $HOME via ".." are skipped: the field is documented as $HOME-relative, and silently accepting a path outside the home dir would make the "compile-time literal, home-anchored" contract harder to audit. Returns nil when the tool declares none or the home dir can't be determined.
//
// The ".." check can't be a plain prefix test on the raw entry — filepath.Join Cleans its result, so "sub/../../elsewhere" escapes without ever starting with "..". Comparing the joined path back against home is what actually enforces containment.
func extraBinDirs(t *Tool) []string {
	if t == nil {
		return nil
	}
	var dirs []string
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, safeRelativeDirs(home, t.ExtraBinDirs)...)
	}
	if runtime.GOOS == "windows" {
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			dirs = append(dirs, safeRelativeDirs(localAppData, t.WindowsLocalAppDataBinDirs)...)
		}
	}
	return dedupeStrings(dirs)
}

func safeRelativeDirs(base string, relativeDirs []string) []string {
	var dirs []string
	for _, rel := range relativeDirs {
		if rel == "" || filepath.IsAbs(rel) {
			continue
		}
		dir := filepath.Join(base, rel)
		if !isSubpath(base, dir) {
			continue
		}
		dirs = append(dirs, dir)
	}
	return dirs
}

// isSubpath reports whether dir lies strictly inside base. Both are expected to be Clean absolute paths (filepath.Join guarantees that for dir). A dir equal to base is rejected: an entry resolving to the home dir itself would scan $HOME for executables, which no installer layout warrants.
func isSubpath(base, dir string) bool {
	rel, err := filepath.Rel(base, dir)
	if err != nil {
		return false
	}
	if rel == "." || rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// npmEnvBinDirs lists candidate global-bin directories derivable from the environment ALONE (no subprocess). Node version managers export their active install's bin dir, and everyapi inherits those even when the shell rc that would add them to $PATH wasn't sourced (login vs. non-login shells, GUI-launched terminals, etc.) — which is exactly the "installed but not on PATH" situation this rescues.
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
	// Explicit npm prefix override — all three spellings npm honors. Bare `PREFIX` included deliberately: @npmcli/config's loadGlobalPrefix (npm 7+) checks `this.env.PREFIX` FIRST when deriving the global prefix, so on Termux-style / hand-rolled setups global installs genuinely land in $PREFIX/bin and skipping it would push those users back into the install loop.
	if p := firstNonEmptyEnv("npm_config_prefix", "NPM_CONFIG_PREFIX", "PREFIX"); p != "" {
		add(binSubdir(p))
	}
	// Common hand-configured global prefixes under $HOME.
	if home, err := os.UserHomeDir(); err == nil {
		add(binSubdir(filepath.Join(home, ".npm-global")))
		add(binSubdir(filepath.Join(home, ".npm-packages")))
		add(filepath.Join(home, ".bun", "bin"))
		add(filepath.Join(home, ".local", "share", "pnpm"))
		add(filepath.Join(home, "Library", "pnpm"))
		add(filepath.Join(home, ".volta", "bin"))
		add(filepath.Join(home, ".nodenv", "shims"))
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

// npmPrefixBinDir resolves npm's global bin directory by asking npm itself (`npm prefix -g`). Returns "" when npm isn't a resolvable binary on $PATH — the classic version-manager case where `npm` exists only as a shell function, in which case the env-derived dirs above are the only signal we have. Time-boxed so a wedged npm can't hang launch.
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

// binSubdir maps an npm PREFIX to the directory its executables live in: <prefix>/bin on Unix, and the prefix itself on Windows (npm writes its .cmd/.ps1 shims directly into the prefix there, not a bin subdir).
func binSubdir(prefix string) string {
	if prefix == "" {
		return ""
	}
	if runtime.GOOS == "windows" {
		return prefix
	}
	return filepath.Join(prefix, "bin")
}

// findExecutable looks for an executable named `name` in `dir`, returning its absolute path and true on the first hit.
//
// On Unix it requires a non-directory file with an execute bit set. On Windows it tries each PATHEXT extension (npm writes a `name.cmd` shim), since a bare extensionless name there is typically the git-bash shell script the OS can't CreateProcess.
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

// windowsExecExts returns the filename extensions to try for `name` on Windows: the standard PATHEXT list (npm's shims are `.cmd`/`.exe`), plus a bare "" only when `name` already carries its own extension.
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

// firstNonEmptyEnv returns the value of the first set, non-empty env var among names, or "".
func firstNonEmptyEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

// dedupeStrings returns s with duplicates removed, preserving first-seen order.
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
