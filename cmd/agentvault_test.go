package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentSessionsJSONProjectsCodexJSONLWithoutAssistantBody(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, ".codex", "sessions", "2026", "09", "01", "rollout-abc.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"codex-123","cwd":"/workspace/app"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Fix the browser timeout overlay"}]}}`,
		`{"type":"response_item","payload":{"role":"assistant","content":"secret response body"}}`,
	}, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}

	scan, err := scanAgentSessionsAt([]agentProviderRoot{{provider: "codex", path: filepath.Join(home, ".codex", "sessions")}}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(scan.Sessions))
	}
	session := scan.Sessions[0]
	if session.SessionID != "codex-123" || session.Title != "Fix the browser timeout overlay" {
		t.Fatalf("session projection = %#v", session)
	}
	if session.CWD == nil || *session.CWD != "/workspace/app" {
		t.Fatalf("cwd = %#v", session.CWD)
	}
	if session.FirstPrompt == nil || *session.FirstPrompt != "Fix the browser timeout overlay" {
		t.Fatalf("first prompt = %#v", session.FirstPrompt)
	}
	encoded, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "secret response body") {
		t.Fatal("assistant body leaked into JSON output")
	}
}

func TestAgentSessionsCapsResultsAndSkipsSymlink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := filepath.Join(home, ".claude", "projects")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		path := filepath.Join(root, "session-"+string(rune('0'+i))+".jsonl")
		if err := os.WriteFile(path, []byte(`{"sessionId":"claude-`+string(rune('0'+i))+`","cwd":"/workspace/app","type":"user","message":{"role":"user","content":"Plan project"}}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "private.jsonl")
	if err := os.WriteFile(outside, []byte(`{"type":"user","message":"private"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked.jsonl")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	scan, err := scanAgentSessionsAt([]agentProviderRoot{{provider: "claude", path: root}}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Sessions) != 2 || scan.ScannedFiles != 3 || !scan.Truncated {
		t.Fatalf("scan = %#v", scan)
	}
	if len(scan.Providers) != 1 || scan.Providers[0] != "claude" {
		t.Fatalf("providers = %#v", scan.Providers)
	}
}
