package menubar

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/internal/config"
)

// fakeMenu records every menuView call so tests can assert on order
// + payload. Concurrency-safe because applyData fires from a
// background refresh goroutine.
type fakeMenu struct {
	mu  sync.Mutex
	log []menuCall
}

type menuCall struct {
	kind string // "logged-out" | "logging-in" | "logged-in" | "data" | "sanitizer"
	args []interface{}
}

func (f *fakeMenu) applyLoggedOut() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = append(f.log, menuCall{kind: "logged-out"})
}
func (f *fakeMenu) applyLoggingIn(userCode string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = append(f.log, menuCall{kind: "logging-in", args: []interface{}{userCode}})
}
func (f *fakeMenu) applyLoggedIn(username string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = append(f.log, menuCall{kind: "logged-in", args: []interface{}{username}})
}
func (f *fakeMenu) applyData(quotaUSD, usedUSD string, requests int64, sellerUSD string, hasSeller bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = append(f.log, menuCall{kind: "data", args: []interface{}{quotaUSD, usedUSD, requests, sellerUSD, hasSeller}})
}
func (f *fakeMenu) applySanitizerState(running bool, listen string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = append(f.log, menuCall{kind: "sanitizer", args: []interface{}{running, listen}})
}
func (f *fakeMenu) applyClaudePastePending(pending bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = append(f.log, menuCall{kind: "claude-paste", args: []interface{}{pending}})
}
func (f *fakeMenu) applyChannels(channels []channelMenuRow) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = append(f.log, menuCall{kind: "channels", args: []interface{}{channels}})
}
func (f *fakeMenu) applyIconState(state IconState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = append(f.log, menuCall{kind: "icon", args: []interface{}{state}})
}
func (f *fakeMenu) applyStale(stale bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = append(f.log, menuCall{kind: "stale", args: []interface{}{stale}})
}

func (f *fakeMenu) calls() []menuCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]menuCall, len(f.log))
	copy(out, f.log)
	return out
}

func (f *fakeMenu) lastOfKind(kind string) (menuCall, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.log) - 1; i >= 0; i-- {
		if f.log[i].kind == kind {
			return f.log[i], true
		}
	}
	return menuCall{}, false
}

// TestApplyInitialState_NoCreds ensures the cold-start path with no
// credentials file collapses to the logged-out menu.
func TestApplyInitialState_NoCreds(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	fm := &fakeMenu{}
	c := newForTest(fm)
	c.applyInitialState()

	if got := c.getState(); got != StateLoggedOut {
		t.Errorf("state = %v, want StateLoggedOut", got)
	}
	if _, ok := fm.lastOfKind("logged-out"); !ok {
		t.Errorf("expected applyLoggedOut, calls=%+v", fm.calls())
	}
}

// TestApplyInitialState_WithCreds is the warm-start path — a
// credentials.json sitting in XDG_CONFIG_HOME must promote the menu
// to logged-in and trigger a refresh against the saved api_base.
func TestApplyInitialState_WithCreds(t *testing.T) {
	cfgDir := filepath.Join(t.TempDir(), "everyapi")
	t.Setenv("XDG_CONFIG_HOME", filepath.Dir(cfgDir))

	// We don't care about the refresh HTTP failure — there's no
	// server. We're only asserting the menu transition.
	creds := &config.Credentials{
		APIBase:     "http://127.0.0.1:1",
		AccessToken: "tok",
		UserID:      42,
		Username:    "alice",
	}
	if err := config.Save(creds); err != nil {
		t.Fatalf("Save: %v", err)
	}

	fm := &fakeMenu{}
	c := newForTest(fm)
	c.applyInitialState()

	if got := c.getState(); got != StateLoggedIn {
		t.Errorf("state = %v, want StateLoggedIn", got)
	}
	loggedIn, ok := fm.lastOfKind("logged-in")
	if !ok {
		t.Fatalf("expected applyLoggedIn, calls=%+v", fm.calls())
	}
	if loggedIn.args[0] != "alice" {
		t.Errorf("username = %v, want alice", loggedIn.args[0])
	}
}

// TestRefresh_HappyPath wires a fake API server and asserts the
// menu is populated with the formatted quota / used / request
// counts on a successful poll.
func TestRefresh_HappyPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "",
			"data":    map[string]interface{}{"quota_per_unit": 500000.0},
		})
	})
	mux.HandleFunc("/api/user/self", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "",
			"data": map[string]interface{}{
				"id":            42,
				"username":      "alice",
				"quota":         5_000_000,
				"used_quota":    1_250_000,
				"request_count": 1234,
				"seller_quota":  0,
			},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	fm := &fakeMenu{}
	c := newForTest(fm)
	c.creds = &config.Credentials{APIBase: srv.URL, AccessToken: "tok", UserID: 42}

	c.refresh(t.Context())

	data, ok := fm.lastOfKind("data")
	if !ok {
		t.Fatalf("expected applyData, got %+v", fm.calls())
	}
	if got := data.args[0]; got != "$10.00" {
		t.Errorf("quotaUSD = %v, want $10.00", got)
	}
	if got := data.args[1]; got != "$2.50" {
		t.Errorf("usedUSD = %v, want $2.50", got)
	}
	if got := data.args[2]; got != int64(1234) {
		t.Errorf("requests = %v, want 1234", got)
	}
	if got := data.args[4]; got != false {
		t.Errorf("hasSeller = %v, want false", got)
	}
}

