//go:build !windows

package cmd

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
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
	managedTmuxPrefix       = "everyapi-v3-"
	previousTmuxPrefix      = "everyapi-v2-"
)

func tmuxLaunchArgs(envExecutable, executable, workingDirectory, sessionName, encodedUseArgs, statusSocket, environmentFile, errorFile string) []string {
	return []string{"tmux", "new-session", "-s", sessionName, "-c", workingDirectory, envExecutable, tmuxUseArgsEnv + "=" + encodedUseArgs, tmuxStatusSocketEnv + "=" + statusSocket, tmuxEnvironmentFileEnv + "=" + environmentFile, tmuxErrorFileEnv + "=" + errorFile, tools.TerminalModeEnvironment + "=tmux", tools.TmuxSessionEnvironment + "=" + sessionName, tools.TmuxAttachCommandEnvironment + "=" + tools.TmuxAttachCommand(sessionName), executable, tmuxUseWrapperCommand}
}

func tmuxSessionPrefix(toolName, workingDirectory string) string {
	workspaceIdentity := filepath.Clean(workingDirectory)
	if info, err := os.Stat(workingDirectory); err == nil {
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			workspaceIdentity = fmt.Sprintf("%d:%d", stat.Dev, stat.Ino)
		}
	}
	return tmuxSessionPrefixForIdentity(toolName, workspaceIdentity)
}

func previousTmuxSessionPrefix(toolName, workingDirectory string) string {
	return tmuxSessionPrefixForIdentity(toolName, filepath.Clean(workingDirectory))
}

func tmuxSessionPrefixForIdentity(toolName, workspaceIdentity string) string {
	toolComponent := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return '-'
	}, strings.ToLower(toolName))
	toolComponent = strings.Trim(toolComponent, "-")
	if toolComponent == "" {
		toolComponent = "use"
	}
	workspaceHash := sha256.Sum256([]byte(workspaceIdentity))
	return fmt.Sprintf("%s%s-%x-", managedTmuxPrefix, toolComponent, workspaceHash[:6])
}

func newTmuxSessionName(prefix string) (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate tmux session identity: %w", err)
	}
	return prefix + hex.EncodeToString(nonce), nil
}

type tmuxSessionInventory struct {
	id               string
	allPanesDead     bool
	managedPaneSeen  bool
	managedPaneAlive bool
	managedPaneID    string
}

type tmuxSessionReference struct {
	name   string
	paneID string
}

func legacyEveryAPITmuxSession(name string) bool {
	parts := strings.Split(name, "-")
	if len(parts) != 3 || parts[0] != "everyapi" {
		return false
	}
	pid, pidErr := strconv.ParseUint(parts[1], 10, 64)
	timestamp, timestampErr := strconv.ParseUint(parts[2], 10, 64)
	return pidErr == nil && timestampErr == nil && pid > 0 && timestamp > 0
}

