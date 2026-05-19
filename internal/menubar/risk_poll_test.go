package menubar

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/internal/config"
)

// TestPollChannelRisk_HappyPath drives the full poll loop body
// against an httptest backend: seed cache with one enabled channel,
// have the server flip it to auto-disabled, assert the notification
// fires.
func TestPollChannelRisk_HappyPath(t *testing.T) {
	notes := captureNotifier(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/seller/channel", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data": map[string]interface{}{
				"items": []map[string]interface{}{
					{"id": 1, "name": "claude-prod", "type": 0, "status": channelStatusAutoDisable, "models": "m"},
				},
				"total": 1, "page": 1, "page_size": 50,
			},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := newForTest(&fakeMenu{})
	c.creds = &config.Credentials{APIBase: srv.URL, AccessToken: "tok", UserID: 1}
	c.channelStatusCache = map[int]int{1: channelStatusEnabled} // simulate prior poll

	c.pollChannelRisk(context.Background())

	if len(*notes) != 1 {
		t.Fatalf("notifications = %d, want 1: %+v", len(*notes), *notes)
	}
	if !strings.Contains((*notes)[0].body, "claude-prod") {
		t.Errorf("body %q missing channel name", (*notes)[0].body)
	}
	if c.channelStatusCache[1] != channelStatusAutoDisable {
		t.Errorf("cache not updated to %d, got %d", channelStatusAutoDisable, c.channelStatusCache[1])
	}
}

// TestPollChannelRisk_NotSignedIn — no creds, no HTTP call, no
// notifications. Exercises the early-return guard.
func TestPollChannelRisk_NotSignedIn(t *testing.T) {
	notes := captureNotifier(t)
	c := newForTest(&fakeMenu{})
	c.pollChannelRisk(context.Background())
	if len(*notes) != 0 {
		t.Errorf("notifications when signed-out: %+v", *notes)
	}
}
