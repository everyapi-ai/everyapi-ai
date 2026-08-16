package cmd

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/tools"
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

func startTransparentLaunch(tool *tools.Tool, upstreamBase, relayDestination, relayKey string) (*transparentLaunch, error) {
	session, err := startTransparentConnector(upstreamBase, relayDestination, relayKey)
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

// startTransparentConnector owns a loopback listener, an ephemeral signing CA and the Connector goroutine for exactly one `everyapi use` child process. Any startup error is fatal to transparent mode; callers must not fall back to a direct vendor connection or to the legacy Base URL path. relayDestination is the gateway relayed traffic ultimately reaches. It differs from upstreamBase only when the caller chains the sanitizer in between; the connector's intercepted-origin loop guard validates it, so passing the real gateway here is what keeps that guard meaningful for a chained launch.
func startTransparentConnector(upstreamBase, relayDestination, relayKey string) (*transparentConnectorSession, error) {
	// Reap CA bundles orphaned by a previous transparent session hard-killed (SIGKILL / crash) before its stop() could remove its own ca-*.pem. Runs before we write this session's bundle so our fresh file is never a sweep candidate. Best-effort; see sweepStaleConnectorCABundles.
	sweepStaleConnectorCABundles()

	registry, err := connector.NewRegistry(connector.DefaultTargets())
	if err != nil {
		return nil, fmt.Errorf("build transparent connector registry: %w", err)
	}
	logger, closeLog := connectorLogger()
	server, err := connector.New(connector.Config{
		UpstreamBase:     upstreamBase,
		RelayDestination: relayDestination,
		RelayToken:       relayKey,
		Registry:         registry,
		Logger:           logger,
	})
	if err != nil {
		closeLog()
		return nil, fmt.Errorf("construct transparent connector: %w", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		closeLog()
		return nil, fmt.Errorf("bind transparent connector loopback listener: %w", err)
	}
	caPath, err := writeTransparentCABundle(server.CACertificatePEM())
	if err != nil {
		_ = listener.Close()
		closeLog()
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
			closeLog()
		})
	}

	// The listener is already bound, so a successful TCP connection proves the address is reachable before the child receives its proxy settings.
	readyDeadline := time.Now().Add(2 * time.Second)
	for {
		select {
		case serveErr := <-done:
			_ = os.Remove(caPath)
			cancel()
			closeLog()
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

// writeTransparentCABundle writes the ephemeral public CA and preserves any custom CA bundles the parent process already supplied. The signing key never leaves connector.Server memory.
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

// connectorLogger writes to connector.log. Without it, a transparent session's fail-closed blocks and relay/tunnel failures are invisible — the user only sees an opaque 502/403 on the tool side with no way to tell why.
//
// The caller closes the file on every startup-failure path; on the happy path the handle is owned by stop() (run by ExecWithOptions after the child exits).
func connectorLogger() (*log.Logger, func()) {
	return loopbackProxyLogger("connector.log")
}

// staleConnectorCAAge is how old a leftover connector/ca-*.pem must be before sweepStaleConnectorCABundles reaps it. A live session's bundle is written once at launch and never touched again, so ModTime is the session's age, not its idleness — the floor must therefore exceed the longest a session can still be working. That bound is exactly connector.CertificateLifetime: past it the CA in the bundle is expired, so the session it belongs to is already broken and reaping the file takes nothing from it. Derived from the connector's constant rather than restated, so the two cannot drift apart — an earlier version of this hardcoded 24h against a 24h CA and would have deleted the in-use bundle of any session that outlived its own certificate.
//
// The extra day is slack for clock skew and for a session that started just before the boundary.
var staleConnectorCAAge = connector.CertificateLifetime + 24*time.Hour

// sweepStaleConnectorCABundles best-effort removes connector/ca-*.pem files orphaned by a transparent session that was SIGKILLed before stop() could remove its own bundle. Every error is ignored: a sweep failure must never block a launch. Only files older than staleConnectorCAAge are touched, so a concurrently launching session's just-written bundle is never deleted.
func sweepStaleConnectorCABundles() {
	dir, err := config.ConfigDir()
	if err != nil {
		return
	}
	// os.ReadDir + prefix/suffix rather than filepath.Glob: the directory comes from ConfigDir (XDG_CONFIG_HOME or $HOME), and a glob metacharacter anywhere in that path — '*', '?', '[' — makes the pattern match nothing and silently disables the sweep instead of erroring.
	connectorDir := filepath.Join(dir, "connector")
	entries, err := os.ReadDir(connectorDir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-staleConnectorCAAge)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "ca-") || !strings.HasSuffix(name, ".pem") {
			continue
		}
		info, statErr := e.Info()
		if statErr != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(connectorDir, name))
		}
	}
}
