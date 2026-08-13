package cmd

import (
	"strings"
	"testing"
)

func TestInstallToolRequiresExactlyOneAllowlistedTool(t *testing.T) {
	for _, args := range [][]string{nil, {}, {"grok", "--then-launch"}} {
		if err := InstallTool(args); err == nil {
			t.Fatalf("InstallTool(%q) = nil, want argument error", args)
		}
	}
	if err := InstallTool([]string{"../../bin/sh"}); err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("InstallTool(unknown) = %v, want allowlist error", err)
	}
}
