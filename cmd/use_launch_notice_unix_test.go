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

const useLaunchNoticeCallerEnv = "EVERYAPI_TEST_USE_LAUNCH_NOTICE_CALLER"

func TestUseLaunchNoticeNamesClineNotCLite(t *testing.T) {
	if os.Getenv(useLaunchNoticeCallerEnv) == "1" {
		if err := Use([]string{"cline", "--transparent=false", "--model", "chat-model"}); err != nil {
			t.Fatal(err)
		}
		return
	}

	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []any{map[string]any{
				"id":                       "chat-model",
				"supported_endpoint_types": []string{"openai"},
			}},
		})
	}))
	defer gateway.Close()

	configRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	if err := config.Save(&config.Credentials{
		APIBase:  gateway.URL,
		RelayKey: "sk-everyapi-test",
	}); err != nil {
		t.Fatal(err)
	}

	shimDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(shimDir, "clite"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	child := exec.Command(os.Args[0], "-test.run=^TestUseLaunchNoticeNamesClineNotCLite$")
	child.Env = append(os.Environ(),
		useLaunchNoticeCallerEnv+"=1",
		"XDG_CONFIG_HOME="+configRoot,
		"PATH="+shimDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := child.CombinedOutput()
	if err != nil {
		t.Fatalf("use cline failed: %v\n%s", err, out)
	}
	text := string(out)
	if !strings.Contains(text, "Launching cline against ") {
		t.Fatalf("launch notice = %q, want the Cline command name", text)
	}
	if strings.Contains(text, "Launching clite against ") {
		t.Fatalf("launch notice exposed the executable name: %q", text)
	}
}
