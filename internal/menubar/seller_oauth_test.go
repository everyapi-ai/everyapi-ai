package menubar

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/internal/api"
	"github.com/everyapi-ai/everyapi-ai/internal/config"
)

// stubTextPrompt swaps textPrompt for the duration of a test with a
// scripted reply queue. The Nth call returns answers[N]; if more
// calls happen than answers exist the test fails.
func stubTextPrompt(t *testing.T, answers []string) {
	t.Helper()
	prev := textPrompt
	idx := 0
	textPrompt = func(title, body, defaultValue string) (string, bool, error) {
		if idx >= len(answers) {
			t.Fatalf("textPrompt called %d times, only %d answers scripted", idx+1, len(answers))
		}
		ans := answers[idx]
		idx++
		return ans, true, nil
	}
	t.Cleanup(func() { textPrompt = prev })
}

func stubTextPromptCancel(t *testing.T) {
	t.Helper()
	prev := textPrompt
	textPrompt = func(title, body, defaultValue string) (string, bool, error) {
		return "", false, nil
	}
	t.Cleanup(func() { textPrompt = prev })
}

func stubOpenBrowser(t *testing.T) *[]string {
	t.Helper()
	prev := openBrowser
	var captured []string
	openBrowser = func(url string) error {
		captured = append(captured, url)
		return nil
	}
	t.Cleanup(func() { openBrowser = prev })
	return &captured
}

func stubClipboard(t *testing.T, content string, err error) {
	t.Helper()
	prev := readClipboard
	readClipboard = func() (string, error) { return content, err }
	t.Cleanup(func() { readClipboard = prev })
}

// TestPromptChannelMeta_HappyPath drives both modals and asserts
// the trimmed return values.
func TestPromptChannelMeta_HappyPath(t *testing.T) {
	stubTextPrompt(t, []string{"  my-channel  ", "model-a,model-b"})
	name, models, ok, err := promptChannelMeta("Claude")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok {
		t.Fatal("ok=false on happy path")
	}
	if name != "my-channel" {
		t.Errorf("name = %q, want %q", name, "my-channel")
	}
	if models != "model-a,model-b" {
		t.Errorf("models = %q", models)
	}
}

func TestPromptChannelMeta_BlankName(t *testing.T) {
	stubTextPrompt(t, []string{"   "})
	_, _, ok, err := promptChannelMeta("Claude")
	if ok {
		t.Error("ok=true with blank name")
	}
	if err == nil {
		t.Error("expected error for blank name")
	}
}

func TestPromptChannelMeta_UserCancels(t *testing.T) {
	stubTextPromptCancel(t)
	_, _, ok, err := promptChannelMeta("Claude")
	if ok {
		t.Error("ok=true after user cancel")
	}
	if err != nil {
		t.Errorf("err on cancel: %v", err)
	}
}

// TestHandleAddClaude_Flow exercises the start half of the Claude
// flow end-to-end against a fake server. Asserts the controller
// stashes the cookie-jar'd client and surfaces the paste-pending
// menu state.
func TestHandleAddClaude_Flow(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	stubTextPrompt(t, []string{"claude-test", "claude-3-5-sonnet"})
	browser := stubOpenBrowser(t)
	notes := captureNotifier(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/seller/claude/oauth/start", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data":    map[string]interface{}{"authorize_url": "https://console.anthropic.com/oauth/authorize?stub=1"},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	fm := &fakeMenu{}
	c := newForTest(fm)
	c.creds = &config.Credentials{APIBase: srv.URL, AccessToken: "tok", UserID: 1}
	c.state = StateLoggedIn

	c.handleAddClaude()

	if c.claudeClient == nil {
		t.Fatal("claudeClient not stashed after Start")
	}
	if len(*browser) != 1 || !strings.Contains((*browser)[0], "console.anthropic.com") {
		t.Errorf("browser opens = %v", *browser)
	}
	if last, ok := fm.lastOfKind("claude-paste"); !ok || last.args[0] != true {
		t.Errorf("expected claude-paste(true), got %+v", fm.calls())
	}
	if len(*notes) == 0 {
		t.Error("expected at least one notification (progress hint)")
	}
}

// TestHandlePasteClaude_HappyPath completes the Claude flow using a
// stubbed clipboard and asserts state cleanup + success notification.
func TestHandlePasteClaude_HappyPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	stubClipboard(t, "the-code#the-state", nil)
	notes := captureNotifier(t)

	var completeHit atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/seller/claude/oauth/complete", func(w http.ResponseWriter, r *http.Request) {
		completeHit.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data": map[string]interface{}{
				"channel":      map[string]interface{}{"id": 42},
				"expires_at":   "2027-01-01T00:00:00Z",
				"last_refresh": "2026-05-19T00:00:00Z",
			},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	fm := &fakeMenu{}
	c := newForTest(fm)
	c.creds = &config.Credentials{APIBase: srv.URL, AccessToken: "tok", UserID: 1}
	c.state = StateLoggedIn
	// Pre-stash a real api.Client pointing at the fake server. We
	// reach into the same package's constructor used by the real
	// handleAddClaude.
	c.claudeClient = apiClientForTest(srv.URL)

	c.handlePasteClaude()

	if completeHit.Load() != 1 {
		t.Errorf("complete called %d times, want 1", completeHit.Load())
	}
	if c.claudeClient != nil {
		t.Error("claudeClient not cleared on success")
	}
	if last, ok := fm.lastOfKind("claude-paste"); !ok || last.args[0] != false {
		t.Errorf("expected claude-paste(false), got %+v", fm.calls())
	}
	if len(*notes) == 0 || !strings.Contains((*notes)[len(*notes)-1].title, "Claude channel #42") {
		t.Errorf("expected success notification, got %+v", *notes)
	}
}

