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
	} {
		t.Run(tc.name, func(t *testing.T) {
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []any{}})
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
			} else {
				if got["ANTHROPIC_BASE_URL"] != gateway.URL {
					t.Errorf("ANTHROPIC_BASE_URL = %q, want the gateway %q", got["ANTHROPIC_BASE_URL"], gateway.URL)
				}
				if got["ANTHROPIC_AUTH_TOKEN"] != "sk-everyapi-test" {
					t.Errorf("ANTHROPIC_AUTH_TOKEN = %q, want the relay key on the injected path", got["ANTHROPIC_AUTH_TOKEN"])
				}
			}
		})
	}
}