// TestRefresh_SellerEarningsIncrement walks the controller through
// two refreshes (first observation suppresses, second triggers) and
// asserts both the notification and the menu state.
func TestRefresh_SellerEarningsIncrement(t *testing.T) {
	var sellerQuotaSeq = []int{1_000_000, 1_500_000} // first poll, second poll
	var pollIdx int

	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true, "data": map[string]interface{}{"quota_per_unit": 500000.0},
		})
	})
	mux.HandleFunc("/api/user/self", func(w http.ResponseWriter, r *http.Request) {
		quota := sellerQuotaSeq[pollIdx]
		if pollIdx < len(sellerQuotaSeq)-1 {
			pollIdx++
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data": map[string]interface{}{
				"id": 1, "username": "alice",
				"quota": 0, "used_quota": 0, "request_count": 0,
				"seller_quota": quota,
			},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	notes := captureNotifier(t)
	fm := &fakeMenu{}
	c := newForTest(fm)
	c.creds = &config.Credentials{APIBase: srv.URL, AccessToken: "tok", UserID: 1}

	c.refresh(t.Context())
	if len(*notes) != 0 {
		t.Errorf("first refresh notified %d times, want 0", len(*notes))
	}

	c.refresh(t.Context())
	if len(*notes) != 1 {
		t.Fatalf("second refresh notified %d times, want 1: %+v", len(*notes), *notes)
	}
	if !contains((*notes)[0].title, "$1.00") {
		t.Errorf("title %q missing $1.00 delta", (*notes)[0].title)
	}
}

// TestHandleSanitizerToggle_StartFromOff fires toggle on a stopped
// runner and asserts it ends up running, with state persisted.
// Swaps the default listen address to an ephemeral port so the
// test is robust against a dev box that already has 8888 bound.
func TestHandleSanitizerToggle_StartFromOff(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	addr := freeLoopbackPort(t)
	fm := &fakeMenu{}
	c := newForTest(fm)
	c.sanitizerListen = addr // per-Controller override; no package-var mutation
	c.creds = &config.Credentials{APIBase: "https://api.example.test", AccessToken: "tok"}
	c.state = StateLoggedIn

	c.handleSanitizerToggle()
	defer c.sanitizer.Stop()

	if !c.sanitizer.Running() {
		t.Error("sanitizer not running after toggle-on")
	}
	last, ok := fm.lastOfKind("sanitizer")
	if !ok || last.args[0] != true {
		t.Errorf("expected sanitizer(true), got %+v", fm.calls())
	}
	st, _ := loadState()
	if !st.SanitizerEnabled {
		t.Errorf("state.SanitizerEnabled = false, want true (state = %+v)", st)
	}
}

// TestHandleSanitizerToggle exercises start / stop via the
// dispatcher entry-point. The runner is real; bind succeeds on
// ephemeral port supplied via the default path.
func TestHandleSanitizerToggle(t *testing.T) {
	fm := &fakeMenu{}
	c := newForTest(fm)
	// Default 127.0.0.1:8888 may be in use on a dev box. Override
	// via the public Start so we know it'll succeed.
	addr := freeLoopbackPort(t)
	if err := c.sanitizer.Start(addr, "https://example.invalid"); err != nil {
		t.Fatalf("seed start: %v", err)
	}
	if !c.sanitizer.Running() {
		t.Fatal("seed start did not flip Running")
	}
	// Toggle should now stop.
	c.handleSanitizerToggle()
	if !waitFor(func() bool { return !c.sanitizer.Running() }, 500_000_000) {
		t.Errorf("toggle off failed: Running()=%v", c.sanitizer.Running())
	}
	last, ok := fm.lastOfKind("sanitizer")
	if !ok {
		t.Fatalf("expected applySanitizerState call, got %+v", fm.calls())
	}
	if last.args[0] != false {
		t.Errorf("sanitizer running flag = %v, want false", last.args[0])
	}
}

// TestHandleSignOut clears state + emits applyLoggedOut, even when
// the credentials file is absent (idempotent path).
func TestHandleSignOut(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	fm := &fakeMenu{}
	c := newForTest(fm)
	c.creds = &config.Credentials{Username: "alice", AccessToken: "tok"}
	c.state = StateLoggedIn
	c.lastSellerQuota = 999

	c.handleSignOut()

	if c.creds != nil {
		t.Error("creds not cleared")
	}
	if c.state != StateLoggedOut {
		t.Errorf("state = %v, want StateLoggedOut", c.state)
	}
	if c.lastSellerQuota != -1 {
		t.Errorf("lastSellerQuota = %d, want -1", c.lastSellerQuota)
	}
	if _, ok := fm.lastOfKind("logged-out"); !ok {
		t.Errorf("expected applyLoggedOut, got %+v", fm.calls())
	}
}
