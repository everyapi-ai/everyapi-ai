package menubar

import (
	"errors"
	"os"
	"sync/atomic"
	"testing"
)

func stubOpenInEditor(t *testing.T, retErr error) *atomic.Int32 {
	t.Helper()
	prev := openInEditorFn
	var calls atomic.Int32
	openInEditorFn = func(path string) error {
		calls.Add(1)
		return retErr
	}
	t.Cleanup(func() { openInEditorFn = prev })
	return &calls
}

func TestHandlePreferences_CreatesSeedAndOpens(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	calls := stubOpenInEditor(t, nil)
	notes := captureNotifier(t)

	c := newForTest(&fakeMenu{})
	c.handlePreferences()

	if calls.Load() != 1 {
		t.Errorf("editor opens = %d, want 1", calls.Load())
	}
	if len(*notes) == 0 {
		t.Error("expected post-open notification with restart hint")
	}

	// Seed file should now exist on disk.
	path, _ := prefsPath()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("prefs seed not created: %v", err)
	}
}

func TestHandlePreferences_EditorError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	stubOpenInEditor(t, errors.New("editor missing"))
	notes := captureNotifier(t)

	c := newForTest(&fakeMenu{})
	c.handlePreferences()

	if len(*notes) == 0 {
		t.Error("expected failure notification")
	}
}
