package cmd

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/everyapi-ai/everyapi-ai/cmd/proxy"
	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

// uninstallPlan is the resolved set of paths Uninstall touches. We
// build it before any deletion so the confirmation block can render
// the exact list AND so --dry-run reuses the same code path as the
// real run (everything except the final delete loop).
type uninstallPlan struct {
	configDir    string // ~/.config/everyapi (creds, settings, sanitizer.*, edge active pointer, update_check)
	dataDir      string // ~/.local/share/everyapi (edge per-node state)
	binaryPath   string // resolved os.Executable()
	method       installMethod
	proxyRunning bool

	keepConfig bool
	keepData   bool
	keepBinary bool
}

// binaryRemover is the hook the production path uses to unlink the
// running binary. Tests override it to avoid deleting the test
// executable when exercising the full plan with --keep-binary=false.
var binaryRemover = os.Remove

// Uninstall removes everyapi state from the machine. By default it
// stops any running sanitizer proxy, deletes ~/.config/everyapi/
// (credentials + settings + sanitizer + update-check + edge active
// pointer), deletes ~/.local/share/everyapi/ (edge per-node data),
// and removes the binary itself (when the install method is one we
// can safely undo — brew is delegated to `brew uninstall`).
//
// --dry-run lists what would happen without touching anything.
// --keep-config / --keep-data / --keep-binary scope down the wipe.
// --yes / -y skips the confirmation prompt.
func Uninstall(args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "list what would be removed without touching anything")
	yes := fs.Bool("yes", false, "skip the confirmation prompt")
	// `-y` as a bool flag separate from `--yes` (rather than via aliases)
	// because flag.FlagSet has no native alias mechanism. Both write to
	// the same *bool via fs.BoolVar so either form sets the flag.
	fs.BoolVar(yes, "y", false, "alias for --yes")
	keepConfig := fs.Bool("keep-config", false, "do not remove ~/.config/everyapi")
	keepData := fs.Bool("keep-data", false, "do not remove ~/.local/share/everyapi (edge per-node data; future state under the everyapi namespace)")
	keepBinary := fs.Bool("keep-binary", false, "do not remove the binary itself")
	if err := fs.Parse(args); err != nil {
		return err
	}

	plan, err := buildUninstallPlan(*keepConfig, *keepData, *keepBinary)
	if err != nil {
		return err
	}
	renderUninstallPlan(plan)

	if *dryRun {
		cliout.Println(i18n.T("uninstall.dry_run_footer"))
		return nil
	}

	if !*yes {
		ok, err := cliprompt.YesNo(bufio.NewReader(os.Stdin),
			i18n.T("uninstall.confirm"), false)
		if err != nil {
			return err
		}
		if !ok {
			cliout.Println(i18n.T("uninstall.aborted"))
			return nil
		}
	}

	return executeUninstallPlan(plan)
}

func buildUninstallPlan(keepConfig, keepData, keepBinary bool) (uninstallPlan, error) {
	p := uninstallPlan{
		keepConfig: keepConfig,
		keepData:   keepData,
		keepBinary: keepBinary,
	}

	dir, err := config.ConfigDir()
	if err != nil {
		return p, fmt.Errorf("resolve config dir: %w", err)
	}
	p.configDir = dir

	dataDir, err := uninstallDataDir()
	if err != nil {
		return p, fmt.Errorf("resolve data dir: %w", err)
	}
	p.dataDir = dataDir

	if exe, err := os.Executable(); err == nil {
		if resolved, lerr := filepath.EvalSymlinks(exe); lerr == nil {
			p.binaryPath = resolved
		} else {
			p.binaryPath = exe
		}
	}
	p.method = detectInstallMethod()

	// Real liveness check (pid file exists AND the named process is
	// alive). A bare pid-file stat would falsely flag a crashed-proxy
	// stale pid as "running" — the confirmation text then promises a
	// step that won't actually happen. `proxy.Run("stop")` is still
	// idempotent on a stale pid, so the discrepancy is cosmetic, but
	// "the plan said it, we didn't do it" is a trust bug regardless.
	p.proxyRunning = proxy.IsRunning()

	return p, nil
}

