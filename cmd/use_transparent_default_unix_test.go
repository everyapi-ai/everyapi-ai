//go:build !windows

package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-sdk/config"
)

const (
	useDefaultCallerEnv = "EVERYAPI_TEST_USE_DEFAULT_CALLER" // runs Use()
	useDefaultShimEnv   = "EVERYAPI_TEST_USE_DEFAULT_SHIM"   // stands in for `claude`
	// An ambient NO_PROXY the user is assumed to have exported. Injected
	// deliberately after the host's proxy vars are stripped, so the launch paths
	// are tested against a known value rather than whatever the developer or CI
	// runner happens to have set.
	ambientNoProxy = ".corp.internal,10.0.0.0/8"
)

// TestUseDefaultsToTransparentForSupportedTool is the end-to-end pin for this
// PR's headline behavior: a bare `everyapi use claude`, no flags, must launch
// through the connector. Nothing else asserted it — the parser tests cover only
// flag parsing and TestTransparentDefaultResolution covers only
// Tool.SupportsTransparent, so the resolution inside Use (the code that actually
// decides) could silently regress to the injected path.
//
// The launched child's environment is the only honest evidence of which path
// ran: transparent unsets ANTHROPIC_BASE_URL and points HTTPS_PROXY at the
// loopback connector while withholding the relay key; the injected path does
// the exact opposite. Asserting on the launch banner would only re-read our own
// Printf.
func TestUseDefaultsToTransparentForSupportedTool(t *testing.T) {
	switch {
	case os.Getenv(useDefaultShimEnv) == "1":
		// Stands in for the real `claude`: record what env we were handed.
		out := map[string]string{
			"ANTHROPIC_BASE_URL":   os.Getenv("ANTHROPIC_BASE_URL"),
			"ANTHROPIC_AUTH_TOKEN": os.Getenv("ANTHROPIC_AUTH_TOKEN"),
			"HTTPS_PROXY":          os.Getenv("HTTPS_PROXY"),
			"NO_PROXY":             os.Getenv("NO_PROXY"),
		}
		b, _ := json.Marshal(out)
		_ = os.WriteFile(os.Getenv("EVERYAPI_TEST_USE_ENV_FILE"), b, 0o600)
		return
	case os.Getenv(useDefaultCallerEnv) == "1":
		// tools.Exec never returns, so Use runs in its own process.
		args := strings.Split(os.Getenv("EVERYAPI_TEST_USE_ARGS"), ",")
		if err := Use(args); err != nil {
			t.Fatal(err)
		}
		return
	}

	for _, tc := range []struct {
		name            string
		args            []string
		wantTransparent bool
	}{
		{"bare invocation defaults to transparent", []string{"claude"}, true},
		{"explicit opt-out uses the injected path", []string{"claude", "--transparent=false"}, false},
		// --sanitize moves the catalogue onto the sanitizer's socket, so the
		// injected base URL is the sanitizer's address and no catalogue proxy
		// of its own gets started. Both facts have to leave NO_PROXY set.
		{"injected path with the transforms merged", []string{"claude", "--transparent=false", "--sanitize"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []any{map[string]any{"id": "claude-test", "owned_by": "anthropic", "supported_endpoint_types": []string{"anthropic"}}}})
			}))
			defer gateway.Close()

			configRoot := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", configRoot)
			if err := config.Save(&config.Credentials{APIBase: gateway.URL, RelayKey: "sk-everyapi-test"}); err != nil {
				t.Fatal(err)
			}

			envPath := filepath.Join(t.TempDir(), "env.json")
			shimDir := t.TempDir()
			shim := "#!/bin/sh\n" +
				useDefaultShimEnv + "=1 exec \"$EVERYAPI_TEST_USE_TEST_BINARY\" -test.run=^TestUseDefaultsToTransparentForSupportedTool$\n"
			if err := os.WriteFile(filepath.Join(shimDir, "claude"), []byte(shim), 0o755); err != nil {
				t.Fatal(err)
			}

			child := exec.Command(os.Args[0], "-test.run=^TestUseDefaultsToTransparentForSupportedTool$")
			// Hermetic: strip the host's proxy variables. socksOnlyEgressVar
			// reads them, so a developer or CI runner with a SOCKS proxy
			// exported would send Use down the injected path and make the
			// "bare invocation defaults to transparent" case pass for the wrong
			// reason — or fail for a reason that has nothing to do with the code.
			hostEnv := []string{}
			for _, kv := range os.Environ() {
				name, _, _ := strings.Cut(kv, "=")
				switch name {
				case "HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy",
					"ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy":
					continue
				}
				hostEnv = append(hostEnv, kv)
			}
			child.Env = append(hostEnv,
				useDefaultCallerEnv+"=1",
				"EVERYAPI_TEST_USE_ARGS="+strings.Join(tc.args, ","),
				"EVERYAPI_TEST_USE_ENV_FILE="+envPath,
				"EVERYAPI_TEST_USE_TEST_BINARY="+os.Args[0],
				"XDG_CONFIG_HOME="+configRoot,
				"PATH="+shimDir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"NO_PROXY="+ambientNoProxy,
			)
			if out, err := child.CombinedOutput(); err != nil {
				t.Fatalf("use failed: %v\n%s", err, out)
			}

			raw, err := os.ReadFile(envPath)
			if err != nil {
				t.Fatalf("tool was never launched: %v", err)
			}
			var got map[string]string
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}

			if tc.wantTransparent {
				if got["ANTHROPIC_BASE_URL"] != "" {
					t.Errorf("ANTHROPIC_BASE_URL = %q, want unset — the tool must stay on its vendor origin", got["ANTHROPIC_BASE_URL"])
				}
				if !strings.HasPrefix(got["HTTPS_PROXY"], "http://127.0.0.1:") {
					t.Errorf("HTTPS_PROXY = %q, want the loopback connector", got["HTTPS_PROXY"])
				}
				if got["ANTHROPIC_AUTH_TOKEN"] == "sk-everyapi-test" {
					t.Error("the real relay key reached the child under transparent mode")
				}
				// Transparent mode unsets NO_PROXY outright: the connector has
				// to see every request, and an inherited exclusion could let a
				// vendor origin bypass it entirely.
				if got["NO_PROXY"] != "" {
					t.Errorf("NO_PROXY = %q, want it unset so nothing bypasses the connector", got["NO_PROXY"])
				}
			} else {
				if !strings.HasPrefix(got["ANTHROPIC_BASE_URL"], "http://127.0.0.1:") {
					t.Errorf("ANTHROPIC_BASE_URL = %q, want a process-local listener", got["ANTHROPIC_BASE_URL"])
				}
				if got["ANTHROPIC_AUTH_TOKEN"] != "sk-everyapi-test" {
					t.Errorf("ANTHROPIC_AUTH_TOKEN = %q, want the relay key on the injected path", got["ANTHROPIC_AUTH_TOKEN"])
				}
				// The base URL above is loopback, so an ambient corporate proxy
				// must be excluded or the tool's requests never reach it. This
				// has to hold however the launch arrived at a loopback base —
				// keying it on which hop was started silently broke the moment
				// the sanitizer could host the catalogue itself.
				//
				// PREPENDED, not replaced. mergeEnvRemoving overlays this map
				// onto os.Environ() by key, so assigning a bare loopback list
				// discards the user's own exclusions and pushes the child's
				// internal-host traffic back through the corporate proxy. The
				// ambient value is injected above precisely so that a
				// regression to a plain assignment fails here.
				wantNoProxy := "127.0.0.1,localhost," + ambientNoProxy
				if got["NO_PROXY"] != wantNoProxy {
					t.Errorf("NO_PROXY = %q, want %q — the loopback exemption must extend the user's value, not replace it",
						got["NO_PROXY"], wantNoProxy)
				}
				// On this path the launch line prints the gateway while the tool
				// is pointed at a loopback port, so the log is the only thing
				// connecting the two. The transparent path gets the same record
				// in connector.log.
				logged, readErr := os.ReadFile(filepath.Join(configRoot, "everyapi", "model-catalog.log"))
				if readErr != nil {
					t.Fatalf("no catalogue log for an injected launch: %v", readErr)
				}
				if !strings.Contains(string(logged), "launch: claude via "+got["ANTHROPIC_BASE_URL"]) {
					t.Errorf("catalogue log does not tie the tool to the base URL it was given (%s):\n%s",
						got["ANTHROPIC_BASE_URL"], logged)
				}
			}
		})
	}
}

