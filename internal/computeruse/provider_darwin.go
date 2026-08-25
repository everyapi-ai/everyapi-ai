//go:build darwin

package computeruse

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The native helper (clients/desktop/native/computer-use-macos) replaces the
// osascript/JXA implementation this file used to hold. It is a standalone,
// independently code-signed .app so macOS attributes the Accessibility grant
// to "EveryAPI Computer Use" — not to the shared system /usr/bin/osascript,
// which would otherwise grant every AppleScript on the machine the same
// power once approved. See specs/2026-08-20-computer-use-design.md.
const (
	darwinHelperAppName        = "EveryAPI Computer Use.app"
	darwinHelperExecutable     = "everyapi-computer-use-macos"
	darwinHelperReleaseAsset   = "everyapi-computer-use-macos.zip"
	darwinHelperReleaseBaseURL = "https://github.com/everyapi-ai/everyapi-ai/releases/latest/download/"
	darwinConnectResourcePath  = "/Applications/EveryAPI Connect.app/Contents/Resources/" + darwinHelperAppName
	darwinDialTimeout          = 3 * time.Second
	darwinLaunchWait           = 10 * time.Second
	darwinLaunchPoll           = 100 * time.Millisecond
	darwinDownloadTimeout      = 60 * time.Second
	darwinStateTimeout         = 25 * time.Second
	darwinActionTimeout        = 15 * time.Second
	darwinMaxWalk              = 600
	darwinMaxShown             = 150
	darwinWalkBudgetMS         = 10_000
	darwinMaxDepth             = 20
)

type darwinProvider struct {
	stateDir string
	mu       sync.Mutex
}

func newPlatformProvider(_ string) (Provider, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, NewError(CodeInternal, "resolve home directory", err)
	}
	stateDir := filepath.Join(home, "Library", "Application Support", "everyapi", "computer-use")
	return &darwinProvider{stateDir: stateDir}, nil
}

func (p *darwinProvider) socketPath() string { return filepath.Join(p.stateDir, "helper-v2.sock") }
func (p *darwinProvider) tokenPath() string  { return filepath.Join(p.stateDir, "helper-v2.token") }
func (p *darwinProvider) managedAppPath() string {
	return filepath.Join(p.stateDir, darwinHelperAppName)
}

func (p *darwinProvider) Capabilities(ctx context.Context) (Capabilities, error) {
	capabilities := Capabilities{Provider: "everyapi-computer-use-macos", ProviderVersion: computerProviderVersion, ProtocolVersion: computerProtocolVersion, Platform: "darwin"}
	capabilities.Supports.Apps.List = true
	capabilities.Supports.Apps.BundleIDs = true
	capabilities.Supports.Apps.PIDs = true
	capabilities.Supports.Windows.List = true
	capabilities.Supports.Windows.TargetByIndex = true
	// The native helper matches AXUIElement windows to the real CGWindowID
	// CoreGraphics assigns (see
	// clients/desktop/native/computer-use-macos/src/state.rs); --window-id
	// (alongside --window-index) exposes that id as a public selector.
	capabilities.Supports.Windows.TargetByID = true
	capabilities.Supports.Observation.AccessibilityTree = true
	capabilities.Supports.Observation.ElementFrames = true
	// CGWindowListCreateImage captures window_id's own pixels regardless of
	// what overlaps it, so screenshot shares the window-scoped guarantee the
	// other observation capabilities already make.
	capabilities.Supports.Observation.Screenshot = true
	capabilities.Supports.Actions.Click = true
	capabilities.Supports.Actions.SetValue = true
	capabilities.Supports.Actions.TypeText = true
	capabilities.Supports.Actions.PressKey = true
	capabilities.Supports.Actions.Hotkey = true
	capabilities.Supports.Actions.Scroll = true
	capabilities.Supports.Actions.Drag = true
	capabilities.Supports.Actions.PerformAction = true
	capabilities.Supports.Actions.PasteText = true
	return capabilities, nil
}

