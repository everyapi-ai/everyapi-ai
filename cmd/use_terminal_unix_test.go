//go:build !windows

package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/tools"
)

func TestTmuxLaunchArgsPreserveUseArgumentsWithoutShellJoining(t *testing.T) {
	useArgs := []string{"claude", "--", "--model", "model with spaces", ";", "touch /tmp/nope"}
	payload, err := encodeTmuxUseArgs(useArgs)
	if err != nil {
		t.Fatal(err)
	}
	got := tmuxLaunchArgs(
		"/usr/bin/env",
		"/Applications/Every API/everyapi",
		"/tmp/project with spaces",
		"everyapi-123-456",
		payload,
		"/tmp/everyapi-status.sock",
		"/tmp/everyapi-environment.json",
	)
	want := []string{
		"tmux", "new-session", "-s", "everyapi-123-456", "-c", "/tmp/project with spaces",
		"/usr/bin/env", tmuxUseArgsEnv + "=" + payload, tmuxStatusSocketEnv + "=/tmp/everyapi-status.sock", tmuxEnvironmentFileEnv + "=/tmp/everyapi-environment.json", tools.TerminalModeEnvironment + "=tmux", tools.TmuxSessionEnvironment + "=everyapi-123-456", tools.TmuxAttachCommandEnvironment + "=tmux attach -t everyapi-123-456", "/Applications/Every API/everyapi", tmuxUseWrapperCommand,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tmux args = %#v, want %#v", got, want)
	}
	decoded, err := decodeTmuxUseArgs(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, useArgs) {
		t.Fatalf("decoded args = %#v, want %#v", decoded, useArgs)
	}
}

func TestTmuxEnvironmentFileRoundTripAndChildOverlay(t *testing.T) {
	statusDirectory, err := os.MkdirTemp("/tmp", "everyapi-tmux-test-")
	if err != nil {
		t.Fatal(err)
	}
	statusSocket := filepath.Join(statusDirectory, tmuxStatusSocketName)
	environmentFile := filepath.Join(statusDirectory, tmuxEnvironmentFileName)
	t.Cleanup(func() {
		_ = os.Remove(environmentFile)
		_ = os.Remove(statusSocket)
		_ = os.Remove(statusDirectory)
	})
	outerEnvironment := []string{"PATH=/outer/bin", "TERM=xterm-256color", "XDG_CONFIG_HOME=/tmp/config"}
	if err := writeTmuxEnvironment(environmentFile, outerEnvironment); err != nil {
		t.Fatal(err)
	}
	loaded, runtimeDirectory, err := loadTmuxEnvironment(environmentFile, statusSocket)
	if err != nil {
		t.Fatal(err)
	}
	if runtimeDirectory != statusDirectory {
		t.Fatalf("runtime directory = %q, want %q", runtimeDirectory, statusDirectory)
	}
	if !reflect.DeepEqual(loaded, outerEnvironment) {
		t.Fatalf("loaded environment = %#v, want %#v", loaded, outerEnvironment)
	}
	if _, err := os.Stat(environmentFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("environment file remained after consumption: %v", err)
	}
	childEnvironment := tmuxChildEnvironment(loaded, []string{"TMUX=/tmp/tmux,1,0", "TMUX_PANE=%4", "TERM=tmux-256color", tools.TerminalModeEnvironment + "=tmux", tools.TmuxSessionEnvironment + "=everyapi-123-456", tools.TmuxAttachCommandEnvironment + "=tmux attach -t everyapi-123-456"}, "payload", statusSocket)
	wantValues := map[string]string{
		"PATH":                             "/outer/bin",
		"XDG_CONFIG_HOME":                  "/tmp/config",
		"TERM":                             "tmux-256color",
		"TMUX":                             "/tmp/tmux,1,0",
		"TMUX_PANE":                        "%4",
		tmuxUseArgsEnv:                     "payload",
		tmuxStatusSocketEnv:                statusSocket,
		tools.TerminalModeEnvironment:      "tmux",
		tools.TmuxSessionEnvironment:       "everyapi-123-456",
		tools.TmuxAttachCommandEnvironment: "tmux attach -t everyapi-123-456",
	}
	gotValues := make(map[string]string)
	for _, entry := range childEnvironment {
		parts := strings.SplitN(entry, "=", 2)
		gotValues[parts[0]] = parts[1]
	}
	for key, want := range wantValues {
		if gotValues[key] != want {
			t.Errorf("child environment %s = %q, want %q", key, gotValues[key], want)
		}
	}
	if _, exists := gotValues[tmuxEnvironmentFileEnv]; exists {
		t.Fatalf("%s leaked into the inner EveryAPI environment", tmuxEnvironmentFileEnv)
	}
}