// TestUseKeepsTheCatalogueWhenTheSanitizerFailsToStart covers the fallback the
// merge introduced: the catalogue transform is built before a host is chosen,
// so a sanitizer that cannot start must leave it running on its own listener
// rather than taking it down as collateral. Losing the mask is what the user
// asked for by continuing; losing the model filter has nothing to do with why
// the sanitizer failed.
//
// Nothing else exercises this branch. Changing the guard at the second host
// site to skip the standalone proxy whenever a sanitizer was attempted leaves
// the rest of the suite green, while a real user would silently get an
// unfiltered /model picker and unrewritten claude-everyapi-* aliases.
//
// The failure is induced through the real code path: --sanitize makes
// startInProcessSanitizer load sanitizer.json, and a malformed one is a hard
// error rather than a fall-back-to-defaults.
func TestUseKeepsTheCatalogueWhenTheSanitizerFailsToStart(t *testing.T) {
	switch {
	case os.Getenv(useDefaultShimEnv) == "1":
		out := map[string]string{
			"ANTHROPIC_BASE_URL": os.Getenv("ANTHROPIC_BASE_URL"),
			"NO_PROXY":           os.Getenv("NO_PROXY"),
		}
		b, _ := json.Marshal(out)
		_ = os.WriteFile(os.Getenv("EVERYAPI_TEST_USE_ENV_FILE"), b, 0o600)
		return
	case os.Getenv(useDefaultCallerEnv) == "1":
		args := strings.Split(os.Getenv("EVERYAPI_TEST_USE_ARGS"), ",")
		if err := Use(args); err != nil {
			t.Fatal(err)
		}
		return
	}

	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []any{
			map[string]any{"id": "claude-test", "owned_by": "anthropic", "supported_endpoint_types": []string{"anthropic"}},
		}})
	}))
	defer gateway.Close()

	configRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	if err := config.Save(&config.Credentials{APIBase: gateway.URL, RelayKey: "sk-everyapi-test"}); err != nil {
		t.Fatal(err)
	}
	// Malformed on purpose: LoadFileConfig returns a parse error for this,
	// which startInProcessSanitizer treats as fatal to the sanitizer.
	if err := os.WriteFile(filepath.Join(configRoot, "everyapi", "sanitizer.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	envPath := filepath.Join(t.TempDir(), "env.json")
	shimDir := t.TempDir()
	shim := "#!/bin/sh\n" +
		useDefaultShimEnv + "=1 exec \"$EVERYAPI_TEST_USE_TEST_BINARY\" -test.run=^TestUseKeepsTheCatalogueWhenTheSanitizerFailsToStart$\n"
	if err := os.WriteFile(filepath.Join(shimDir, "claude"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}

	hostEnv := []string{}
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy",
			"ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy":
			continue
		}
		hostEnv = append(hostEnv, kv)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestUseKeepsTheCatalogueWhenTheSanitizerFailsToStart$")
	child.Env = append(hostEnv,
		useDefaultCallerEnv+"=1",
		"EVERYAPI_TEST_USE_ARGS=claude,--transparent=false,--sanitize",
		"EVERYAPI_TEST_USE_ENV_FILE="+envPath,
		"EVERYAPI_TEST_USE_TEST_BINARY="+os.Args[0],
		"XDG_CONFIG_HOME="+configRoot,
		"PATH="+shimDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		// Lowercase here, uppercase in the table test above: x/net/http/httpproxy
		// reads whichever spelling it finds first, so both have to be picked up
		// as the ambient value to extend.
		"no_proxy="+ambientNoProxy,
	)
	out, err := child.CombinedOutput()
	if err != nil {
		t.Fatalf("use must continue after a sanitizer failure, not abort: %v\n%s", err, out)
	}

	raw, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("tool was never launched: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got["ANTHROPIC_BASE_URL"], "http://127.0.0.1:") {
		t.Fatalf("ANTHROPIC_BASE_URL = %q — the catalogue went down with the sanitizer instead of falling back to its own listener", got["ANTHROPIC_BASE_URL"])
	}
	wantNoProxy := "127.0.0.1,localhost," + ambientNoProxy
	if got["NO_PROXY"] != wantNoProxy {
		t.Fatalf("NO_PROXY = %q, want %q — the ambient value was set in its lowercase spelling and must still be extended, not dropped",
			got["NO_PROXY"], wantNoProxy)
	}
}
