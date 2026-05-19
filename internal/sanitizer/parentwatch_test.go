package sanitizer

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestServer_ParentPIDShutsDownWhenParentDies — when ParentPID is
// set on the Config, the server's background watcher should notice
// the parent process going away and trigger a graceful shutdown
// without anyone calling cancel() on the outer ctx.
func TestServer_ParentPIDShutsDownWhenParentDies(t *testing.T) {
	// Spawn a short-lived child whose pid we'll watch. `sleep 1`
	// is universally available and gives us a deterministic exit.
	child := exec.Command("sleep", "1")
	if err := child.Start(); err != nil {
		t.Fatalf("spawn watched child: %v", err)
	}
	pidToWatch := child.Process.Pid

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(204)
	}))
	defer upstream.Close()

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	_ = ln.Close()

	srv, err := New(Config{
		Listen:       addr,
		UpstreamBase: upstream.URL,
		ParentPID:    pidToWatch,
		Logger:       log.New(io.Discard, "", 0),
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()

	// Wait briefly for the listener to come up.
	dl := time.Now().Add(2 * time.Second)
	for time.Now().Before(dl) {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The child will exit on its own in ~1s. Wait for the watcher
	// (2s poll interval) to notice and shut down.
	_ = child.Wait()
	// Sleep > watcher tick to give the watcher a chance.
	select {
	case err := <-done:
		// Got graceful shutdown — what we wanted.
		if err != nil {
			t.Errorf("server returned error on parent-death shutdown: %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatalf("server did not shut down within 8s of parent exit")
	}
	// Sanity: post-shutdown, the listener should be released.
	if _, err := net.DialTimeout("tcp", addr, 100*time.Millisecond); err == nil {
		t.Errorf("listener still accepting connections after shutdown")
	}
}

// TestServer_ParentPIDZeroDisablesWatcher — when ParentPID is 0
// (the common case for foreground `everyapi proxy start`), the
// watcher must not run; otherwise tests that don't pass a pid
// would race against a goroutine reading their own pid for no
// reason. We verify by checking the server runs and accepts a
// request normally; with a buggy watcher reading pid=0, the
// pidAlive helper might trigger spurious shutdown.
func TestServer_ParentPIDZeroDisablesWatcher(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer upstream.Close()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	_ = ln.Close()
	srv, err := New(Config{
		Listen:       addr,
		UpstreamBase: upstream.URL,
		ParentPID:    0,
		Logger:       log.New(io.Discard, "", 0),
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	// Wait a bit longer than the watcher's 2s tick to prove
	// nothing self-shuts-down.
	time.Sleep(3 * time.Second)
	// Hit the proxy to confirm it's still alive.
	conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("server stopped accepting connections (ParentPID=0 watcher misfire?): %v", err)
	}
	_ = conn.Close()
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("graceful shutdown didn't complete")
	}
}

// pidAlive on a definitely-dead pid returns false. Uses a pid we
// know is gone (we wait on a child) — keeps the test cross-platform
// without making assumptions about pid numbering.
func TestPidAlive_DeadProcess(t *testing.T) {
	child := exec.Command("sleep", "0.05")
	if err := child.Start(); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	pid := child.Process.Pid
	_ = child.Wait()
	// On macOS the process is fully reaped at Wait(); on Linux the
	// zombie persists until the parent reaps it (which Wait did).
	// Give the kernel a moment to drop the entry.
	time.Sleep(200 * time.Millisecond)
	if pidAlive(pid) {
		t.Errorf("pidAlive(%d) returned true for reaped child", pid)
	}
	// Sanity: our own pid is always alive.
	if !pidAlive(os.Getpid()) {
		t.Errorf("pidAlive(self) returned false")
	}
	// Pid 0 is the sentinel "not set"; pidAlive should refuse.
	if pidAlive(0) {
		t.Errorf("pidAlive(0) returned true; want false (sentinel)")
	}
}
