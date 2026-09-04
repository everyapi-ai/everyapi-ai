package cmd

import (
	"errors"
	"strings"
	"testing"
)

func TestInstallToolRequiresOneAllowlistedToolAndOnlyAcceptsTheForceFlag(t *testing.T) {
	for _, args := range [][]string{nil, {}, {"grok", "--then-launch"}, {"grok", "--force", "extra"}} {
		if err := InstallTool(args); err == nil {
			t.Fatalf("InstallTool(%q) = nil, want argument error", args)
		}
	}
	if err := InstallTool([]string{"../../bin/sh", "--force"}); err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("InstallTool(forced unknown) = %v, want allowlist error", err)
	}
	if err := InstallTool([]string{"../../bin/sh"}); err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("InstallTool(unknown) = %v, want allowlist error", err)
	}
}

func TestInstallToolForceUsesClaudeNativeUpdater(t *testing.T) {
	originalCommand := claudeDesktopUpdateFn
	originalMirror := claudeMirrorUpdateFn
	t.Cleanup(func() {
		claudeDesktopUpdateFn = originalCommand
		claudeMirrorUpdateFn = originalMirror
	})

	commandCalls := 0
	mirrorCalls := 0
	claudeDesktopUpdateFn = func() error {
		commandCalls++
		return nil
	}
	claudeMirrorUpdateFn = func() error {
		mirrorCalls++
		return errors.New("mirror should not run")
	}

	if err := InstallTool([]string{"claude", "--force"}); err != nil {
		t.Fatal(err)
	}
	if commandCalls != 1 || mirrorCalls != 0 {
		t.Fatalf("update calls = native %d, mirror %d; want 1, 0", commandCalls, mirrorCalls)
	}
}
