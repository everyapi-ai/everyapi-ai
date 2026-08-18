//go:build !windows

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/tools"
)

const (
	tmuxStatusSocketName    = "status.sock"
	tmuxEnvironmentFileName = "environment.json"
)

func tmuxLaunchArgs(envExecutable, executable, workingDirectory, sessionName, encodedUseArgs, statusSocket, environmentFile string) []string {
	return []string{"tmux", "new-session", "-s", sessionName, "-c", workingDirectory, envExecutable, tmuxUseArgsEnv + "=" + encodedUseArgs, tmuxStatusSocketEnv + "=" + statusSocket, tmuxEnvironmentFileEnv + "=" + environmentFile, tools.TerminalModeEnvironment + "=tmux", tools.TmuxSessionEnvironment + "=" + sessionName, tools.TmuxAttachCommandEnvironment + "=" + tools.TmuxAttachCommand(sessionName), executable, tmuxUseWrapperCommand}
}

func tmuxAvailable() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

func adoptCurrentTmuxContext() error {
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		return errors.New(i18n.T("use.tmux_not_found"))
	}
	output, err := exec.Command(tmuxPath, "display-message", "-p", "#S").Output()
	if err != nil {
		return fmt.Errorf("resolve current tmux session: %w", err)
	}
	session := strings.TrimRight(string(output), "\r\n")
	if session == "" {
		return errors.New("resolve current tmux session: tmux returned an empty name")
	}
	for name, value := range map[string]string{
		tools.TerminalModeEnvironment:      "tmux",
		tools.TmuxSessionEnvironment:       session,
		tools.TmuxAttachCommandEnvironment: tools.TmuxAttachCommand(session),
	} {
		if err := os.Setenv(name, value); err != nil {
			return fmt.Errorf("set %s: %w", name, err)
		}
	}
	return nil
}

func commandExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		if waitStatus, ok := exitError.Sys().(syscall.WaitStatus); ok && waitStatus.Signaled() {
			return 128 + int(waitStatus.Signal())
		}
		if exitError.ExitCode() >= 0 {
			return exitError.ExitCode()
		}
	}
	return 1
}

func reportTmuxExitStatus(statusSocket string, exitCode int) error {
	connection, err := net.DialTimeout("unix", statusSocket, 2*time.Second)
	if err != nil {
		return fmt.Errorf("connect to tmux status socket: %w", err)
	}
	defer connection.Close()
	if _, err := io.WriteString(connection, strconv.Itoa(exitCode)); err != nil {
		return fmt.Errorf("report tmux child exit status: %w", err)
	}
	return nil
}