func (p *darwinProvider) Permissions(ctx context.Context) (PermissionStatus, error) {
	var wire struct {
		Accessibility PermissionState `json:"accessibility"`
		Automation    PermissionState `json:"automation"`
		Screenshot    PermissionState `json:"screenshot"`
	}
	if err := p.call(ctx, "permissions", nil, &wire, 5*time.Second); err != nil {
		return PermissionStatus{}, err
	}
	return PermissionStatus{Accessibility: wire.Accessibility, Automation: wire.Automation, Screenshot: wire.Screenshot}, nil
}

func (p *darwinProvider) RequestPermission(ctx context.Context, kind string) error {
	method := ""
	switch kind {
	case "accessibility":
		method = "requestAccessibility"
	case "screen-recording":
		method = "requestScreenCapture"
	default:
		return NewError(CodeInvalidArgument, "permission must be accessibility or screen-recording", nil)
	}
	var ignored bool
	return p.call(ctx, method, nil, &ignored, 30*time.Second)
}

func (p *darwinProvider) ListApps(ctx context.Context) ([]App, error) {
	var apps []App
	if err := p.call(ctx, "listApps", nil, &apps, darwinStateTimeout); err != nil {
		return nil, err
	}
	return apps, nil
}

type darwinListWindowsParams struct {
	PID      int    `json:"pid"`
	BundleID string `json:"bundleId"`
}

type darwinWindowWire struct {
	ID          uint32 `json:"id"`
	Index       int    `json:"index"`
	Title       string `json:"title"`
	Frame       Frame  `json:"frame"`
	Focused     bool   `json:"focused"`
	Fingerprint string `json:"fingerprint"`
}

func (w darwinWindowWire) window() Window {
	return Window{ID: int(w.ID), Index: w.Index, Title: w.Title, Frame: w.Frame, Focused: w.Focused, Fingerprint: w.Fingerprint}
}

func (p *darwinProvider) ListWindows(ctx context.Context, app App) ([]Window, error) {
	var wire []darwinWindowWire
	params := darwinListWindowsParams{PID: app.PID, BundleID: app.BundleID}
	if err := p.call(ctx, "listWindows", params, &wire, darwinStateTimeout); err != nil {
		return nil, err
	}
	windows := make([]Window, len(wire))
	for i := range wire {
		windows[i] = wire[i].window()
	}
	return windows, nil
}

type darwinGetStateParams struct {
	PID               int    `json:"pid"`
	BundleID          string `json:"bundleId"`
	WindowID          uint32 `json:"windowId"`
	WindowFingerprint string `json:"windowFingerprint"`
	MaxWalk           int    `json:"maxWalk"`
	MaxShown          int    `json:"maxShown"`
	BudgetMs          int64  `json:"budgetMs"`
	MaxDepth          int    `json:"maxDepth"`
}