func TestHandlePasteClaude_EmptyClipboard(t *testing.T) {
	stubClipboard(t, "   ", nil)
	notes := captureNotifier(t)

	fm := &fakeMenu{}
	c := newForTest(fm)
	c.claudeClient = apiClientForTest("http://127.0.0.1:1")

	c.handlePasteClaude()

	if c.claudeClient == nil {
		t.Error("claudeClient cleared on empty-clipboard error")
	}
	if len(*notes) == 0 {
		t.Error("expected failure notification")
	}
}

// TestHandlePasteClaude_RejectsGarbage ensures a random clipboard
// payload doesn't get forwarded to the backend — the client-side
// regex catches it and surfaces a helpful error.
func TestHandlePasteClaude_RejectsGarbage(t *testing.T) {
	stubClipboard(t, "this is not an auth code at all", nil)
	notes := captureNotifier(t)

	c := newForTest(&fakeMenu{})
	c.claudeClient = apiClientForTest("http://127.0.0.1:1") // unreachable on purpose

	c.handlePasteClaude()

	if c.claudeClient == nil {
		t.Error("claudeClient cleared on validation failure (should stay so user can retry)")
	}
	if len(*notes) == 0 {
		t.Error("expected failure notification")
	}
}

// TestHandlePasteClaude_TruncatesAdversarialError verifies the
// notification body is capped when a backend error echoes the
// (potentially adversarial) clipboard input.
func TestHandlePasteClaude_TruncatesAdversarialError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// Use a valid-shape paste so we get past the client-side regex
	// and into the backend round-trip where the long error happens.
	stubClipboard(t, "validlooking-code#validlooking-state", nil)
	notes := captureNotifier(t)

	longEcho := strings.Repeat("X", 1024)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/seller/claude/oauth/complete", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false, "message": longEcho, "data": map[string]interface{}{},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := newForTest(&fakeMenu{})
	c.creds = &config.Credentials{APIBase: srv.URL, AccessToken: "tok", UserID: 1}
	c.claudeClient = apiClientForTest(srv.URL)

	c.handlePasteClaude()

	if len(*notes) == 0 {
		t.Fatal("expected failure notification")
	}
	last := (*notes)[len(*notes)-1]
	if len([]rune(last.body)) > notifyBodyMaxLen+1 { // +1 allows the "…" tail
		t.Errorf("notification body length = %d, want <= %d (got=%q)",
			len([]rune(last.body)), notifyBodyMaxLen+1, last.body)
	}
}

func TestHandlePasteClaude_NoFlowInProgress(t *testing.T) {
	notes := captureNotifier(t)
	fm := &fakeMenu{}
	c := newForTest(fm)
	// claudeClient nil — should be a no-op
	c.handlePasteClaude()
	if len(*notes) != 0 {
		t.Errorf("notifications on no-op call: %+v", *notes)
	}
}

// apiClientForTest constructs an api.Client without WithCookieJar
// — sufficient because the fake test server doesn't actually enforce
// the cookie session. Production code uses WithCookieJar; the test
// bypasses startClaudeOAuth by stashing the client directly.
func apiClientForTest(base string) *api.Client {
	return api.New(base, "tok").WithUserID(1).WithCookieJar()
}