func writeTmuxEnvironment(path string, environment []string) error {
	data, err := json.Marshal(environment)
	if err != nil {
		return fmt.Errorf("encode tmux environment: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create tmux environment file: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("write tmux environment file: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("close tmux environment file: %w", err)
	}
	return nil
}

func validateTmuxRuntimePaths(environmentFile, statusSocket string) (string, error) {
	runtimeDirectory := filepath.Dir(statusSocket)
	if filepath.Dir(runtimeDirectory) != "/tmp" || !strings.HasPrefix(filepath.Base(runtimeDirectory), "everyapi-tmux-") || statusSocket != filepath.Join(runtimeDirectory, tmuxStatusSocketName) || environmentFile != filepath.Join(runtimeDirectory, tmuxEnvironmentFileName) {
		return "", errors.New("invalid tmux runtime paths")
	}
	info, err := os.Lstat(runtimeDirectory)
	if err != nil {
		return "", fmt.Errorf("inspect tmux runtime directory: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm() != 0o700 || !ok || int(stat.Uid) != os.Getuid() {
		return "", errors.New("tmux runtime directory is not private")
	}
	return runtimeDirectory, nil
}

func loadTmuxEnvironment(environmentFile, statusSocket string) ([]string, string, error) {
	runtimeDirectory, err := validateTmuxRuntimePaths(environmentFile, statusSocket)
	if err != nil {
		return nil, "", err
	}
	info, err := os.Lstat(environmentFile)
	if err != nil {
		return nil, "", fmt.Errorf("inspect tmux environment file: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || !ok || int(stat.Uid) != os.Getuid() {
		return nil, "", errors.New("tmux environment file is not private")
	}
	data, err := os.ReadFile(environmentFile)
	_ = os.Remove(environmentFile)
	if err != nil {
		return nil, "", fmt.Errorf("read tmux environment file: %w", err)
	}
	var environment []string
	if err := json.Unmarshal(data, &environment); err != nil {
		return nil, "", fmt.Errorf("parse tmux environment file: %w", err)
	}
	for _, entry := range environment {
		name, _, found := strings.Cut(entry, "=")
		if !found || name == "" {
			return nil, "", fmt.Errorf("invalid tmux environment entry %q", entry)
		}
	}
	return environment, runtimeDirectory, nil
}

func setEnvironmentValue(environment []string, name, value string) []string {
	prefix := name + "="
	filtered := environment[:0]
	for _, entry := range environment {
		if !strings.HasPrefix(entry, prefix) {
			filtered = append(filtered, entry)
		}
	}
	return append(filtered, prefix+value)
}

func removeEnvironmentValue(environment []string, name string) []string {
	prefix := name + "="
	filtered := environment[:0]
	for _, entry := range environment {
		if !strings.HasPrefix(entry, prefix) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func environmentValue(environment []string, name string) (string, bool) {
	prefix := name + "="
	for _, entry := range environment {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix), true
		}
	}
	return "", false
}

func tmuxChildEnvironment(outerEnvironment, wrapperEnvironment []string, payload, statusSocket string) []string {
	childEnvironment := append([]string(nil), outerEnvironment...)
	childEnvironment = setEnvironmentValue(childEnvironment, tmuxUseArgsEnv, payload)
	childEnvironment = setEnvironmentValue(childEnvironment, tmuxStatusSocketEnv, statusSocket)
	childEnvironment = removeEnvironmentValue(childEnvironment, tmuxEnvironmentFileEnv)
	for _, name := range []string{"TERM", "TMUX", "TMUX_PANE", tools.TerminalModeEnvironment, tools.TmuxSessionEnvironment, tools.TmuxAttachCommandEnvironment} {
		if value, ok := environmentValue(wrapperEnvironment, name); ok {
			childEnvironment = setEnvironmentValue(childEnvironment, name, value)
		}
	}
	return childEnvironment
}

func runTmuxChildAndReport(child *exec.Cmd, statusSocket string) (int, error) {
	// The wrapper and inner EveryAPI process share tmux's foreground process group, so terminal-generated signals already reach the child. Catch them here only to keep the wrapper alive long enough to reap and report; forward targeted SIGTERM because it is not generated by the terminal.
	signalChannel := make(chan os.Signal, 8)
	signal.Notify(signalChannel, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGHUP)
	if err := child.Start(); err != nil {
		signal.Stop(signalChannel)
		close(signalChannel)
		return 1, reportTmuxExitStatus(statusSocket, 1)
	}
	go func() {
		for received := range signalChannel {
			if received == syscall.SIGTERM {
				_ = child.Process.Signal(received)
			}
		}
	}()
	waitErr := child.Wait()
	signal.Stop(signalChannel)
	close(signalChannel)
	exitCode := commandExitCode(waitErr)
	return exitCode, reportTmuxExitStatus(statusSocket, exitCode)
}

func RunTmuxUseWrapper() (int, error) {
	payload := os.Getenv(tmuxUseArgsEnv)
	if payload == "" {
		return 1, errors.New("tmux use payload is missing")
	}
	statusSocket := os.Getenv(tmuxStatusSocketEnv)
	if statusSocket == "" {
		return 1, errors.New("tmux status socket is missing")
	}
	environmentFile := os.Getenv(tmuxEnvironmentFileEnv)
	if environmentFile == "" {
		return 1, errors.New("tmux environment file is missing")
	}
	outerEnvironment, runtimeDirectory, err := loadTmuxEnvironment(environmentFile, statusSocket)
	if err != nil {
		return 1, err
	}
	defer func() {
		_ = os.Remove(statusSocket)
		_ = os.Remove(runtimeDirectory)
	}()
	executable, err := os.Executable()
	if err != nil {
		return 1, fmt.Errorf("resolve everyapi executable for tmux wrapper: %w", err)
	}
	child := exec.Command(executable, "use", tmuxReentryArg)
	child.Stdin = os.Stdin
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	child.Env = tmuxChildEnvironment(outerEnvironment, os.Environ(), payload, statusSocket)
	exitCode, _ := runTmuxChildAndReport(child, statusSocket)
	return exitCode, nil
}

func readTmuxExitStatus(listener *net.UnixListener) (int, error) {
	connection, err := listener.Accept()
	if err != nil {
		return 0, fmt.Errorf("accept tmux child exit status: %w", err)
	}
	defer connection.Close()
	data, err := io.ReadAll(io.LimitReader(connection, 16))
	if err != nil {
		return 0, fmt.Errorf("read tmux child exit status: %w", err)
	}
	exitCode, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || exitCode < 0 || exitCode > 255 {
		return 0, fmt.Errorf("invalid tmux child exit status %q", data)
	}
	return exitCode, nil
}

type tmuxExitStatusResult struct {
	exitCode int
	err      error
}

func relaunchUseInTmux(useArgs []string) error {
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		return errors.New(i18n.T("use.tmux_not_found"))
	}
	envExecutable, err := exec.LookPath("env")
	if err != nil {
		return fmt.Errorf("resolve env executable for tmux: %w", err)
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve everyapi executable for tmux: %w", err)
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve working directory for tmux: %w", err)
	}
	encodedUseArgs, err := encodeTmuxUseArgs(useArgs)
	if err != nil {
		return err
	}
	terminationSignals := make(chan os.Signal, 2)
	signal.Notify(terminationSignals, syscall.SIGHUP, syscall.SIGTERM)
	defer signal.Stop(terminationSignals)
	statusDirectory, err := os.MkdirTemp("/tmp", "everyapi-tmux-")
	if err != nil {
		return fmt.Errorf("create tmux status directory: %w", err)
	}
	statusSocket := filepath.Join(statusDirectory, tmuxStatusSocketName)
	environmentFile := filepath.Join(statusDirectory, tmuxEnvironmentFileName)
	if err := writeTmuxEnvironment(environmentFile, os.Environ()); err != nil {
		_ = os.Remove(statusDirectory)
		return err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: statusSocket, Net: "unix"})
	if err != nil {
		_ = os.Remove(environmentFile)
		_ = os.Remove(statusDirectory)
		return fmt.Errorf("listen for tmux child exit status: %w", err)
	}
	cleanup := func(removeEnvironmentFile bool) {
		_ = listener.Close()
		if removeEnvironmentFile {
			_ = os.Remove(environmentFile)
		}
		_ = os.Remove(statusSocket)
		_ = os.Remove(statusDirectory)
	}
	defer cleanup(true)

	sessionName := fmt.Sprintf("everyapi-%d-%d", os.Getpid(), time.Now().UnixMilli())
	cliout.Printf(i18n.T("use.tmux_launching")+"\n", sessionName, sessionName)
	tmuxCommand := exec.Command(tmuxPath)
	tmuxCommand.Args = tmuxLaunchArgs(envExecutable, executable, workingDirectory, sessionName, encodedUseArgs, statusSocket, environmentFile)
	tmuxCommand.Stdin = os.Stdin
	tmuxCommand.Stdout = os.Stdout
	tmuxCommand.Stderr = os.Stderr
	tmuxCommand.Env = os.Environ()
	statusResult := make(chan tmuxExitStatusResult, 1)
	go func() {
		exitCode, err := readTmuxExitStatus(listener)
		statusResult <- tmuxExitStatusResult{exitCode: exitCode, err: err}
	}()
	if err := tmuxCommand.Start(); err != nil {
		return fmt.Errorf("start tmux session %s: %w", sessionName, err)
	}
	tmuxResult := make(chan error, 1)
	go func() { tmuxResult <- tmuxCommand.Wait() }()

	for {
		select {
		case status := <-statusResult:
			if status.err != nil {
				return fmt.Errorf("wait for tmux session %s: %w", sessionName, status.err)
			}
			// A user may enable remain-on-exit or open another window in this uniquely named session. The reported tool status is authoritative; detach the client so those session-level choices cannot hold the outer EveryAPI process open or overwrite the real exit code.
			_ = exec.Command(tmuxPath, "detach-client", "-s", sessionName).Run()
			_ = tmuxCommand.Process.Signal(syscall.SIGHUP)
			<-tmuxResult
			cleanup(true)
			os.Exit(status.exitCode)
		case runErr := <-tmuxResult:
			// Status can race with the tmux client shutdown. Consume an already-delivered result before treating a still-live session as an intentional detach.
			select {
			case status := <-statusResult:
				if status.err != nil {
					return fmt.Errorf("wait for tmux session %s: %w", sessionName, status.err)
				}
				cleanup(true)
				os.Exit(status.exitCode)
			default:
			}
			if exec.Command(tmuxPath, "has-session", "-t", sessionName).Run() == nil {
				cleanup(false)
				os.Exit(0)
			}
			// When the session is gone, the wrapper necessarily finished first and its local socket message is the remaining causal completion signal. The deadline only bounds a broken IPC path; normal completion does not wait on time.
			if err := listener.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
				return fmt.Errorf("set tmux status deadline: %w", err)
			}
			status := <-statusResult
			if status.err != nil {
				if runErr != nil {
					return fmt.Errorf("start tmux session %s: %w", sessionName, runErr)
				}
				return fmt.Errorf("wait for tmux session %s: %w", sessionName, status.err)
			}
			cleanup(true)
			os.Exit(status.exitCode)
		case received := <-terminationSignals:
			// Closing the host terminal is equivalent to detaching from the persistent session. Signal only the tmux client, clean the private IPC path explicitly because os.Exit skips defers, and leave the server-side session running.
			_ = tmuxCommand.Process.Signal(received)
			cleanup(false)
			os.Exit(0)
		}
	}
}
