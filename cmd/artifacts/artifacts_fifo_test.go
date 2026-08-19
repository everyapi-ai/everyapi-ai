//go:build !windows

package artifacts

import (
	"context"
	"net/http"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/everyapi-ai/everyapi-sdk/config"
)

func TestPublishRejectsNamedPipeWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.html")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := publish(context.Background(), http.DefaultClient, "https://artifacts.invalid", &config.Credentials{APIBase: config.DefaultAPIBase, AccessToken: "access-token", UserID: 42}, path)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("want an error for a named pipe")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("publish blocked while opening a named pipe")
	}
}
