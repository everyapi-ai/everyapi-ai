//go:build darwin

package computeruse

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func writeProtocolProbe(t *testing.T, output string, exitCode int) string {
	t.Helper()
	app := filepath.Join(t.TempDir(), darwinHelperAppName)
	executable := filepath.Join(app, "Contents", "MacOS", darwinHelperExecutable)
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nprintf '%s\\n' '" + output + "'\nexit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(executable, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return app
}

func TestHelperSupportsCurrentProtocol(t *testing.T) {
	ctx := context.Background()
	if !helperSupportsProtocol(ctx, writeProtocolProbe(t, "2", 0)) {
		t.Fatal("current helper protocol was rejected")
	}
	if helperSupportsProtocol(ctx, writeProtocolProbe(t, "1", 0)) {
		t.Fatal("old helper protocol was accepted")
	}
	if helperSupportsProtocol(ctx, writeProtocolProbe(t, "2", 2)) {
		t.Fatal("failing helper probe was accepted")
	}
}
