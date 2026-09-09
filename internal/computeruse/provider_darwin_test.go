//go:build darwin

package computeruse

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestHumanTypingTimeoutAllowsCharacterDelays(t *testing.T) {
	for _, kind := range []ActionKind{ActionSetValue, ActionTypeText} {
		if got := darwinInputTimeout(kind, strings.Repeat("水", 200), nil); got < 60*time.Second {
			t.Fatalf("typing deadline too short: %s", got)
		}
	}
	if got := darwinInputTimeout(ActionClick, "", nil); got != darwinActionTimeout {
		t.Fatalf("click deadline changed: %s", got)
	}
}

func TestHumanClickTimeoutAllowsPacedMultiClick(t *testing.T) {
	count := 100
	if got := darwinInputTimeout(ActionClick, "", &count); got < darwinActionTimeout+20*time.Second {
		t.Fatalf("paced multi-click deadline too short: %s", got)
	}
	count = 1000000
	if got := darwinInputTimeout(ActionClick, "", &count); got > darwinActionTimeout+20*time.Second {
		t.Fatalf("invalid count increased the deadline without bounds: %s", got)
	}
}

func TestCapabilitiesWithUnresponsiveHelperRemainBoundedAndUnknown(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "cursor-cap.")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	provider := &darwinProvider{stateDir: dir}
	if err := os.WriteFile(provider.tokenPath(), []byte("fixture-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", provider.socketPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Never reply; wait for the querying client to close at its deadline.
		_, _ = io.Copy(io.Discard, conn)
	}()
	result := make(chan Capabilities, 1)
	go func() { capabilities, _ := provider.Capabilities(context.Background()); result <- capabilities }()
	select {
	case capabilities := <-result:
		if capabilities.IndependentPointer != nil || !capabilities.Supports.Apps.List {
			t.Fatal("unresponsive helper must preserve static support and unknown runtime support")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("capability query did not respect its bounded deadline")
	}
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("capability query leaked its connection")
	}
}

func TestCapabilitiesReportLivePointerSupport(t *testing.T) {
	for _, value := range []string{"true", "false", "null", "omitted", "invalid"} {
		t.Run(value, func(t *testing.T) {
			// A short path is required by Unix-domain socket path limits.
			dir, err := os.MkdirTemp("/tmp", "cursor-cap.")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			provider := &darwinProvider{stateDir: dir}
			if err := os.WriteFile(provider.tokenPath(), []byte("fixture-token"), 0o600); err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("unix", provider.socketPath())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			requestSeen := make(chan darwinRPCRequest, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				line, _ := bufio.NewReader(conn).ReadBytes('\n')
				var request darwinRPCRequest
				if json.Unmarshal(line, &request) != nil {
					return
				}
				requestSeen <- request
				payload := `{"ok":true,"result":{"independentPointer":` + value + `}}` + "\n"
				if value == "omitted" {
					payload = "{\"ok\":true,\"result\":{}}\n"
				} else if value == "invalid" {
					payload = "{\"ok\":true,\"result\":{\"independentPointer\":\"true\"}}\n"
				}
				_, _ = conn.Write([]byte(payload))
			}()
			capabilities, err := provider.Capabilities(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(capabilities)
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(encoded, &fields)
			want := value
			if value == "omitted" || value == "invalid" {
				want = "null"
			}
			if string(fields["independentPointer"]) != want {
				t.Fatalf("runtime pointer support = %s, want %s", fields["independentPointer"], want)
			}
			select {
			case request := <-requestSeen:
				if request.Method != "capabilities" || request.Token != "fixture-token" {
					t.Fatal("wrong capability RPC")
				}
			default:
				t.Fatal("capabilities never queried the running helper")
			}
		})
	}
}

func TestCapabilitiesWithoutHelperRemainUnknownAndDoNotInstall(t *testing.T) {
	provider := &darwinProvider{stateDir: filepath.Join(t.TempDir(), "not-created")}
	capabilities, err := provider.Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(capabilities)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(encoded, &fields)
	if string(fields["independentPointer"]) != "null" {
		t.Fatalf("expected unknown runtime support: %s", encoded)
	}
	if _, err := os.Stat(provider.stateDir); !os.IsNotExist(err) {
		t.Fatalf("capabilities created helper state: %v", err)
	}
}

func writeProtocolProbe(t *testing.T, output string, exitCode int) string {
	t.Helper()
	app := filepath.Join(t.TempDir(), darwinHelperAppName)
	executable := filepath.Join(app, "Contents", "MacOS", darwinHelperExecutable)
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nprintf '%s\\n' '" + output + "'\nexit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(executable, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return app
}

func TestHelperSupportsCurrentProtocol(t *testing.T) {
	ctx := context.Background()
	if !helperSupportsProtocol(ctx, writeProtocolProbe(t, "2", 0)) {
		t.Fatal("current helper protocol was rejected")
	}
	if helperSupportsProtocol(ctx, writeProtocolProbe(t, "1", 0)) {
		t.Fatal("old helper protocol was accepted")
	}
	if helperSupportsProtocol(ctx, writeProtocolProbe(t, "2", 2)) {
		t.Fatal("failing helper probe was accepted")
	}
}