// uninstallDataDir mirrors edge.dataRoot()'s parent: ~/.local/share/everyapi
// (or $XDG_DATA_HOME/everyapi). Removing the parent dir — rather than
// just .../edge — keeps the wipe correct if future features add more
// subdirs under the everyapi namespace.
//
// Kept here (and not as an exported helper in sdk/config) so the
// "remove this whole tree" decision lives next to the rest of the
// uninstall plan. Re-deriving the path costs three lines and avoids
// reaching into the edge package for a private helper.
func uninstallDataDir() (string, error) {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "everyapi"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "everyapi"), nil
}

// pathExists is a non-erroring stat: returns true iff the path
// resolves to an entry we can see. Used in the plan renderer to
// label each line "(already gone)" when nothing's there. We don't
// distinguish file vs dir here — RemoveAll handles both, and the
// "what is it" detail isn't useful to the user.
func pathExists(p string) bool {
	if p == "" {
		return false
	}
	_, err := os.Lstat(p)
	return err == nil
}

func renderUninstallPlan(p uninstallPlan) {
	cliout.Println(i18n.T("uninstall.plan_header"))

	if p.proxyRunning {
		cliout.Printf("  %s\n", i18n.T("uninstall.plan_proxy_running"))
	}

	configLine := p.configDir
	if !pathExists(p.configDir) {
		configLine = fmt.Sprintf("%s  (%s)", p.configDir, i18n.T("uninstall.plan_already_gone"))
	}
	switch {
	case p.keepConfig:
		cliout.Printf("  %s %s  (%s)\n", i18n.T("uninstall.plan_keep_marker"), configLine, i18n.T("uninstall.plan_keep_reason_flag"))
	default:
		cliout.Printf("  %s %s\n", i18n.T("uninstall.plan_remove_marker"), configLine)
	}

	dataLine := p.dataDir
	if !pathExists(p.dataDir) {
		dataLine = fmt.Sprintf("%s  (%s)", p.dataDir, i18n.T("uninstall.plan_already_gone"))
	}
	switch {
	case p.keepData:
		cliout.Printf("  %s %s  (%s)\n", i18n.T("uninstall.plan_keep_marker"), dataLine, i18n.T("uninstall.plan_keep_reason_flag"))
	default:
		cliout.Printf("  %s %s\n", i18n.T("uninstall.plan_remove_marker"), dataLine)
	}

	binaryLine := p.binaryPath
	if p.binaryPath == "" {
		binaryLine = i18n.T("uninstall.plan_binary_unknown")
	} else if !pathExists(p.binaryPath) {
		binaryLine = fmt.Sprintf("%s  (%s)", p.binaryPath, i18n.T("uninstall.plan_already_gone"))
	}
	switch {
	case p.keepBinary:
		cliout.Printf("  %s %s  (%s)\n", i18n.T("uninstall.plan_keep_marker"), binaryLine, i18n.T("uninstall.plan_keep_reason_flag"))
	case p.method == installMethodBrew:
		// brew tracks the install in its own state (the Cellar entry +
		// the formula's caveats). Removing the symlink with os.Remove
		// leaves brew thinking everyapi is still installed and would
		// silently fail to surface the binary on the next `brew
		// upgrade`. Print the explicit hint and skip touching it.
		cliout.Printf("  %s %s  (%s)\n", i18n.T("uninstall.plan_keep_marker"), binaryLine, i18n.T("uninstall.plan_keep_reason_brew"))
	default:
		cliout.Printf("  %s %s\n", i18n.T("uninstall.plan_remove_marker"), binaryLine)
	}
	cliout.Println("")
}

