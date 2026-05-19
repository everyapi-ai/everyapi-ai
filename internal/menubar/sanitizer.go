package menubar

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/everyapi-ai/everyapi-ai/internal/config"
	"github.com/everyapi-ai/everyapi-ai/internal/sanitizer"
)

// defaultSanitizerListen mirrors the CLI's `everyapi proxy start`
// default (cmd/proxy/proxy.go). Loopback-only by design — see the
// long comment on sanitizer.Config.AllowNonLoopback.
//
// Held as a constant; per-Controller override (via prefs or tests)
// lives on Controller.sanitizerListen.
const defaultSanitizerListen = "127.0.0.1:8888"

// sanitizerRunner owns the lifecycle of one in-process sanitizer
// proxy. Start / Stop are idempotent (Start while running is a no-op,
// Stop while not running is a no-op) so the menu toggle doesn't need
// to track state itself — applySanitizerState reads Running() each
// time the menu is built / refreshed.
type sanitizerRunner struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	listen string // address actually bound, populated on Start success
}

// Start launches the proxy on the given listen address with the
// gateway as the upstream. Returns once the server's Run goroutine
// has been spawned — there's no synchronous "ready" signal because
// sanitizer.Server.Run blocks until the listener fails or the ctx
// fires, and we want Start to return promptly. A bind failure shows
// up via Running() flipping back to false within ~50ms; the menubar
// reacts to that on the next refresh tick.
func (s *sanitizerRunner) Start(listen, upstream string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return nil // already running
	}
	if listen == "" {
		listen = defaultSanitizerListen
	}
	// Caller (handleSanitizerToggle) is responsible for resolving
	// the right address — Controller.sanitizerListen holds the
	// prefs override; this default kicks in for tests that hand us
	// the empty string.
	if upstream == "" {
		upstream = config.DefaultAPIBase
	}
	srv, err := sanitizer.New(sanitizer.Config{
		Listen:       listen,
		UpstreamBase: upstream,
	})
	if err != nil {
		return fmt.Errorf("init sanitizer: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.listen = listen
	go func() {
		if err := srv.Run(ctx); err != nil {
			log.Printf("menubar: sanitizer exited: %v", err)
		}
		// On exit (ctx canceled or bind error), clear cancel so a
		// subsequent Start re-binds. Take the lock to avoid racing
		// with Stop.
		s.mu.Lock()
		s.cancel = nil
		s.listen = ""
		s.mu.Unlock()
	}()
	return nil
}

// Stop cancels the running proxy and blocks briefly while its
// Shutdown completes. Idempotent.
func (s *sanitizerRunner) Stop() {
	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.listen = ""
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Running reports whether the proxy goroutine is currently active.
func (s *sanitizerRunner) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancel != nil
}

// Listen returns the current bind address ("" when stopped).
func (s *sanitizerRunner) Listen() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listen
}
