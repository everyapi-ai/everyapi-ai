package computer

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/computeruse"
)

type fakeService struct {
	stateRequests       []computeruse.StateRequest
	actionRequests      []computeruse.ActionRequest
	screenshotRequests  []computeruse.StateRequest
	listAppsErr         error
	screenshotPNG       []byte
	requestedPermission string
}

func (f *fakeService) Capabilities(context.Context) (computeruse.Capabilities, error) {
	return computeruse.Capabilities{Provider: "fake", ProviderVersion: "1.0.0", ProtocolVersion: 1, Platform: "darwin"}, nil
}

func (f *fakeService) Permissions(context.Context) (computeruse.PermissionStatus, error) {
	return computeruse.PermissionStatus{Accessibility: computeruse.PermissionGranted}, nil
}

func (f *fakeService) RequestPermission(_ context.Context, kind string) error {
	f.requestedPermission = kind
	return nil
}

func (f *fakeService) ListApps(context.Context) ([]computeruse.App, error) {
	if f.listAppsErr != nil {
		return nil, f.listAppsErr
	}
	return []computeruse.App{{Name: "TextEdit", BundleID: "com.apple.TextEdit", PID: 42, WindowCount: 1}}, nil
}

func (f *fakeService) ListWindows(context.Context, string) ([]computeruse.Window, error) {
	return []computeruse.Window{{ID: 7, Index: 0, Title: "Untitled"}}, nil
}

func (f *fakeService) GetAppState(_ context.Context, req computeruse.StateRequest) (computeruse.State, error) {
	f.stateRequests = append(f.stateRequests, req)
	return computeruse.State{App: computeruse.App{Name: "TextEdit", PID: 42}, Window: computeruse.Window{ID: 7, Title: "Untitled"}, Snapshot: computeruse.Snapshot{TreeText: "[1] AXTextArea", ElementCount: 1}}, nil
}

func (f *fakeService) Perform(_ context.Context, req computeruse.ActionRequest) (computeruse.State, error) {
	f.actionRequests = append(f.actionRequests, req)
	return computeruse.State{App: computeruse.App{Name: "TextEdit", PID: 42}, Window: computeruse.Window{ID: 7}, Snapshot: computeruse.Snapshot{TreeText: "[1] AXTextArea", ElementCount: 1}}, nil
}

func (f *fakeService) Screenshot(_ context.Context, req computeruse.StateRequest) ([]byte, error) {
	f.screenshotRequests = append(f.screenshotRequests, req)
	if f.screenshotPNG != nil {
		return f.screenshotPNG, nil
	}
	return []byte("fake-png-bytes"), nil
}