func generatedEveryAPITmuxSession(name string) bool {
	prefix := ""
	version := 0
	switch {
	case strings.HasPrefix(name, managedTmuxPrefix):
		prefix, version = managedTmuxPrefix, 3
	case strings.HasPrefix(name, previousTmuxPrefix):
		prefix, version = previousTmuxPrefix, 2
	default:
		return false
	}
	parts := strings.Split(strings.TrimPrefix(name, prefix), "-")
	suffixParts := 2
	if version == 2 {
		suffixParts = 3
	}
	if len(parts) <= suffixParts {
		return false
	}
	tool := strings.Join(parts[:len(parts)-suffixParts], "-")
	workspaceHash := parts[len(parts)-suffixParts]
	if tool == "" || strings.Trim(tool, "-") != tool || len(workspaceHash) != 12 || !lowerHex(workspaceHash) {
		return false
	}
	for _, r := range tool {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	if version == 3 {
		nonce := parts[len(parts)-1]
		return len(nonce) == 32 && lowerHex(nonce)
	}
	pidText := parts[len(parts)-2]
	timestampText := parts[len(parts)-1]
	pid, pidErr := strconv.ParseUint(pidText, 10, 64)
	timestamp, timestampErr := strconv.ParseUint(timestampText, 10, 64)
	return pidErr == nil && timestampErr == nil && pid > 0 && timestamp > 0
}

func lowerHex(value string) bool {
	for _, r := range value {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func tmuxManagedPaneFormat() string {
	return "#{m:* " + tmuxUseWrapperCommand + ",#{pane_start_command}}"
}

func tmuxAndConditions(conditions ...string) string {
	if len(conditions) == 0 {
		return "1"
	}
	combined := conditions[len(conditions)-1]
	for index := len(conditions) - 2; index >= 0; index-- {
		combined = "#{&&:" + conditions[index] + "," + combined + "}"
	}
	return combined
}

func tmuxDeadCleanupCondition(sessionName string) string {
	// EveryAPI creates exactly one window and one pane. Treat any expanded session as user-owned: tmux has no session-wide all-panes-dead predicate that can be checked atomically with kill-session, so trying to collect a multi-pane session would reopen the live-pane TOCTOU this guard exists to prevent.
	return tmuxAndConditions(
		"#{==:#{session_name},"+sessionName+"}",
		"#{==:#{session_windows},1}",
		"#{==:#{window_panes},1}",
		"#{==:#{pane_dead},1}",
		tmuxManagedPaneFormat(),
	)
}

func tmuxReusableCondition(sessionName string) string {
	return tmuxAndConditions(
		"#{==:#{session_name},"+sessionName+"}",
		tmuxManagedPaneFormat(),
		"#{==:#{pane_dead},0}",
	)
}

func exactTmuxSessionTarget(name string) string {
	return "=" + name
}

func classifyTmuxSessions(output string, targetPrefixes ...string) (tmuxSessionReference, []tmuxSessionReference, error) {
	sessions := make(map[string]tmuxSessionInventory)
	for _, line := range strings.Split(strings.TrimSuffix(output, "\n"), "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 5)
		if len(fields) != 5 || fields[0] == "" || fields[1] == "" || !strings.HasPrefix(fields[2], "%") || fields[3] != "0" && fields[3] != "1" || fields[4] != "0" && fields[4] != "1" {
			return tmuxSessionReference{}, nil, fmt.Errorf("invalid tmux session inventory line %q", line)
		}
		state, exists := sessions[fields[0]]
		if !exists {
			state.id = fields[1]
			state.allPanesDead = true
		} else if state.id != fields[1] {
			return tmuxSessionReference{}, nil, fmt.Errorf("tmux session %q changed identity inside inventory", fields[0])
		}
		paneDead := fields[3] == "1"
		if !paneDead {
			state.allPanesDead = false
		}
		if fields[4] == "1" {
			state.managedPaneSeen = true
			if !paneDead {
				state.managedPaneAlive = true
				state.managedPaneID = fields[2]
			} else if state.managedPaneID == "" {
				state.managedPaneID = fields[2]
			}
		}
		sessions[fields[0]] = state
	}

	var liveMatches []tmuxSessionReference
	var dead []tmuxSessionReference
	for name, state := range sessions {
		managedName := generatedEveryAPITmuxSession(name) || legacyEveryAPITmuxSession(name)
		if !managedName || !state.managedPaneSeen {
			continue
		}
		if state.allPanesDead {
			dead = append(dead, tmuxSessionReference{name: name, paneID: state.managedPaneID})
			continue
		}
		matchesTarget := false
		for _, prefix := range targetPrefixes {
			if prefix != "" && strings.HasPrefix(name, prefix) {
				matchesTarget = true
				break
			}
		}
		if state.managedPaneAlive && matchesTarget {
			liveMatches = append(liveMatches, tmuxSessionReference{name: name, paneID: state.managedPaneID})
		}
	}
	sort.Slice(liveMatches, func(i, j int) bool { return liveMatches[i].name < liveMatches[j].name })
	sort.Slice(dead, func(i, j int) bool { return dead[i].name < dead[j].name })
	if len(liveMatches) == 1 {
		return liveMatches[0], dead, nil
	}
	return tmuxSessionReference{}, dead, nil
}

func reusableTmuxSession(tmuxPath string, targetPrefixes ...string) (tmuxSessionReference, error) {
	output, err := exec.Command(
		tmuxPath,
		"list-panes", "-a",
		"-f", "#{m:everyapi-*,#{session_name}}",
		"-F", "#{session_name}\t#{session_id}\t#{pane_id}\t#{pane_dead}\t"+tmuxManagedPaneFormat(),
	).Output()
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return tmuxSessionReference{}, nil
		}
		return tmuxSessionReference{}, fmt.Errorf("list tmux sessions: %w", err)
	}
	reusable, dead, err := classifyTmuxSessions(string(output), targetPrefixes...)
	if err != nil {
		return tmuxSessionReference{}, err
	}
	for _, session := range dead {
		pruneDeadTmuxSession(tmuxPath, session)
	}
	return reusable, nil
}

func pruneDeadTmuxSession(tmuxPath string, session tmuxSessionReference) {
	target := exactTmuxSessionTarget(session.name)
	_ = exec.Command(
		tmuxPath,
		"if-shell", "-F", "-t", session.paneID,
		tmuxDeadCleanupCondition(session.name),
		"kill-session -t "+target,
	).Run()
}

func attachReusableTmuxSession(tmuxPath string, session tmuxSessionReference) (bool, error) {
	terminationSignals := make(chan os.Signal, 2)
	signal.Notify(terminationSignals, syscall.SIGHUP, syscall.SIGTERM)
	defer signal.Stop(terminationSignals)
	target := exactTmuxSessionTarget(session.name)
	rejectionDirectory, err := os.MkdirTemp("/tmp", "everyapi-tmux-attach-")
	if err != nil {
		return false, fmt.Errorf("create tmux attach state directory: %w", err)
	}
	rejectionMarker := filepath.Join(rejectionDirectory, "rejected")
	defer func() {
		_ = os.Remove(rejectionMarker)
		_ = os.Remove(rejectionDirectory)
	}()
	// The false branch runs synchronously in tmux but reports through this process-private marker. Unlike a tmux global environment variable, it is not inherited by newly created sessions, and it survives a server restart between the rejection and this process reading the result. MkdirTemp's path alphabet is shell-safe and the 0700 parent prevents substitution by another user.
	rejectionCommand := "run-shell 'umask 077; : > " + rejectionMarker + "'"
	command := exec.Command(
		tmuxPath,
		"if-shell", "-F", "-t", session.paneID,
		tmuxReusableCondition(session.name),
		"attach-session -t "+target,
		rejectionCommand,
	)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	var stderr bytes.Buffer
	command.Stderr = &stderr
	select {
	case <-terminationSignals:
		return true, nil
	default:
	}
	if err := command.Start(); err != nil {
		if exec.Command(tmuxPath, "has-session", "-t", target).Run() != nil {
			return false, nil
		}
		return true, fmt.Errorf("attach to tmux session %s: %w", session.name, err)
	}
	result := make(chan error, 1)
	go func() { result <- command.Wait() }()
	var waitErr error
	select {
	case waitErr = <-result:
	case received := <-terminationSignals:
		_ = command.Process.Signal(received)
		return true, nil
	}
	if waitErr == nil {
		_, markerErr := os.Lstat(rejectionMarker)
		if markerErr == nil {
			return false, nil
		}
		if !errors.Is(markerErr, os.ErrNotExist) {
			return true, fmt.Errorf("inspect tmux attach rejection state: %w", markerErr)
		}
		_, _ = io.Copy(os.Stderr, &stderr)
		return true, nil
	}
	_, _ = io.Copy(os.Stderr, &stderr)
	if exec.Command(tmuxPath, "has-session", "-t", target).Run() != nil {
		return false, nil
	}
	return true, fmt.Errorf("attach to tmux session %s: %w", session.name, waitErr)
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
	for _, name := range []string{"TERM", "TMUX", "TMUX_PANE", tmuxErrorFileEnv, tools.TerminalModeEnvironment, tools.TmuxSessionEnvironment, tools.TmuxAttachCommandEnvironment} {
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

// tmuxSessionEndedNotice is what the outer process says once the wrapper has
// reported the tool's status, or "" when nothing needs saying. A non-zero
// status is the one case worth a word: the tool is gone, so the reattach hint
// printed at launch can now only answer "can't find session", and whatever the
// tool printed as it failed went to a pane tmux has already destroyed. Name the
// status and point at the terminal mode that would have shown the message. A
// clean exit stays silent — that is just the user quitting their tool.
// readTmuxFatalError returns what the process inside the session recorded as its
// fatal error, or "" when it recorded nothing. Sanitized because a relayed
// backend message can carry attacker-chosen escape sequences, and this text is
// about to be printed to a real terminal.
func readTmuxFatalError(errorFile string) string {
	if errorFile == "" {
		return ""
	}
	data, err := os.ReadFile(errorFile)
	if err != nil {
		return ""
	}
	if len(data) > tmuxFatalErrorLimit {
		data = data[:tmuxFatalErrorLimit]
	}
	return cliout.Sanitize(strings.TrimSpace(string(data)))
}

func tmuxSessionEndedNotice(sessionName string, exitCode int) string {
	if exitCode == 0 {
		return ""
	}
	// commandExitCode maps a signalled child to 128+signo, so Ctrl-C arrives as
	// 130 and a targeted kill as 143. In those cases the tool did not fail —
	// somebody ended it, and watched it end in the pane. Telling them to switch
	// terminal modes "to see failures" would be advice for a failure that never
	// happened. A tool that genuinely exits in this range loses only a hint.
	if exitCode > 128 && exitCode <= 128+31 {
		return ""
	}
	return fmt.Sprintf(i18n.T("use.tmux_session_ended"), sessionName, exitCode)
}

// exitAfterTmuxSession ends the outer process with the tool's status, having
// first said whatever the session could not. When the process inside recorded a
// fatal error, that message IS the answer — print it and skip the generic notice,
// which exists only for the case where nothing was recorded.
func exitAfterTmuxSessionWithMessage(sessionName string, exitCode int, message string) {
	if exitCode != 0 {
		if message != "" {
			fmt.Fprintf(os.Stderr, "%s: %s\n", i18n.T("common.error_prefix"), message)
			os.Exit(exitCode)
		}
	}
	if notice := tmuxSessionEndedNotice(sessionName, exitCode); notice != "" {
		fmt.Fprintln(os.Stderr, notice)
	}
	os.Exit(exitCode)
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
	canonicalWorkingDirectory, canonicalErr := filepath.EvalSymlinks(workingDirectory)
	if canonicalErr == nil {
		workingDirectory = canonicalWorkingDirectory
	}
	toolName, _, _, _, _, _, _, _, parseErr := parseUseArgsWithTransparent(useArgs)
	if parseErr != nil {
		return parseErr
	}
	sessionPrefix := tmuxSessionPrefix(toolName, workingDirectory)
	var targetPrefixes []string
	if shouldReuseTmuxSession(useArgs) {
		targetPrefixes = []string{sessionPrefix, previousTmuxSessionPrefix(toolName, workingDirectory)}
	}
	reusableSession, err := reusableTmuxSession(tmuxPath, targetPrefixes...)
	if err != nil {
		return err
	}
	if reusableSession.name != "" {
		cliout.Printf(i18n.T("use.tmux_launching")+"\n", reusableSession.name, reusableSession.name)
		attached, attachErr := attachReusableTmuxSession(tmuxPath, reusableSession)
		if attachErr != nil {
			return attachErr
		}
		if attached {
			// The original outer launch already crossed the detach boundary and exited successfully while its wrapper kept running. This invocation is a tmux client reattachment, so its completion has the same terminal-client semantics: it does not invent a second persistent channel for the old tool's exit status.
			os.Exit(0)
		}
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
	errorFile := filepath.Join(statusDirectory, tmuxErrorFileName)
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
		// Removed last, and only here: every exit path that prints the recorded
		// error has already read it by the time cleanup runs. A detach leaves the
		// directory in place along with the still-running session.
		_ = os.Remove(errorFile)
		_ = os.Remove(statusDirectory)
	}
	defer cleanup(true)

	sessionName, err := newTmuxSessionName(sessionPrefix)
	if err != nil {
		return err
	}
	cliout.Printf(i18n.T("use.tmux_launching")+"\n", sessionName, sessionName)
	tmuxCommand := exec.Command(tmuxPath)
	tmuxCommand.Args = tmuxLaunchArgs(envExecutable, executable, workingDirectory, sessionName, encodedUseArgs, statusSocket, environmentFile, errorFile)
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
			message := readTmuxFatalError(errorFile)
			cleanup(true)
			exitAfterTmuxSessionWithMessage(sessionName, status.exitCode, message)
		case runErr := <-tmuxResult:
			// Status can race with the tmux client shutdown. Consume an already-delivered result before treating a still-live session as an intentional detach.
			select {
			case status := <-statusResult:
				if status.err != nil {
					return fmt.Errorf("wait for tmux session %s: %w", sessionName, status.err)
				}
				message := readTmuxFatalError(errorFile)
				cleanup(true)
				exitAfterTmuxSessionWithMessage(sessionName, status.exitCode, message)
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
			message := readTmuxFatalError(errorFile)
			cleanup(true)
			exitAfterTmuxSessionWithMessage(sessionName, status.exitCode, message)
		case received := <-terminationSignals:
			// Closing the host terminal is equivalent to detaching from the persistent session. Signal only the tmux client, clean the private IPC path explicitly because os.Exit skips defers, and leave the server-side session running.
			_ = tmuxCommand.Process.Signal(received)
			cleanup(false)
			os.Exit(0)
		}
	}
}
