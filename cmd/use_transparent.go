package cmd

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/everyapi-ai/everyapi-ai/internal/tools"
	"github.com/everyapi-ai/everyapi-sdk/config"
	"github.com/everyapi-ai/everyapi-sdk/connector"
)

type transparentConnectorSession struct {
	proxyURL string
	caPath   string
	stop     func()
}

type transparentLaunch struct {
	session  *transparentConnectorSession
	env      map[string]string
	unsetEnv []string
}

func startTransparentLaunch(tool *tools.Tool, upstreamBase, relayKey string) (*transparentLaunch, error) {
	session, err := startTransparentConnector(upstreamBase, relayKey)
	if err != nil {
		return nil, err
	}
	env, unset, err := tool.TransparentEnv(session.proxyURL, session.caPath)
	if err != nil {
		session.stop()
		return nil, err
	}
	return &transparentLaunch{session: session, env: env, unsetEnv: unset}, nil
}

// startTransparentConnector owns a loopback listener, an ephemeral signing CA
// and the Connector goroutine for exactly one `everyapi use` child process.
// Any startup error is fatal to transparent mode; callers must not fall back to
// a direct vendor connection or to the legacy Base URL path.
func startTransparentConnector(upstreamBase, relayKey string) (*transparentConnectorSession, error) {
	registry, err := connector.NewRegistry(connector.DefaultTargets())
	if err != nil {
		return nil, fmt.Errorf("build transparent connector registry: %w", err)
	}
	server, err := connector.New(connector.Config{
		UpstreamBase: upstreamBase,
		RelayToken:   relayKey,
		Registry:     registry,
		Logger:       log.New(io.Discard, "", 0),
	})
	if err != nil {
		return nil, fmt.Errorf("construct transparent connector: %w", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("bind transparent connector loopback listener: %w", err)
	}
	caPath, err := writeTransparentCABundle(server.CACertificatePEM())
	if err != nil {
		_ = listener.Close()
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx, listener)
	}()

	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				_ = listener.Close()
			}
			_ = os.Remove(caPath)
		})
	}

	// The listener is already bound, so a successful TCP connection proves
	// the address is reachable before the child receives its proxy settings.
	readyDeadline := time.Now().Add(2 * time.Second)
	for {
		select {
		case serveErr := <-done:
			_ = os.Remove(caPath)
			cancel()
			if serveErr == nil {
				serveErr = fmt.Errorf("server stopped before becoming ready")
			}
			return nil, fmt.Errorf("start transparent connector: %w", serveErr)
		default:
		}
		conn, dialErr := net.DialTimeout("tcp", listener.Addr().String(), 100*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			break
		}
		if time.Now().After(readyDeadline) {
			stop()
			return nil, fmt.Errorf("transparent connector did not become ready: %w", dialErr)
		}
		time.Sleep(20 * time.Millisecond)
	}

	return &transparentConnectorSession{
		proxyURL: "http://" + listener.Addr().String(),
		caPath:   caPath,
		stop:     stop,
	}, nil
}

// writeTransparentCABundle writes the ephemeral public CA and preserves any
// custom CA bundles the parent process already supplied. The signing key never
// leaves connector.Server memory.
func writeTransparentCABundle(connectorCA []byte) (string, error) {
	dir, err := config.ConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve EveryAPI config directory: %w", err)
	}
	dir = filepath.Join(dir, "connector")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create connector config directory: %w", err)
	}
	file, err := os.CreateTemp(dir, "ca-*.pem")
	if err != nil {
		return "", fmt.Errorf("create connector CA bundle: %w", err)
	}
	path := file.Name()
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(path)
	}
	if err := file.Chmod(0o600); err != nil {
		cleanup()
		return "", fmt.Errorf("secure connector CA bundle: %w", err)
	}
	if _, err := file.Write(connectorCA); err != nil {
		cleanup()
		return "", fmt.Errorf("write connector CA bundle: %w", err)
	}

	seen := map[string]struct{}{path: {}}
	for _, name := range []string{"NODE_EXTRA_CA_CERTS", "CODEX_CA_CERTIFICATE", "SSL_CERT_FILE"} {
		candidate := strings.TrimSpace(os.Getenv(name))
		if candidate == "" {
			continue
		}
		candidate, _ = filepath.Abs(candidate)
		if _, duplicate := seen[candidate]; duplicate {
			continue
		}
		seen[candidate] = struct{}{}
		body, readErr := os.ReadFile(candidate)
		if readErr != nil || len(body) == 0 {
			continue
		}
		if _, err := file.Write([]byte("\n")); err != nil {
			cleanup()
			return "", fmt.Errorf("extend connector CA bundle: %w", err)
		}
		if _, err := file.Write(body); err != nil {
			cleanup()
			return "", fmt.Errorf("extend connector CA bundle: %w", err)
		}
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close connector CA bundle: %w", err)
	}
	return path, nil
}
