package menubar

import (
	"strings"
	"sync/atomic"
	"testing"
)

func stubRevealInFileBrowser(t *testing.T) (captured *string, calls *atomic.Int32) {
	t.Helper()
	prev := revealInFileBrowser
	var path string
	var n atomic.Int32
	revealInFileBrowser = func(p string) error {
		path = p
		n.Add(1)
		return nil
	}
	t.Cleanup(func() { revealInFileBrowser = prev })
	return &path, &n
}

func TestHandleOpenDocs(t *testing.T) {
	browser := stubOpenBrowser(t)
	c := newForTest(&fakeMenu{})
	c.handleOpenDocs()
	if len(*browser) != 1 || !strings.Contains((*browser)[0], "everyapi-docs") {
		t.Errorf("docs browser opens = %v", *browser)
	}
}

func TestHandleReportIssue(t *testing.T) {
	browser := stubOpenBrowser(t)
	c := newForTest(&fakeMenu{})
	c.handleReportIssue()
	if len(*browser) != 1 || !strings.Contains((*browser)[0], "/issues/new") {
		t.Errorf("issues browser opens = %v", *browser)
	}
}

func TestHandleRevealConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, calls := stubRevealInFileBrowser(t)
	c := newForTest(&fakeMenu{})
	c.handleRevealConfig()
	if calls.Load() != 1 {
		t.Errorf("revealInFileBrowser calls = %d, want 1", calls.Load())
	}
	if !strings.Contains(*path, "everyapi") {
		t.Errorf("path %q missing 'everyapi'", *path)
	}
}

func TestHandleAbout(t *testing.T) {
	var captured string
	prev := confirmDialog
	confirmDialog = func(title, body, ok, cancel string) (bool, error) {
		captured = body
		return true, nil
	}
	t.Cleanup(func() { confirmDialog = prev })

	c := newForTest(&fakeMenu{})
	c.handleAbout()

	for _, want := range []string{"EveryAPI", "Version:", "Commit:", "MIT", "Docs:"} {
		if !strings.Contains(captured, want) {
			t.Errorf("about body missing %q\n--- body ---\n%s", want, captured)
		}
	}
}