func executeUninstallPlan(p uninstallPlan) error {
	// Stop a running proxy first. `proxy.Run([]string{"stop"})`
	// already handles "not running" and "process gone but stale pid"
	// internally and is safe to call when our liveness probe above
	// gave a false positive. Print its output as-is — it's an
	// uninstall step, the user should see what's happening.
	if p.proxyRunning {
		if err := proxy.Run([]string{"stop"}); err != nil {
			cliout.Printf("  %s: %s\n", i18n.T("uninstall.proxy_stop_failed"), err)
			// Don't abort — the config dir wipe below will remove the
			// stale pid file regardless, and the user asked to
			// uninstall, not to babysit a stuck process.
		}
	}

	if !p.keepConfig {
		existed := pathExists(p.configDir)
		if err := removeTree(p.configDir); err != nil {
			return fmt.Errorf("remove %s: %w", p.configDir, err)
		}
		if existed {
			cliout.Printf("  %s %s\n", i18n.T("uninstall.removed_marker"), p.configDir)
		}
	}

	if !p.keepData {
		existed := pathExists(p.dataDir)
		if err := removeTree(p.dataDir); err != nil {
			return fmt.Errorf("remove %s: %w", p.dataDir, err)
		}
		if existed {
			cliout.Printf("  %s %s\n", i18n.T("uninstall.removed_marker"), p.dataDir)
		}
	}

	// Binary removal comes LAST. If anything above errors out the
	// binary is still on disk, so the user can re-run uninstall to
	// retry. Order matters: a half-wiped install where the binary
	// is gone but state remains is harder to recover from than the
	// reverse.
	switch {
	case p.keepBinary:
		// nothing
	case p.method == installMethodBrew:
		cliout.Printf("\n%s\n", i18n.T("uninstall.hint_brew_header"))
		cliout.Printf("  brew uninstall everyapi\n")
		cliout.Printf("  brew untap everyapi-ai/tap   # %s\n", i18n.T("uninstall.hint_brew_untap_note"))
	case p.binaryPath == "":
		cliout.Printf("\n%s\n", i18n.T("uninstall.hint_binary_unknown"))
	default:
		existed := pathExists(p.binaryPath)
		if err := binaryRemover(p.binaryPath); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				// Binary removal is the LAST step and nothing depends on it.
				// A failure here — Windows can't unlink a running image, or a
				// read-only mount / root-owned path on Unix — must not abort
				// after config + data are already gone. Downgrade to a warning
				// + manual-delete hint and fall through to the done message.
				cliout.Printf("  %s\n", fmt.Sprintf(i18n.T("uninstall.binary_remove_failed"), err))
				cliout.Printf("  %s\n", fmt.Sprintf(i18n.T("uninstall.hint_binary_manual"), p.binaryPath))
			}
		} else if existed {
			cliout.Printf("  %s %s\n", i18n.T("uninstall.removed_marker"), p.binaryPath)
		}
		// install.ps1's upgrade-over-a-running-binary dance parks the
		// previous binary at <exe>.old for the NEXT install to clean up.
		// Uninstall is that "next" too: without this, a full working
		// prior-version executable outlives the uninstall on a dir
		// that's still on the user's PATH. Best-effort — a still-running
		// .old can't be unlinked on Windows, same policy as above.
		_ = binaryRemover(p.binaryPath + ".old")
	}

	cliout.Printf("\n%s\n", i18n.T("uninstall.done"))
	return nil
}

// removeTree is os.RemoveAll with a "missing parent is fine" guard.
// We use it in two places (configDir, dataDir) so factor it once;
// the bare-os.RemoveAll call would also treat ENOENT as success but
// the explicit pre-stat keeps the success path obvious in a code
// review of an uninstaller, where "delete by path" deserves the
// extra scrutiny.
func removeTree(path string) error {
	if path == "" {
		return nil
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return os.RemoveAll(path)
}