type darwinElementWire struct {
	Index       int      `json:"index"`
	Path        []int    `json:"path"`
	Role        string   `json:"role"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Value       string   `json:"value"`
	Frame       Frame    `json:"frame"`
	Actions     []string `json:"actions"`
	Fingerprint string   `json:"fingerprint"`
}

type darwinStateWire struct {
	App       App                 `json:"app"`
	Window    darwinWindowWire    `json:"window"`
	Elements  []darwinElementWire `json:"elements"`
	Walked    int                 `json:"walked"`
	Truncated bool                `json:"truncated"`
}

func (p *darwinProvider) GetState(ctx context.Context, target Target) (State, error) {
	params := darwinGetStateParams{
		PID: target.App.PID, BundleID: target.App.BundleID, WindowID: uint32(target.Window.ID), WindowFingerprint: target.Window.Fingerprint,
		MaxWalk: darwinMaxWalk, MaxShown: darwinMaxShown, BudgetMs: darwinWalkBudgetMS, MaxDepth: darwinMaxDepth,
	}
	var wire darwinStateWire
	if err := p.call(ctx, "getState", params, &wire, darwinStateTimeout); err != nil {
		return State{}, err
	}
	state := State{App: wire.App, Window: wire.Window.window(), Snapshot: Snapshot{Walked: wire.Walked, Truncated: wire.Truncated}}
	state.Snapshot.Elements = make([]Element, len(wire.Elements))
	for i, element := range wire.Elements {
		state.Snapshot.Elements[i] = Element{Index: element.Index, Path: element.Path, Role: element.Role, Title: element.Title, Description: element.Description, Value: element.Value, Frame: element.Frame, Actions: element.Actions, Fingerprint: element.Fingerprint}
	}
	state.Snapshot.ElementCount = len(state.Snapshot.Elements)
	state.Snapshot.TreeText = renderTree(state.Snapshot)
	return state, nil
}

type darwinScreenshotParams struct {
	PID               int    `json:"pid"`
	BundleID          string `json:"bundleId"`
	WindowID          uint32 `json:"windowId"`
	WindowFingerprint string `json:"windowFingerprint"`
}

type darwinScreenshotWire struct {
	PNG string `json:"png"`
}

func (p *darwinProvider) Screenshot(ctx context.Context, target Target) ([]byte, error) {
	params := darwinScreenshotParams{PID: target.App.PID, BundleID: target.App.BundleID, WindowID: uint32(target.Window.ID), WindowFingerprint: target.Window.Fingerprint}
	var wire darwinScreenshotWire
	if err := p.call(ctx, "screenshot", params, &wire, darwinStateTimeout); err != nil {
		return nil, err
	}
	png, err := base64.StdEncoding.DecodeString(wire.PNG)
	if err != nil {
		return nil, NewError(CodeInternal, "decode screenshot payload: "+err.Error(), err)
	}
	return png, nil
}

func renderTree(snapshot Snapshot) string {
	var b strings.Builder
	for _, element := range snapshot.Elements {
		fmt.Fprintf(&b, "[%d] %s", element.Index, element.Role)
		if element.Title != "" {
			fmt.Fprintf(&b, " %q", element.Title)
		}
		if element.Description != "" && element.Description != element.Title {
			fmt.Fprintf(&b, " description=%q", element.Description)
		}
		if element.Value != "" {
			fmt.Fprintf(&b, " value=%q", element.Value)
		}
		fmt.Fprintf(&b, " frame=%.0f,%.0f %.0fx%.0f", element.Frame.X, element.Frame.Y, element.Frame.Width, element.Frame.Height)
		if len(element.Actions) > 0 {
			fmt.Fprintf(&b, " actions=%q", strings.Join(element.Actions, ","))
		}
		b.WriteByte('\n')
	}
	if snapshot.Truncated {
		fmt.Fprintf(&b, "[tree truncated after walking %d elements]\n", snapshot.Walked)
	}
	return strings.TrimRight(b.String(), "\n")
}

type darwinActionPayload struct {
	PID                int      `json:"pid"`
	BundleID           string   `json:"bundleId"`
	WindowID           uint32   `json:"windowId"`
	Kind               string   `json:"kind"`
	WindowFingerprint  string   `json:"windowFingerprint,omitempty"`
	Path               []int    `json:"path,omitempty"`
	Role               string   `json:"role,omitempty"`
	ElementFingerprint string   `json:"elementFingerprint,omitempty"`
	FromPath           []int    `json:"fromPath,omitempty"`
	FromRole           string   `json:"fromRole,omitempty"`
	FromFingerprint    string   `json:"fromFingerprint,omitempty"`
	ToPath             []int    `json:"toPath,omitempty"`
	ToRole             string   `json:"toRole,omitempty"`
	ToFingerprint      string   `json:"toFingerprint,omitempty"`
	ScreenX            *int     `json:"screenX,omitempty"`
	ScreenY            *int     `json:"screenY,omitempty"`
	FromScreenX        *int     `json:"fromScreenX,omitempty"`
	FromScreenY        *int     `json:"fromScreenY,omitempty"`
	ToScreenX          *int     `json:"toScreenX,omitempty"`
	ToScreenY          *int     `json:"toScreenY,omitempty"`
	Text               string   `json:"text"`
	KeyChar            string   `json:"keyChar,omitempty"`
	KeyCode            *int     `json:"keyCode,omitempty"`
	Modifiers          []string `json:"modifiers,omitempty"`
	Direction          string   `json:"direction,omitempty"`
	Amount             int      `json:"amount,omitempty"`
	SecondaryAction    string   `json:"secondaryAction,omitempty"`
	MouseButton        string   `json:"mouseButton,omitempty"`
	ClickCount         *int     `json:"clickCount,omitempty"`
	RestoreWindow      bool     `json:"restoreWindow,omitempty"`
}

func (p *darwinProvider) Perform(ctx context.Context, req PerformRequest) error {
	payload := darwinActionPayload{PID: req.Target.App.PID, BundleID: req.Target.App.BundleID, WindowID: uint32(req.Target.Window.ID), Kind: string(req.Kind), WindowFingerprint: req.ExpectedWindowFingerprint, Text: req.Text, Direction: req.Direction, Amount: req.Amount, SecondaryAction: req.SecondaryAction, MouseButton: req.MouseButton, ClickCount: req.ClickCount, RestoreWindow: req.RestoreWindow}
	if req.ExpectedElement != nil {
		payload.Path = req.ExpectedElement.Path
		payload.Role = req.ExpectedElement.Role
		payload.ElementFingerprint = req.ExpectedElement.Fingerprint
	}
	if req.ExpectedFromElement != nil {
		payload.FromPath = req.ExpectedFromElement.Path
		payload.FromRole = req.ExpectedFromElement.Role
		payload.FromFingerprint = req.ExpectedFromElement.Fingerprint
	}
	if req.ExpectedToElement != nil {
		payload.ToPath = req.ExpectedToElement.Path
		payload.ToRole = req.ExpectedToElement.Role
		payload.ToFingerprint = req.ExpectedToElement.Fingerprint
	}
	payload.ScreenX, payload.ScreenY = windowLocalPoint(req.Target.Window, req.X, req.Y)
	payload.FromScreenX, payload.FromScreenY = windowLocalPoint(req.Target.Window, req.FromX, req.FromY)
	payload.ToScreenX, payload.ToScreenY = windowLocalPoint(req.Target.Window, req.ToX, req.ToY)
	if req.Kind == ActionPressKey || req.Kind == ActionHotkey {
		char, code, modifiers, err := parseDarwinKey(req.Key, req.Kind == ActionHotkey)
		if err != nil {
			return err
		}
		payload.KeyChar, payload.KeyCode, payload.Modifiers = char, code, modifiers
	}
	if req.Kind == ActionClick && req.Modifiers != "" {
		modifiers, err := parseDarwinModifiers(req.Modifiers)
		if err != nil {
			return err
		}
		payload.Modifiers = modifiers
	}
	var empty struct{}
	if err := p.call(ctx, "perform", payload, &empty, darwinActionTimeout); err != nil {
		if ErrorCode(err) == CodeActionTimeout {
			return NewError(CodeActionOutcomeUnknown, "macOS action outcome is unknown after the helper call was interrupted; refresh state before deciding whether to retry", err)
		}
		return err
	}
	return nil
}

func windowLocalPoint(window Window, x, y *int) (*int, *int) {
	if x == nil || y == nil {
		return nil, nil
	}
	screenX := int(window.Frame.X) + *x
	screenY := int(window.Frame.Y) + *y
	return &screenX, &screenY
}

var darwinKeyCodes = map[string]int{"return": 36, "enter": 76, "tab": 48, "space": 49, "delete": 51, "backspace": 51, "forwarddelete": 117, "esc": 53, "escape": 53, "left": 123, "right": 124, "down": 125, "up": 126, "home": 115, "end": 119, "pageup": 116, "pagedown": 121, "f1": 122, "f2": 120, "f3": 99, "f4": 118, "f5": 96, "f6": 97, "f7": 98, "f8": 100, "f9": 101, "f10": 109, "f11": 103, "f12": 111}

func parseDarwinKey(value string, requireModifier bool) (string, *int, []string, error) {
	parts := strings.Split(strings.TrimSpace(value), "+")
	if len(parts) == 0 || strings.TrimSpace(parts[len(parts)-1]) == "" {
		return "", nil, nil, NewError(CodeInvalidArgument, "key must name a character or supported key", nil)
	}
	var modifiers []string
	for _, part := range parts[:len(parts)-1] {
		switch strings.ToLower(strings.TrimSpace(part)) {
		case "cmd", "command", "cmdorctrl":
			modifiers = append(modifiers, "command")
		case "shift":
			modifiers = append(modifiers, "shift")
		case "opt", "option", "alt":
			modifiers = append(modifiers, "option")
		case "ctrl", "control":
			modifiers = append(modifiers, "control")
		default:
			return "", nil, nil, NewError(CodeInvalidArgument, "unsupported key modifier "+part, nil)
		}
	}
	if requireModifier && len(modifiers) == 0 {
		return "", nil, nil, NewError(CodeInvalidArgument, "hotkey requires at least one modifier", nil)
	}
	if !requireModifier && len(modifiers) != 0 {
		return "", nil, nil, NewError(CodeInvalidArgument, "press-key does not accept modifiers; use hotkey", nil)
	}
	key := strings.TrimSpace(parts[len(parts)-1])
	if code, ok := darwinKeyCodes[strings.ToLower(key)]; ok {
		return "", &code, modifiers, nil
	}
	if len([]rune(key)) != 1 {
		return "", nil, nil, NewError(CodeInvalidArgument, "unsupported key "+key, nil)
	}
	return key, nil, modifiers, nil
}

// parseDarwinModifiers accepts the same modifier names as parseDarwinKey but,
// unlike it, requires every "+"-joined part to be a modifier — there is no
// final key, because a click already has its own target (an element or a
// point) and only needs to know which modifier keys were held while it fired.
func parseDarwinModifiers(value string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	var modifiers []string
	for _, part := range strings.Split(value, "+") {
		switch strings.ToLower(strings.TrimSpace(part)) {
		case "cmd", "command", "cmdorctrl":
			modifiers = append(modifiers, "command")
		case "shift":
			modifiers = append(modifiers, "shift")
		case "opt", "option", "alt":
			modifiers = append(modifiers, "option")
		case "ctrl", "control":
			modifiers = append(modifiers, "control")
		default:
			return nil, NewError(CodeInvalidArgument, "unsupported modifier "+part, nil)
		}
	}
	return modifiers, nil
}

// --- RPC transport ---

type darwinRPCRequest struct {
	ID     int    `json:"id"`
	Method string `json:"method"`
	Token  string `json:"token"`
	Params any    `json:"params"`
}

// call ensures the shared helper daemon is reachable (which, on a machine
// that has never run computer-use before, includes downloading and
// launching it — legitimately tens of seconds) and then performs one RPC
// round trip bounded by rpcTimeout. The two phases use separate budgets on
// purpose: a slow first-run install must not be misreported as the action
// itself having an unknown outcome, which is what rpcTimeout expiring means.
func (p *darwinProvider) call(ctx context.Context, method string, params, result any, rpcTimeout time.Duration) error {
	if err := p.ensureHelper(ctx); err != nil {
		return err
	}
	rpcCtx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	dialer := net.Dialer{Timeout: darwinDialTimeout}
	conn, err := dialer.DialContext(rpcCtx, "unix", p.socketPath())
	if err != nil {
		return NewError(CodeInternal, "connect to computer-use helper: "+err.Error(), err)
	}
	defer conn.Close()

	token, err := os.ReadFile(p.tokenPath())
	if err != nil {
		return NewError(CodeInternal, "read computer-use helper token: "+err.Error(), err)
	}
	if deadline, ok := rpcCtx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	request := darwinRPCRequest{ID: 1, Method: method, Token: strings.TrimSpace(string(token)), Params: params}
	encoded, err := json.Marshal(request)
	if err != nil {
		return NewError(CodeInternal, "encode computer-use helper request", err)
	}
	if _, err := conn.Write(append(encoded, '\n')); err != nil {
		return NewError(CodeInternal, "write to computer-use helper: "+err.Error(), err)
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		if ctxErr := rpcCtx.Err(); ctxErr != nil {
			return NewError(CodeActionTimeout, "computer-use helper timed out", ctxErr)
		}
		return NewError(CodeInternal, "read from computer-use helper: "+err.Error(), err)
	}
	var envelope struct {
		OK      bool            `json:"ok"`
		Code    string          `json:"code"`
		Message string          `json:"message"`
		Result  json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		return NewError(CodeInternal, "decode computer-use helper response: "+err.Error(), err)
	}
	if !envelope.OK {
		code := envelope.Code
		if code == "" {
			code = CodeInternal
		}
		return NewError(code, redactSensitiveText(envelope.Message), nil)
	}
	if result == nil || len(envelope.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(envelope.Result, result); err != nil {
		return NewError(CodeInternal, "decode computer-use helper result: "+err.Error(), err)
	}
	return nil
}

// --- Helper lifecycle: locate an installed copy (Connect's, or our own
// managed download), launch it, and wait for the shared socket to accept
// connections. Both this CLI and EveryAPI Connect target the same canonical
// state directory, so whichever process starts the helper first serves both.

func (p *darwinProvider) ensureHelper(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.socketReachable() {
		return nil
	}
	appPath, err := p.locateOrInstallHelperApp(ctx)
	if err != nil {
		return err
	}
	if err := exec.CommandContext(ctx, "/usr/bin/open", "-n", appPath).Run(); err != nil {
		return NewError(CodeInternal, "launch computer-use helper: "+err.Error(), err)
	}
	deadline := time.Now().Add(darwinLaunchWait)
	for time.Now().Before(deadline) {
		if p.socketReachable() {
			return nil
		}
		select {
		case <-ctx.Done():
			return NewError(CodeActionTimeout, "computer-use helper did not start before the context was canceled", ctx.Err())
		case <-time.After(darwinLaunchPoll):
		}
	}
	return NewError(CodeInternal, "computer-use helper did not start listening within "+darwinLaunchWait.String(), nil)
}

func (p *darwinProvider) socketReachable() bool {
	conn, err := net.DialTimeout("unix", p.socketPath(), darwinDialTimeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (p *darwinProvider) locateOrInstallHelperApp(ctx context.Context) (string, error) {
	for _, candidate := range []string{darwinConnectResourcePath, p.managedAppPath()} {
		if helperSupportsProtocol(ctx, candidate) {
			return candidate, nil
		}
	}
	return p.installHelperApp(ctx)
}

// A protocol bump uses a new socket so an upgraded CLI never talks to an old
// daemon. It must also reject an old helper bundle, otherwise `open` starts
// that binary on its v1 default socket and the v2 caller waits until timeout.
// The command is local, secret-free, and absent from v1, which makes a failed
// or mismatched probe an unambiguous signal to install the current artifact.
func helperSupportsProtocol(ctx context.Context, appPath string) bool {
	executable := filepath.Join(appPath, "Contents", "MacOS", darwinHelperExecutable)
	if info, err := os.Stat(executable); err != nil || info.IsDir() {
		return false
	}
	output, err := exec.CommandContext(ctx, executable, "protocol-version").Output()
	return err == nil && strings.TrimSpace(string(output)) == fmt.Sprint(computerProtocolVersion)
}

func (p *darwinProvider) installHelperApp(ctx context.Context) (string, error) {
	downloadCtx, cancel := context.WithTimeout(ctx, darwinDownloadTimeout)
	defer cancel()

	archive, err := httpGet(downloadCtx, darwinHelperReleaseBaseURL+darwinHelperReleaseAsset)
	if err != nil {
		return "", NewError(CodeDependencyMissing, "download the computer-use helper: "+err.Error(), err)
	}
	// A dedicated single-hash file, not an entry appended to the CLI's own
	// SHA256SUMS: that file is cosign-signed by the main release job, and
	// appending a line to it after the fact would silently invalidate that
	// signature for every other artifact in the release.
	checksum, err := httpGet(downloadCtx, darwinHelperReleaseBaseURL+darwinHelperReleaseAsset+".sha256")
	if err != nil {
		return "", NewError(CodeDependencyMissing, "download computer-use helper checksum: "+err.Error(), err)
	}
	if err := verifySHA256(archive, checksum); err != nil {
		return "", NewError(CodeDependencyMissing, "verify computer-use helper checksum: "+err.Error(), err)
	}

	stagingDir, err := os.MkdirTemp(p.stateDir, ".install-*")
	if err != nil {
		if mkErr := os.MkdirAll(p.stateDir, 0o700); mkErr != nil {
			return "", NewError(CodeInternal, "create computer-use state directory: "+mkErr.Error(), mkErr)
		}
		stagingDir, err = os.MkdirTemp(p.stateDir, ".install-*")
		if err != nil {
			return "", NewError(CodeInternal, "create computer-use install staging directory: "+err.Error(), err)
		}
	}
	defer os.RemoveAll(stagingDir)

	if err := extractZip(archive, stagingDir); err != nil {
		return "", NewError(CodeInternal, "extract computer-use helper: "+err.Error(), err)
	}
	extractedApp := filepath.Join(stagingDir, darwinHelperAppName)
	if _, err := os.Stat(extractedApp); err != nil {
		return "", NewError(CodeInternal, "computer-use helper archive did not contain "+darwinHelperAppName, err)
	}
	target := p.managedAppPath()
	_ = os.RemoveAll(target)
	if err := os.Rename(extractedApp, target); err != nil {
		return "", NewError(CodeInternal, "install computer-use helper: "+err.Error(), err)
	}
	return target, nil
}

func httpGet(ctx context.Context, url string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d for %s", response.StatusCode, url)
	}
	return io.ReadAll(io.LimitReader(response.Body, 128<<20))
}

func verifySHA256(archive, checksumFile []byte) error {
	sum := sha256.Sum256(archive)
	digest := hex.EncodeToString(sum[:])
	fields := strings.Fields(string(checksumFile))
	if len(fields) == 0 {
		return fmt.Errorf("empty checksum file")
	}
	if fields[0] != digest {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", fields[0], digest)
	}
	return nil
}

func extractZip(archive []byte, destDir string) error {
	reader, err := zip.NewReader(strings.NewReader(string(archive)), int64(len(archive)))
	if err != nil {
		return err
	}
	for _, file := range reader.File {
		targetPath := filepath.Join(destDir, file.Name)
		if !strings.HasPrefix(targetPath, filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("zip entry escapes destination: %s", file.Name)
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}
		reader, err := file.Open()
		if err != nil {
			return err
		}
		mode := file.Mode()
		if mode == 0 {
			mode = 0o644
		}
		out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			reader.Close()
			return err
		}
		_, copyErr := io.Copy(out, reader)
		reader.Close()
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
