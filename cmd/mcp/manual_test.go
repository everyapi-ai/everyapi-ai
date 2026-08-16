package mcp

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestManualInstallTextIncludesSnippetAndPath covers the whole value of a ManualOnly target: the user gets the exact block to paste and the exact file to paste it into. Nothing else verifies that output — there is no shell-out whose exit code would catch a mistake.
func TestManualInstallTextIncludesSnippetAndPath(t *testing.T) {
	c, err := lookupClient("librefang")
	if err != nil {
		t.Fatal(err)
	}
	got := manualInstallText(c)

	if !strings.Contains(got, c.ManualSnippet) {
		t.Errorf("manual install text omits the snippet:\n%s", got)
	}
	if !strings.Contains(got, c.ConfigPath) {
		t.Errorf("manual install text omits the config path %q:\n%s", c.ConfigPath, got)
	}
	// The per-agent allowlist is the step users miss: a daemon-wide server is invisible to an agent whose mcp_servers doesn't name it, and an empty list means none rather than all.
	if !strings.Contains(got, "mcp_servers = [\"everyapi\"]") {
		t.Errorf("manual install text omits the per-agent allowlist step:\n%s", got)
	}
	// Money-moving tools should not be wired up without at least telling the user that an approval gate exists.
	if !strings.Contains(got, "require_approval") {
		t.Errorf("manual install text omits the approval-gating hint:\n%s", got)
	}
}

// TestManualInstallTextFollowsConfigPathOverride guards against the text hard-coding ~/.librefang while clientConfigPath resolves somewhere else — that would send the paste to a file the daemon never reads.
func TestManualInstallTextFollowsConfigPathOverride(t *testing.T) {
	home := filepath.Join(t.TempDir(), "managed-librefang")
	t.Setenv("LIBREFANG_HOME", home)
	c, _ := lookupClient("librefang")
	want := filepath.Join(home, "config.toml")
	if got := manualInstallText(c); !strings.Contains(got, want) {
		t.Errorf("manual install text does not point at %q:\n%s", want, got)
	}
	if got := manualUninstallText(c); !strings.Contains(got, want) {
		t.Errorf("manual uninstall text does not point at %q:\n%s", want, got)
	}
}

// TestManualUninstallTextNamesWhatToDelete keeps uninstall symmetric with install: it must name the block, the allowlist, and the restart.
func TestManualUninstallTextNamesWhatToDelete(t *testing.T) {
	c, _ := lookupClient("librefang")
	got := manualUninstallText(c)
	for _, want := range []string{"[[mcp_servers]]", "everyapi", "mcp_servers", "restart"} {
		if !strings.Contains(strings.ToLower(got), strings.ToLower(want)) {
			t.Errorf("manual uninstall text omits %q:\n%s", want, got)
		}
	}
	// Unlike install, a working remove verb exists — `librefang mcp remove everyapi` matches a hand-pasted entry by name and hot-reloads the daemon. Sending users to hand-edit TOML without mentioning it would be worse advice, so the hint is part of the contract.
	if !strings.Contains(got, "librefang mcp remove everyapi") {
		t.Errorf("manual uninstall text omits the `mcp remove` shortcut:\n%s", got)
	}
	// ...but it round-trips config.toml and drops comments, which is the very harm install refuses to inflict. Offering it without that caveat would contradict the reason this target is ManualOnly.
	if !strings.Contains(strings.ToLower(got), "comment") {
		t.Errorf("manual uninstall text offers `mcp remove` without the comment-loss caveat:\n%s", got)
	}
}

// TestManualOnlyInstallUninstallSucceedWithoutBinary is the regression this design exists for. Install/Uninstall must return nil for a ManualOnly target even when the binary is absent — printing the config IS the outcome, and both would panic on a nil AddArgv/RemoveArgv if the guard were dropped or moved after the PATH check.
func TestManualOnlyInstallUninstallSucceedWithoutBinary(t *testing.T) {
	// An empty PATH guarantees LookupExecName misses, simulating a user who has not installed LibreFang yet. They still deserve the instructions.
	t.Setenv("PATH", t.TempDir())

	if err := Install([]string{"librefang"}); err != nil {
		t.Errorf("Install(librefang) = %v, want nil", err)
	}
	if err := Uninstall([]string{"librefang"}); err != nil {
		t.Errorf("Uninstall(librefang) = %v, want nil", err)
	}
}
