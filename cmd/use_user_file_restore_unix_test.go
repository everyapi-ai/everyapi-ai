//go:build !windows

package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-sdk/config"
)

const useUserFileRestoreHelperEnv = "EVERYAPI_TEST_USE_USER_FILE_RESTORE_HELPER"

// relayCatalogServer serves the one endpoint a launch needs before it execs: the model catalogue the picker and the family overrides are built from.
func relayCatalogServer(t *testing.T, id, endpoint string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data":   []any{map[string]any{"id": id, "owned_by": "anthropic", "supported_endpoint_types": []string{endpoint}}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// runUseHelper re-enters this test binary as the process that calls Use, because a successful launch ends in os.Exit and cannot return into a test. The caller inspects what the launch left on disk after the helper is reaped.
func runUseHelper(t *testing.T, testName string) {
	t.Helper()
	child := exec.Command(os.Args[0], "-test.run=^"+testName+"$")
	child.Env = append(os.Environ(), useUserFileRestoreHelperEnv+"=1")
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("helper process failed: %v\n%s", err, output)
	}
}

// TestUseRestoresClaudeUserModelAfterLaunch is the end-to-end guard for the leak: `/model` inside a gateway launch saves its pick as the user's default for every ordinary session afterwards, where a gateway-only id resolves to nothing. The shim stands in for that write.
//
// It has to run the launch for real. The restore travels in the cleanup chain ExecWithOptions runs after the child exits, and the bug it replaced was a cleanup that existed but was only ever deferred — in a function that ends in os.Exit, which skips defers. Nothing short of a real launch tells those two apart.
func TestUseRestoresClaudeUserModelAfterLaunch(t *testing.T) {
	if os.Getenv(useUserFileRestoreHelperEnv) == "1" {
		if err := Use([]string{"claude", "--transparent=false"}); err != nil {
			t.Fatal(err)
		}
		t.Fatal("Use returned after a successful tool launch")
	}

	srv := relayCatalogServer(t, "claude-opus-5", "anthropic")
	configRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	if err := config.Save(&config.Credentials{APIBase: srv.URL, RelayKey: "sk-everyapi-test"}); err != nil {
		t.Fatal(err)
	}

	claudeDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	settingsPath := filepath.Join(claudeDir, "settings.json")
	before := "{\n  \"model\": \"claude-fable-5[1m]\",\n  \"theme\": \"auto\"\n}\n"
	if err := os.WriteFile(settingsPath, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	shimDir := t.TempDir()
	shim := "#!/bin/sh\n" +
		"cat > \"$CLAUDE_CONFIG_DIR/settings.json\" <<'JSON'\n" +
		"{\n  \"model\": \"qwen2.5:7b\",\n  \"theme\": \"dark\"\n}\n" +
		"JSON\n"
	if err := os.WriteFile(filepath.Join(shimDir, "claude"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	runUseHelper(t, "TestUseRestoresClaudeUserModelAfterLaunch")

	after, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "\"model\": \"claude-fable-5[1m]\"") {
		t.Fatalf("the launch left its own model as the user's default: %s", after)
	}
	if !strings.Contains(string(after), "\"theme\": \"dark\"") {
		t.Fatalf("an unrelated setting the session changed was reverted: %s", after)
	}
}

// TestUseRemovesGooseManagedBlockAfterLaunch covers the same wiring for the other user-owned file a launch patches. The block is written into ~/.config/goose/.goosehints at launch and documented as removed at exit; before this it was removed only on the paths that returned BEFORE the launch, so every successful one left it behind.
func TestUseRemovesGooseManagedBlockAfterLaunch(t *testing.T) {
	if os.Getenv(useUserFileRestoreHelperEnv) == "1" {
		if err := Use([]string{"goose", "--transparent=false"}); err != nil {
			t.Fatal(err)
		}
		t.Fatal("Use returned after a successful tool launch")
	}

	srv := relayCatalogServer(t, "gpt-5.6-sol", "openai")
	configRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	if err := config.Save(&config.Credentials{APIBase: srv.URL, RelayKey: "sk-everyapi-test"}); err != nil {
		t.Fatal(err)
	}

	gooseDir := filepath.Join(configRoot, "goose")
	if err := os.MkdirAll(gooseDir, 0o700); err != nil {
		t.Fatal(err)
	}
	hintsPath := filepath.Join(gooseDir, ".goosehints")
	if err := os.WriteFile(hintsPath, []byte("Always run the linter.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	shimDir := t.TempDir()
	markerPath := filepath.Join(t.TempDir(), "marker")
	shim := "#!/bin/sh\nprintf '%s' \"$__EVERYAPI_MANAGED_BLOCKS\" > \"$EVERYAPI_TEST_MARKER_FILE\"\n"
	if err := os.WriteFile(filepath.Join(shimDir, "goose"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("EVERYAPI_TEST_MARKER_FILE", markerPath)

	runUseHelper(t, "TestUseRemovesGooseManagedBlockAfterLaunch")

	// The marker is EveryAPI's own bookkeeping; taking it out of the map is what keeps it from reaching the tool as an environment variable.
	marker, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(marker) != 0 {
		t.Fatalf("internal managed-block marker reached the launched tool: %s", marker)
	}

	hints, err := os.ReadFile(hintsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(hints), "everyapi:begin") {
		t.Fatalf("the launch left its managed block in the user's hints file: %s", hints)
	}
	if !strings.Contains(string(hints), "Always run the linter.") {
		t.Fatalf("the user's own hints were lost: %s", hints)
	}
}
