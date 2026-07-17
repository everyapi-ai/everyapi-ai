//go:build !windows

package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-sdk/config"
)

const (
	useClaudeRecoveryHelperEnv = "EVERYAPI_TEST_USE_CLAUDE_RECOVERY_HELPER"
	useClaudeRecoveryShimEnv   = "EVERYAPI_TEST_USE_CLAUDE_RECOVERY_SHIM"
)

// TestUseExecReceivesRecoveredClaudeSessionID covers the INJECTED path
// (--transparent=false): the tool is handed the sanitizer's loopback base URL
// directly. Transparent mode is the default now, so the flag is explicit here;
// the transparent path's equivalent guard coverage lives in
// TestTransparentConnectorChainsThroughRecoveryGuard.
func TestUseExecReceivesRecoveredClaudeSessionID(t *testing.T) {
	if os.Getenv(useClaudeRecoveryShimEnv) == "1" {
		runClaudeRecoveryShim(t)
		return
	}
	if os.Getenv(useClaudeRecoveryHelperEnv) == "1" {
		oldID := os.Getenv("EVERYAPI_TEST_USE_OLD_SESSION_ID")
		if err := Use([]string{"claude", "--transparent=false", "--", "--resume", oldID}); err != nil {
			t.Fatal(err)
		}
		t.Fatal("Use returned after a successful tool launch")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/messages" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"course\\ncourse\\ncourse\"}}\n\n"))
			_, _ = w.Write([]byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\n"))
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data":   []any{},
		})
	}))
	defer srv.Close()

	configRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	if err := config.Save(&config.Credentials{
		APIBase:  srv.URL,
		RelayKey: "sk-everyapi-test",
	}); err != nil {
		t.Fatal(err)
	}

	claudeDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	oldID := "7f6de1d2-3c4b-4a59-8e60-123456789abc"
	oldPath := writeResumableClaudeSession(t, claudeDir, oldID, true)

	shimDir := t.TempDir()
	argvPath := filepath.Join(t.TempDir(), "argv")
	basePath := filepath.Join(t.TempDir(), "base")
	statusPath := filepath.Join(t.TempDir(), "status")
	bodyPath := filepath.Join(t.TempDir(), "body")
	shimPath := filepath.Join(shimDir, "claude")
	shim := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > \"$EVERYAPI_TEST_USE_ARGV_FILE\"\n" +
		useClaudeRecoveryShimEnv + "=1 exec \"$EVERYAPI_TEST_USE_TEST_BINARY\" -test.run=^TestUseExecReceivesRecoveredClaudeSessionID$\n"
	if err := os.WriteFile(shimPath, []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("EVERYAPI_TEST_USE_ARGV_FILE", argvPath)
	t.Setenv("EVERYAPI_TEST_USE_BASE_FILE", basePath)
	t.Setenv("EVERYAPI_TEST_USE_STATUS_FILE", statusPath)
	t.Setenv("EVERYAPI_TEST_USE_BODY_FILE", bodyPath)
	t.Setenv("EVERYAPI_TEST_USE_TEST_BINARY", os.Args[0])
	t.Setenv("EVERYAPI_TEST_USE_OLD_SESSION_ID", oldID)

	child := exec.Command(os.Args[0], "-test.run=^TestUseExecReceivesRecoveredClaudeSessionID$")
	child.Env = append(os.Environ(), useClaudeRecoveryHelperEnv+"=1")
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("helper process failed: %v\n%s", err, output)
	}

	argv, err := os.ReadFile(argvPath)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Fields(string(argv))
	if len(got) != 2 || got[0] != "--resume" {
		t.Fatalf("exec argv = %#v, want --resume with a fresh session ID", got)
	}
	if got[1] == oldID {
		t.Fatalf("exec resume ID = %q, want the recovered session ID", got[1])
	}
	if !claudeSessionIDPattern.MatchString(got[1]) {
		t.Fatalf("exec resume ID = %q, want UUID", got[1])
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(oldPath), got[1]+".jsonl")); err != nil {
		t.Fatalf("recovered transcript for exec ID: %v", err)
	}

	baseBytes, err := os.ReadFile(basePath)
	if err != nil {
		t.Fatal(err)
	}
	if base := strings.TrimSpace(string(baseBytes)); base == srv.URL || !strings.HasPrefix(base, "http://127.0.0.1:") {
		t.Fatalf("ANTHROPIC_BASE_URL = %q, want incremental recovery guard", base)
	}
	status, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(status)); got != "200" {
		body, _ := os.ReadFile(bodyPath)
		t.Fatalf("direct response status = %q, want 200; body=%s", got, body)
	}
	body, err := os.ReadFile(bodyPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "course") {
		t.Fatalf("polluted assistant text reached launched Claude process: %s", body)
	}
}

func runClaudeRecoveryShim(t *testing.T) {
	t.Helper()
	base := os.Getenv("ANTHROPIC_BASE_URL")
	if err := os.WriteFile(os.Getenv("EVERYAPI_TEST_USE_BASE_FILE"), []byte(base+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, base+"/v1/messages", strings.NewReader(`{"model":"claude-opus-4-8","stream":true,"messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv("EVERYAPI_TEST_USE_BODY_FILE"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv("EVERYAPI_TEST_USE_STATUS_FILE"), []byte(strconv.Itoa(resp.StatusCode)), 0o600); err != nil {
		t.Fatal(err)
	}
}