func TestCapabilitiesJSONEnvelope(t *testing.T) {
	service := &fakeService{}
	var out bytes.Buffer
	if err := run(context.Background(), []string{"capabilities", "--json"}, service, strings.NewReader(""), &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	var envelope struct {
		OK     bool `json:"ok"`
		Result struct {
			Provider string `json:"provider"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("decode output %q: %v", out.String(), err)
	}
	if !envelope.OK || envelope.Result.Provider != "fake" {
		t.Fatalf("envelope = %+v", envelope)
	}
}

func TestPermissionsCanRequestMacOSConsent(t *testing.T) {
	service := &fakeService{}
	var out bytes.Buffer
	if err := run(context.Background(), []string{"permissions", "--request", "screen-recording", "--json"}, service, strings.NewReader(""), &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if service.requestedPermission != "screen-recording" {
		t.Fatalf("requested permission = %q", service.requestedPermission)
	}
	if !strings.Contains(out.String(), `"accessibility":"granted"`) {
		t.Fatalf("JSON output = %q", out.String())
	}
}

func TestListWindowsExposesWindowID(t *testing.T) {
	service := &fakeService{}
	var jsonOut bytes.Buffer
	if err := run(context.Background(), []string{"list-windows", "--app", "TextEdit", "--json"}, service, strings.NewReader(""), &jsonOut); err != nil {
		t.Fatalf("JSON run: %v", err)
	}
	if !strings.Contains(jsonOut.String(), `"id":7`) || !strings.Contains(jsonOut.String(), `"index":0`) {
		t.Fatalf("JSON output = %q", jsonOut.String())
	}
	var plainOut bytes.Buffer
	if err := run(context.Background(), []string{"list-windows", "--app", "TextEdit"}, service, strings.NewReader(""), &plainOut); err != nil {
		t.Fatalf("plain run: %v", err)
	}
	if !strings.Contains(plainOut.String(), "id=7") || !strings.Contains(plainOut.String(), "[0]") {
		t.Fatalf("plain output = %q", plainOut.String())
	}
}

func TestScreenshotWritesFileWhenOutIsGiven(t *testing.T) {
	service := &fakeService{screenshotPNG: []byte("real-png-bytes")}
	outPath := filepath.Join(t.TempDir(), "shot.png")
	var out bytes.Buffer
	if err := run(context.Background(), []string{"screenshot", "--app", "TextEdit", "--window-index", "0", "--out", outPath}, service, strings.NewReader(""), &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(service.screenshotRequests) != 1 || service.screenshotRequests[0].App != "TextEdit" {
		t.Fatalf("screenshot requests = %+v", service.screenshotRequests)
	}
	written, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read %s: %v", outPath, err)
	}
	if string(written) != "real-png-bytes" {
		t.Fatalf("written bytes = %q", written)
	}
	if !strings.Contains(out.String(), outPath) {
		t.Fatalf("plain output = %q", out.String())
	}
}

func TestScreenshotJSONReturnsBase64WithoutOut(t *testing.T) {
	service := &fakeService{screenshotPNG: []byte("real-png-bytes")}
	var out bytes.Buffer
	if err := run(context.Background(), []string{"screenshot", "--app", "TextEdit", "--window-index", "0", "--json"}, service, strings.NewReader(""), &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	var response struct {
		Result struct {
			PNG string `json:"png"`
		} `json:"result"`
	}
	if decodeErr := json.Unmarshal(out.Bytes(), &response); decodeErr != nil {
		t.Fatalf("decode output %q: %v", out.String(), decodeErr)
	}
	decoded, err := base64.StdEncoding.DecodeString(response.Result.PNG)
	if err != nil || string(decoded) != "real-png-bytes" {
		t.Fatalf("decoded png = %q, err = %v", decoded, err)
	}
}

func TestScreenshotPlainWithoutOutIsRejected(t *testing.T) {
	service := &fakeService{}
	err := run(context.Background(), []string{"screenshot", "--app", "TextEdit", "--window-index", "0"}, service, strings.NewReader(""), &bytes.Buffer{})
	if computeruse.ErrorCode(err) != computeruse.CodeInvalidArgument {
		t.Fatalf("error = %v (%q)", err, computeruse.ErrorCode(err))
	}
	if len(service.screenshotRequests) != 0 {
		t.Fatal("service was called before validating --out/--json")
	}
}

func TestGetAppStateParsesWindowIndex(t *testing.T) {
	service := &fakeService{}
	var out bytes.Buffer
	if err := run(context.Background(), []string{"get-app-state", "--app", "com.apple.TextEdit", "--window-index", "0", "--json"}, service, strings.NewReader(""), &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(service.stateRequests) != 1 {
		t.Fatalf("state requests = %d", len(service.stateRequests))
	}
	req := service.stateRequests[0]
	if req.App != "com.apple.TextEdit" || req.WindowIndex == nil || *req.WindowIndex != 0 {
		t.Fatalf("state request = %+v", req)
	}
}

func TestGetAppStateParsesWindowID(t *testing.T) {
	service := &fakeService{}
	if err := run(context.Background(), []string{"get-app-state", "--app", "TextEdit", "--window-id", "7", "--json"}, service, strings.NewReader(""), &bytes.Buffer{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(service.stateRequests) != 1 {
		t.Fatalf("state requests = %d", len(service.stateRequests))
	}
	req := service.stateRequests[0]
	if req.App != "TextEdit" || req.WindowID == nil || *req.WindowID != 7 || req.WindowIndex != nil {
		t.Fatalf("state request = %+v", req)
	}
}

func TestGetAppStateParsesNoScreenshot(t *testing.T) {
	service := &fakeService{}
	if err := run(context.Background(), []string{"get-app-state", "--app", "TextEdit", "--window-index", "0", "--no-screenshot", "--json"}, service, strings.NewReader(""), &bytes.Buffer{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(service.stateRequests) != 1 {
		t.Fatalf("state requests = %d", len(service.stateRequests))
	}
	if req := service.stateRequests[0]; !req.NoScreenshot {
		t.Fatalf("state request = %+v, want NoScreenshot=true", req)
	}
}

func TestGetAppStateParsesSession(t *testing.T) {
	service := &fakeService{}
	if err := run(context.Background(), []string{"get-app-state", "--app", "TextEdit", "--window-index", "0", "--session", "agent-a", "--json"}, service, strings.NewReader(""), &bytes.Buffer{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(service.stateRequests) != 1 {
		t.Fatalf("state requests = %d", len(service.stateRequests))
	}
	if req := service.stateRequests[0]; req.SessionID != "agent-a" {
		t.Fatalf("state request = %+v, want SessionID=agent-a", req)
	}
}

func TestGetAppStateRejectsWindowIDAndWindowIndexTogether(t *testing.T) {
	service := &fakeService{}
	err := run(context.Background(), []string{"get-app-state", "--app", "TextEdit", "--window-id", "7", "--window-index", "0"}, service, strings.NewReader(""), &bytes.Buffer{})
	if computeruse.ErrorCode(err) != computeruse.CodeInvalidArgument {
		t.Fatalf("error = %v (%q), want %q", err, computeruse.ErrorCode(err), computeruse.CodeInvalidArgument)
	}
	if len(service.stateRequests) != 0 {
		t.Fatal("service was called for an invalid request")
	}
}

func TestSetValueReadsStdinAndKeepsTextOutOfArgs(t *testing.T) {
	service := &fakeService{}
	var out bytes.Buffer
	if err := run(context.Background(), []string{"set-value", "--app", "TextEdit", "--window-index", "0", "--element-index", "12", "--value-stdin", "--json"}, service, strings.NewReader("hello from stdin"), &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(service.actionRequests) != 1 {
		t.Fatalf("action requests = %d", len(service.actionRequests))
	}
	req := service.actionRequests[0]
	if req.Kind != computeruse.ActionSetValue || req.Text != "hello from stdin" || req.ElementIndex == nil || *req.ElementIndex != 12 {
		t.Fatalf("action request = %+v", req)
	}
}

func TestSetValueAcceptsExplicitEmptyValues(t *testing.T) {
	for _, args := range [][]string{
		{"set-value", "--app", "TextEdit", "--element-index", "12", "--value="},
		{"set-value", "--app", "TextEdit", "--element-index", "12", "--value-stdin"},
	} {
		service := &fakeService{}
		if err := run(context.Background(), args, service, strings.NewReader(""), &bytes.Buffer{}); err != nil {
			t.Fatalf("run(%v): %v", args, err)
		}
		if len(service.actionRequests) != 1 || service.actionRequests[0].Kind != computeruse.ActionSetValue || service.actionRequests[0].Text != "" {
			t.Fatalf("run(%v) action requests = %+v", args, service.actionRequests)
		}
	}
}

func TestTypeTextRejectsExplicitEmptyValues(t *testing.T) {
	for _, args := range [][]string{
		{"type-text", "--app", "TextEdit", "--text="},
		{"type-text", "--app", "TextEdit", "--text-stdin"},
	} {
		service := &fakeService{}
		err := run(context.Background(), args, service, strings.NewReader(""), &bytes.Buffer{})
		if computeruse.ErrorCode(err) != computeruse.CodeInvalidArgument {
			t.Fatalf("run(%v) error = %v (%q)", args, err, computeruse.ErrorCode(err))
		}
		if len(service.actionRequests) != 0 {
			t.Fatalf("run(%v) called service", args)
		}
	}
}

func TestPasteTextRejectsExplicitEmptyValues(t *testing.T) {
	for _, args := range [][]string{
		{"paste-text", "--app", "TextEdit", "--text="},
		{"paste-text", "--app", "TextEdit", "--text-stdin"},
	} {
		service := &fakeService{}
		err := run(context.Background(), args, service, strings.NewReader(""), &bytes.Buffer{})
		if computeruse.ErrorCode(err) != computeruse.CodeInvalidArgument {
			t.Fatalf("run(%v) error = %v (%q)", args, err, computeruse.ErrorCode(err))
		}
		if len(service.actionRequests) != 0 {
			t.Fatalf("run(%v) called service", args)
		}
	}
}

func TestClickRejectsInvalidMouseButton(t *testing.T) {
	service := &fakeService{}
	err := run(context.Background(), []string{"click", "--app", "TextEdit", "--x", "1", "--y", "2", "--mouse-button", "scroll-wheel"}, service, strings.NewReader(""), &bytes.Buffer{})
	if computeruse.ErrorCode(err) != computeruse.CodeInvalidArgument {
		t.Fatalf("error = %v (%q)", err, computeruse.ErrorCode(err))
	}
	if len(service.actionRequests) != 0 {
		t.Fatal("service was called for an invalid mouse button")
	}
}

func TestClickRequiresExactlyOneTargetShape(t *testing.T) {
	service := &fakeService{}
	for _, args := range [][]string{
		{"click", "--app", "TextEdit"},
		{"click", "--app", "TextEdit", "--element-index", "1", "--x", "10", "--y", "20"},
	} {
		err := run(context.Background(), args, service, strings.NewReader(""), &bytes.Buffer{})
		if computeruse.ErrorCode(err) != computeruse.CodeInvalidArgument {
			t.Fatalf("run(%v) error = %v (%q)", args, err, computeruse.ErrorCode(err))
		}
	}
	if len(service.actionRequests) != 0 {
		t.Fatal("service was called for invalid click arguments")
	}
}

func TestUnknownSubcommandReturnsCodedError(t *testing.T) {
	err := run(context.Background(), []string{"launch-missiles"}, &fakeService{}, strings.NewReader(""), &bytes.Buffer{})
	if computeruse.ErrorCode(err) != computeruse.CodeInvalidArgument {
		t.Fatalf("error = %v (%q)", err, computeruse.ErrorCode(err))
	}
}

func TestActionCommandsDispatchExactRequestShape(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want func(computeruse.ActionRequest) bool
	}{
		{name: "click coordinates", args: []string{"click", "--app", "TextEdit", "--x", "10", "--y", "20", "--json"}, want: func(req computeruse.ActionRequest) bool {
			return req.Kind == computeruse.ActionClick && req.X != nil && *req.X == 10 && req.Y != nil && *req.Y == 20
		}},
		{name: "click with mouse button, click count, and modifiers", args: []string{"click", "--app", "TextEdit", "--x", "10", "--y", "20", "--mouse-button", "right", "--click-count", "2", "--modifiers", "cmd+shift", "--json"}, want: func(req computeruse.ActionRequest) bool {
			return req.Kind == computeruse.ActionClick && req.MouseButton == "right" && req.ClickCount != nil && *req.ClickCount == 2 && req.Modifiers == "cmd+shift"
		}},
		{name: "click with restore-window", args: []string{"click", "--app", "TextEdit", "--x", "10", "--y", "20", "--restore-window", "--json"}, want: func(req computeruse.ActionRequest) bool {
			return req.Kind == computeruse.ActionClick && req.RestoreWindow
		}},
		{name: "click with no-screenshot", args: []string{"click", "--app", "TextEdit", "--x", "10", "--y", "20", "--no-screenshot", "--json"}, want: func(req computeruse.ActionRequest) bool {
			return req.Kind == computeruse.ActionClick && req.NoScreenshot
		}},
		{name: "click with session", args: []string{"click", "--app", "TextEdit", "--x", "10", "--y", "20", "--session", "agent-a", "--json"}, want: func(req computeruse.ActionRequest) bool {
			return req.Kind == computeruse.ActionClick && req.SessionID == "agent-a"
		}},
		{name: "type text", args: []string{"type-text", "--app", "TextEdit", "--text", "hello", "--json"}, want: func(req computeruse.ActionRequest) bool {
			return req.Kind == computeruse.ActionTypeText && req.Text == "hello"
		}},
		{name: "paste text", args: []string{"paste-text", "--app", "TextEdit", "--text", "hello", "--json"}, want: func(req computeruse.ActionRequest) bool {
			return req.Kind == computeruse.ActionPasteText && req.Text == "hello"
		}},
		{name: "press key", args: []string{"press-key", "--app", "TextEdit", "--key", "return", "--json"}, want: func(req computeruse.ActionRequest) bool {
			return req.Kind == computeruse.ActionPressKey && req.Key == "return"
		}},
		{name: "hotkey", args: []string{"hotkey", "--app", "TextEdit", "--key", "cmd+a", "--json"}, want: func(req computeruse.ActionRequest) bool {
			return req.Kind == computeruse.ActionHotkey && req.Key == "cmd+a"
		}},
		{name: "scroll", args: []string{"scroll", "--app", "TextEdit", "--x", "5", "--y", "6", "--direction", "down", "--amount", "240", "--json"}, want: func(req computeruse.ActionRequest) bool {
			return req.Kind == computeruse.ActionScroll && req.Direction == "down" && req.Amount == 240
		}},
		{name: "drag", args: []string{"drag", "--app", "TextEdit", "--from-x", "1", "--from-y", "2", "--to-x", "30", "--to-y", "40", "--json"}, want: func(req computeruse.ActionRequest) bool {
			return req.Kind == computeruse.ActionDrag && req.FromX != nil && *req.FromX == 1 && req.ToY != nil && *req.ToY == 40
		}},
		{name: "secondary action", args: []string{"perform-secondary-action", "--app", "TextEdit", "--element-index", "12", "--action", "AXShowMenu", "--json"}, want: func(req computeruse.ActionRequest) bool {
			return req.Kind == computeruse.ActionSecondary && req.ElementIndex != nil && *req.ElementIndex == 12 && req.SecondaryAction == "AXShowMenu"
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			service := &fakeService{}
			if err := run(context.Background(), tc.args, service, strings.NewReader(""), &bytes.Buffer{}); err != nil {
				t.Fatalf("run: %v", err)
			}
			if len(service.actionRequests) != 1 || !tc.want(service.actionRequests[0]) {
				t.Fatalf("action requests = %+v", service.actionRequests)
			}
		})
	}
}

func TestJSONErrorEnvelopeKeepsStableCode(t *testing.T) {
	service := &fakeService{listAppsErr: computeruse.NewError(computeruse.CodeAccessibilityDenied, "grant Accessibility", nil)}
	var out bytes.Buffer
	err := run(context.Background(), []string{"list-apps", "--json"}, service, strings.NewReader(""), &out)
	if computeruse.ErrorCode(err) != computeruse.CodeAccessibilityDenied {
		t.Fatalf("run error = %v", err)
	}
	var response struct {
		OK    bool `json:"ok"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if decodeErr := json.Unmarshal(out.Bytes(), &response); decodeErr != nil {
		t.Fatalf("decode output %q: %v", out.String(), decodeErr)
	}
	if response.OK || response.Error.Code != computeruse.CodeAccessibilityDenied {
		t.Fatalf("response = %+v", response)
	}
}

func TestSubcommandHelpPrintsFlagsWithoutCallingService(t *testing.T) {
	service := &fakeService{}
	var out bytes.Buffer
	if err := run(context.Background(), []string{"click", "--help"}, service, strings.NewReader(""), &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "--element-index") || !strings.Contains(out.String(), "--window-index") || strings.Contains(out.String(), `\n`) {
		t.Fatalf("help output = %q", out.String())
	}
	if len(service.actionRequests) != 0 {
		t.Fatal("service was called for help")
	}
}

func TestHelpAndJSONIntentAreOrderIndependent(t *testing.T) {
	service := &fakeService{}
	var help bytes.Buffer
	if err := run(context.Background(), []string{"click", "--json", "--help"}, service, strings.NewReader(""), &help); err != nil {
		t.Fatalf("help run: %v", err)
	}
	if !strings.Contains(help.String(), "Usage: everyapi computer click") {
		t.Fatalf("help output = %q", help.String())
	}
	var failure bytes.Buffer
	if err := run(context.Background(), []string{"capabilities", "--unknown", "--json"}, service, strings.NewReader(""), &failure); computeruse.ErrorCode(err) != computeruse.CodeInvalidArgument {
		t.Fatalf("invalid flag error = %v", err)
	}
	var response struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(failure.Bytes(), &response); err != nil || response.OK {
		t.Fatalf("JSON failure = %q, decode error = %v", failure.String(), err)
	}
}

func TestHelpTokenUsedAsFlagValueDoesNotTriggerHelp(t *testing.T) {
	service := &fakeService{}
	var out bytes.Buffer
	if err := run(context.Background(), []string{"type-text", "--app", "TextEdit", "--text", "--help", "--json"}, service, strings.NewReader(""), &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(service.actionRequests) != 1 || service.actionRequests[0].Text != "--help" {
		t.Fatalf("action requests = %+v", service.actionRequests)
	}
	var response struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(out.Bytes(), &response); err != nil || !response.OK {
		t.Fatalf("JSON output = %q, decode error = %v", out.String(), err)
	}
}

func TestJSONTokenUsedAsFlagValueDoesNotRequestJSONOnParseError(t *testing.T) {
	service := &fakeService{}
	var out bytes.Buffer
	err := run(context.Background(), []string{"type-text", "--app", "TextEdit", "--text", "--json", "trailing"}, service, strings.NewReader(""), &out)
	if computeruse.ErrorCode(err) != computeruse.CodeInvalidArgument {
		t.Fatalf("run error = %v (%q)", err, computeruse.ErrorCode(err))
	}
	if out.Len() != 0 {
		t.Fatalf("plain error unexpectedly wrote JSON = %q", out.String())
	}
	if len(service.actionRequests) != 0 {
		t.Fatal("service was called for invalid arguments")
	}
}

func TestJSONBooleanSyntaxControlsErrorEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		wantJSON bool
	}{
		{name: "true", args: []string{"--json=true"}, wantJSON: true},
		{name: "false", args: []string{"--json=false"}, wantJSON: false},
		{name: "last false wins", args: []string{"--json", "--json=false"}, wantJSON: false},
		{name: "last true wins", args: []string{"--json=false", "--json"}, wantJSON: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			args := append([]string{"capabilities"}, tc.args...)
			args = append(args, "--unknown")
			err := run(context.Background(), args, &fakeService{}, strings.NewReader(""), &out)
			if computeruse.ErrorCode(err) != computeruse.CodeInvalidArgument {
				t.Fatalf("run error = %v (%q)", err, computeruse.ErrorCode(err))
			}
			if gotJSON := out.Len() != 0; gotJSON != tc.wantJSON {
				t.Fatalf("output = %q, want JSON = %t", out.String(), tc.wantJSON)
			}
		})
	}
}

func TestBareHelpUsedAsFlagValueDoesNotTriggerHelp(t *testing.T) {
	service := &fakeService{}
	if err := run(context.Background(), []string{"type-text", "--app", "TextEdit", "--text", "help"}, service, strings.NewReader(""), &bytes.Buffer{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(service.actionRequests) != 1 || service.actionRequests[0].Text != "help" {
		t.Fatalf("action requests = %+v", service.actionRequests)
	}
}

func TestBareHelpInArgumentPositionIsNotAHelpFlag(t *testing.T) {
	for _, args := range [][]string{{"click", "help"}, {"click", "--bogus", "help"}} {
		var out bytes.Buffer
		err := run(context.Background(), args, &fakeService{}, strings.NewReader(""), &out)
		if computeruse.ErrorCode(err) != computeruse.CodeInvalidArgument {
			t.Fatalf("run(%v) error = %v (%q)", args, err, computeruse.ErrorCode(err))
		}
		if out.Len() != 0 {
			t.Fatalf("run(%v) printed subcommand help: %q", args, out.String())
		}
	}
}

func TestSingleDashFlagValuesAreSkippedByIntentScan(t *testing.T) {
	service := &fakeService{}
	if err := run(context.Background(), []string{"type-text", "-app", "TextEdit", "-text", "help"}, service, strings.NewReader(""), &bytes.Buffer{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(service.actionRequests) != 1 || service.actionRequests[0].Text != "help" {
		t.Fatalf("action requests = %+v", service.actionRequests)
	}
	var out bytes.Buffer
	err := run(context.Background(), []string{"capabilities", "-json=true", "-unknown"}, &fakeService{}, strings.NewReader(""), &out)
	if computeruse.ErrorCode(err) != computeruse.CodeInvalidArgument || out.Len() == 0 {
		t.Fatalf("single-dash JSON error = %v (%q), output = %q", err, computeruse.ErrorCode(err), out.String())
	}
}

func TestRenderPlainRefreshFailureDoesNotStartWithBlankLine(t *testing.T) {
	var out bytes.Buffer
	state := computeruse.State{RefreshError: computeruse.NewError(computeruse.CodeWindowNotFound, "window closed", nil)}
	if err := renderPlain(&out, "click", state); err != nil {
		t.Fatalf("renderPlain: %v", err)
	}
	if got, want := out.String(), "Action completed; refreshed state unavailable: window closed\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestRenderPlainPrintsScreenshotPath(t *testing.T) {
	var out bytes.Buffer
	state := computeruse.State{Screenshot: &computeruse.ScreenshotAttachment{Path: "/tmp/shot.png", Format: "png", Width: 12, Height: 34}}
	if err := renderPlain(&out, "click", state); err != nil {
		t.Fatalf("renderPlain: %v", err)
	}
	if got, want := out.String(), "screenshot: /tmp/shot.png (12x34)\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestRenderPlainPrintsScreenshotError(t *testing.T) {
	var out bytes.Buffer
	state := computeruse.State{ScreenshotError: computeruse.NewError(computeruse.CodeInternal, "screen recording permission denied", nil)}
	if err := renderPlain(&out, "click", state); err != nil {
		t.Fatalf("renderPlain: %v", err)
	}
	if got, want := out.String(), "screenshot unavailable: screen recording permission denied\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestActionRejectsIrrelevantFlagsBeforeCallingService(t *testing.T) {
	service := &fakeService{}
	err := run(context.Background(), []string{"press-key", "--app", "TextEdit", "--key", "return", "--text", "ignored"}, service, strings.NewReader(""), &bytes.Buffer{})
	if computeruse.ErrorCode(err) != computeruse.CodeInvalidArgument {
		t.Fatalf("irrelevant flag error = %v (%q)", err, computeruse.ErrorCode(err))
	}
	if len(service.actionRequests) != 0 {
		t.Fatal("service was called with irrelevant flags")
	}
}

// computerFlagSample returns a parseable value for one flag name, so probing a flag exercises whether it is REGISTERED rather than whether its value is well formed.
func computerFlagSample(t *testing.T, name string) string {
	t.Helper()
	switch name {
	case "json", "no-screenshot", "restore-window", "text-stdin", "value-stdin":
		return "true"
	case "app":
		return "TextEdit"
	case "session":
		return "probe"
	case "request":
		return "accessibility"
	case "out":
		return filepath.Join(t.TempDir(), "probe.png")
	case "direction":
		return "down"
	case "key":
		return "return"
	case "action":
		return "AXPress"
	case "mouse-button":
		return "left"
	case "modifiers":
		return "cmd"
	case "text", "value":
		return "probe"
	}
	return "1"
}

// TestCommandFlagsMatchTheParser is the anti-drift guard behind the agent command schema. cmd/workspace advertises computer flags straight from CommandFlags, so CommandFlags must describe what the dispatcher really parses: every advertised flag has to survive flag parsing, and every flag advertised for some OTHER computer command has to be refused — either as undefined or as irrelevant for this kind. Without this, the schema can promise flags like --pages or --restore-window that hard-fail at argument parse.
func TestCommandFlagsMatchTheParser(t *testing.T) {
	universe := map[string]bool{}
	for _, command := range CommandNames() {
		for _, name := range CommandFlags(command) {
			universe[name] = true
		}
	}
	for _, command := range CommandNames() {
		advertised := map[string]bool{}
		for _, name := range CommandFlags(command) {
			advertised[name] = true
		}
		if len(advertised) == 0 {
			t.Fatalf("CommandFlags(%q) is empty", command)
		}
		for name := range universe {
			name := name
			t.Run(command+"/"+name, func(t *testing.T) {
				args := []string{command, "--" + name + "=" + computerFlagSample(t, name)}
				if command != "capabilities" && command != "list-apps" && command != "permissions" && name != "app" {
					args = append(args, "--app=TextEdit")
				}
				err := run(context.Background(), args, &fakeService{}, strings.NewReader("probe"), &bytes.Buffer{})
				message := ""
				if err != nil {
					message = err.Error()
				}
				refused := strings.Contains(message, "flag provided but not defined") || strings.Contains(message, "does not accept")
				if advertised[name] && refused {
					t.Fatalf("computer %s advertises --%s but the parser refuses it: %s", command, name, message)
				}
				if !advertised[name] && !refused {
					t.Fatalf("computer %s accepts --%s without advertising it in CommandFlags", command, name)
				}
			})
		}
	}
}

// TestCommandNamesMatchDispatch keeps CommandNames in step with the dispatcher's switch, so a subcommand can never be implemented yet stay invisible to the agent schema built from this list.
func TestCommandNamesMatchDispatch(t *testing.T) {
	for _, command := range CommandNames() {
		err := run(context.Background(), []string{command}, &fakeService{}, strings.NewReader(""), &bytes.Buffer{})
		if err != nil && strings.Contains(err.Error(), "unknown computer command") {
			t.Errorf("CommandNames lists %q but dispatch rejects it", command)
		}
		if CommandFlags(command) == nil {
			t.Errorf("CommandFlags(%q) = nil", command)
		}
	}
	if err := run(context.Background(), []string{"teleport"}, &fakeService{}, strings.NewReader(""), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "unknown computer command") {
		t.Errorf("unknown subcommand error = %v", err)
	}
}