func TestRunTmuxChildAndReportPreservesExitCode(t *testing.T) {
	statusDirectory, err := os.MkdirTemp("/tmp", "everyapi-tmux-test-")
	if err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(statusDirectory, "status.sock")
	t.Cleanup(func() {
		_ = os.Remove(socketPath)
		_ = os.Remove(statusDirectory)
	})
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	reported := make(chan int, 1)
	acceptErr := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			acceptErr <- err
			return
		}
		defer connection.Close()
		data, err := io.ReadAll(io.LimitReader(connection, 16))
		if err != nil {
			acceptErr <- err
			return
		}
		code, err := strconv.Atoi(string(data))
		if err != nil {
			acceptErr <- err
			return
		}
		reported <- code
	}()

	code, err := runTmuxChildAndReport(exec.Command("sh", "-c", "exit 7"), socketPath)
	if err != nil {
		t.Fatal(err)
	}
	if code != 7 {
		t.Fatalf("child exit code = %d, want 7", code)
	}
	select {
	case got := <-reported:
		if got != 7 {
			t.Fatalf("reported exit code = %d, want 7", got)
		}
	case err := <-acceptErr:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for tmux child exit status")
	}
}

func TestCommandExitCodeMapsSignalsLikeAShell(t *testing.T) {
	err := exec.Command("sh", "-c", "kill -TERM $$").Run()
	if got := commandExitCode(err); got != 143 {
		t.Fatalf("SIGTERM exit code = %d, want 143", got)
	}
}

func TestRunTmuxChildAndReportSurvivesForegroundGroupInterrupt(t *testing.T) {
	const helperEnv = "EVERYAPI_TMUX_SIGNAL_HELPER"
	if os.Getenv(helperEnv) == "1" {
		child := exec.Command("sh", "-c", "trap 'exit 7' INT; echo ready; while :; do sleep 1; done")
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		exitCode, err := runTmuxChildAndReport(child, os.Getenv(tmuxStatusSocketEnv))
		if err != nil {
			os.Exit(98)
		}
		os.Exit(exitCode)
	}

	statusDirectory, err := os.MkdirTemp("/tmp", "everyapi-tmux-signal-test-")
	if err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(statusDirectory, "status.sock")
	t.Cleanup(func() {
		_ = os.Remove(socketPath)
		_ = os.Remove(statusDirectory)
	})
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	helper := exec.Command(os.Args[0], "-test.run=^TestRunTmuxChildAndReportSurvivesForegroundGroupInterrupt$")
	helper.Env = append(os.Environ(), helperEnv+"=1", tmuxStatusSocketEnv+"="+socketPath)
	helper.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := helper.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	helper.Stderr = os.Stderr
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	ready := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(stdout).ReadString('\n')
		if err == nil && line != "ready\n" {
			err = fmt.Errorf("helper readiness line = %q", line)
		}
		ready <- err
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for signal helper")
	}
	if err := syscall.Kill(-helper.Process.Pid, syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	waitErr := helper.Wait()
	var exitError *exec.ExitError
	if !errors.As(waitErr, &exitError) || exitError.ExitCode() != 7 {
		t.Fatalf("helper wait error = %v, want exit code 7", waitErr)
	}
	if err := listener.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	connection, err := listener.Accept()
	if err != nil {
		t.Fatalf("wrapper did not report after SIGINT: %v", err)
	}
	defer connection.Close()
	data, err := io.ReadAll(io.LimitReader(connection, 16))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "7" {
		t.Fatalf("reported exit code = %q, want 7", data)
	}
}

func TestShouldRelaunchInTmuxDoesNotNest(t *testing.T) {
	if !shouldRelaunchInTmux("tmux", "") {
		t.Fatal("tmux mode outside tmux did not request a relaunch")
	}
	if shouldRelaunchInTmux("tmux", "/private/tmp/tmux-501/default,1,0") {
		t.Fatal("tmux mode inside tmux requested a nested session")
	}
	if shouldRelaunchInTmux("native", "") {
		t.Fatal("native mode requested a tmux relaunch")
	}
}

func TestApplyTerminalModeAdoptsExistingTmuxSession(t *testing.T) {
	binDir := t.TempDir()
	tmuxPath := filepath.Join(binDir, "tmux")
	script := "#!/bin/sh\nif [ \"$1\" = display-message ] && [ \"$2\" = -p ] && [ \"$3\" = '#S' ]; then\n  printf \"existing user's session\\n\"\n  exit 0\nfi\nexit 2\n"
	if err := os.WriteFile(tmuxPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("TMUX", "/tmp/tmux-501/default,1,0")
	if err := applyTerminalMode("tmux", []string{"claude"}); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(tools.TerminalModeEnvironment); got != "tmux" {
		t.Fatalf("%s = %q, want tmux", tools.TerminalModeEnvironment, got)
	}
	if got := os.Getenv(tools.TmuxSessionEnvironment); got != "existing user's session" {
		t.Fatalf("%s = %q", tools.TmuxSessionEnvironment, got)
	}
	if got := os.Getenv(tools.TmuxAttachCommandEnvironment); got != `tmux attach -t 'existing user'"'"'s session'` {
		t.Fatalf("%s = %q", tools.TmuxAttachCommandEnvironment, got)
	}
	if instructions := tools.TmuxAgentInstructions(); !strings.Contains(instructions, "existing user's session") {
		t.Fatalf("agent instructions = %q", instructions)
	}
}

func TestRelaunchUseInTmuxReportsMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := relaunchUseInTmux([]string{"claude"})
	if err == nil || !strings.Contains(err.Error(), "tmux is not installed") {
		t.Fatalf("error = %v, want missing-tmux guidance", err)
	}
}
