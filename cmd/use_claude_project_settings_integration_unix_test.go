//go:build !windows

package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-sdk/config"
)

const useClaudeProjectHelperEnv = "EVERYAPI_TEST_USE_CLAUDE_PROJECT_HELPER"

// TestUseRespectsClaudeProjectSettings runs the real launch path — catalogue fetch, boot-model resolution, dangerous-mode preference, exec — against a shim standing in for Claude Code, and reads back the argv the shim was handed.
//
// Both globally remembered preferences are deliberately set: a remembered boot model and dangerous mode ON. The no-settings case proves they do reach argv, which is what keeps the two project cases from passing vacuously — a check that a flag is absent is worth nothing unless something would otherwise put it there.
func TestUseRespectsClaudeProjectSettings(t *testing.T) {
	if os.Getenv(useClaudeProjectHelperEnv) == "1" {
		if err := Use([]string{"claude", "--transparent=false"}); err != nil {
			t.Fatal(err)
		}
		t.Fatal("Use returned after a successful tool launch")
	}

	for _, tc := range []struct {
		name             string
		projectSettings  string
		wantModel        bool
		wantSkipPermsAdd bool
	}{
		{
			name:             "no project settings keeps both global preferences",
			projectSettings:  "",
			wantModel:        true,
			wantSkipPermsAdd: true,
		},
		{
			name:             "env-selected model keeps the boot model project-owned",
			projectSettings:  `{"env":{"ANTHROPIC_MODEL":"project-model"}}`,
			wantModel:        false,
			wantSkipPermsAdd: true,
		},
		{
			name:             "permissions policy keeps the permission mode project-owned",
			projectSettings:  `{"permissions":{"deny":["Bash(rm:*)"]}}`,
			wantModel:        true,
			wantSkipPermsAdd: false,
		},
		{
			name:             "a project can own both at once",
			projectSettings:  `{"model":"project-model","permissions":{"defaultMode":"plan"}}`,
			wantModel:        false,
			wantSkipPermsAdd: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			argv := runClaudeProjectSettingsLaunch(t, tc.projectSettings)
			if got := slices.Contains(argv, "--model"); got != tc.wantModel {
				t.Errorf("--model present = %v, want %v; argv=%#v", got, tc.wantModel, argv)
			}
			if got := slices.Contains(argv, "--dangerously-skip-permissions"); got != tc.wantSkipPermsAdd {
				t.Errorf("--dangerously-skip-permissions present = %v, want %v; argv=%#v", got, tc.wantSkipPermsAdd, argv)
			}
		})
	}
}

// runClaudeProjectSettingsLaunch performs one launch from a working directory carrying projectSettings (written only when non-empty) and returns the argv the shim received.
func runClaudeProjectSettingsLaunch(t *testing.T, projectSettings string) []string {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data":   []any{map[string]any{"id": "claude-test", "owned_by": "anthropic", "supported_endpoint_types": []string{"anthropic"}}},
		})
	}))
	defer srv.Close()

	configRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	if err := config.Save(&config.Credentials{APIBase: srv.URL, RelayKey: "sk-everyapi-test"}); err != nil {
		t.Fatal(err)
	}
	dangerous := true
	if err := config.SaveSettings(&config.Settings{
		DangerousMode: &dangerous,
		ToolModels:    map[string]string{"claude": "claude-test"},
		TerminalMode:  "native",
	}); err != nil {
		t.Fatal(err)
	}

	projectDir := t.TempDir()
	if projectSettings != "" {
		if err := os.MkdirAll(filepath.Join(projectDir, ".claude"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(projectDir, ".claude", "settings.json"), []byte(projectSettings), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	claudeDir := t.TempDir()
	shimDir := t.TempDir()
	argvPath := filepath.Join(t.TempDir(), "argv")
	// The shim records argv and exits; nothing here needs it to speak the API, and a launch that reached exec has already answered everything this test asks.
	shim := "#!/bin/sh\nprintf '%s\\000' \"$@\" > \"$EVERYAPI_TEST_USE_ARGV_FILE\"\n"
	if err := os.WriteFile(filepath.Join(shimDir, "claude"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}

	child := exec.Command(os.Args[0], "-test.run=^TestUseRespectsClaudeProjectSettings$")
	child.Dir = projectDir
	child.Env = append(os.Environ(),
		useClaudeProjectHelperEnv+"=1",
		"XDG_CONFIG_HOME="+configRoot,
		"CLAUDE_CONFIG_DIR="+claudeDir,
		"PATH="+shimDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"EVERYAPI_TEST_USE_ARGV_FILE="+argvPath,
	)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("helper process failed: %v\n%s", err, output)
	}

	raw, err := os.ReadFile(argvPath)
	if err != nil {
		t.Fatalf("shim recorded no argv: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
}
