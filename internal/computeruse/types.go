package computeruse

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	CodeUnsupportedPlatform  = "unsupported_platform"
	CodeDependencyMissing    = "dependency_missing"
	CodeAccessibilityDenied  = "accessibility_denied"
	CodeAutomationDenied     = "automation_denied"
	CodeAppNotFound          = "app_not_found"
	CodeAppStale             = "app_stale"
	CodeAppAmbiguous         = "app_ambiguous"
	CodeAppBlocked           = "app_blocked"
	CodeWindowNotFound       = "window_not_found"
	CodeWindowStale          = "window_stale"
	CodeWindowNotFocused     = "window_not_focused"
	CodeElementNotFound      = "element_not_found"
	CodeElementStale         = "element_stale"
	CodeInvalidArgument      = "invalid_argument"
	CodeActionNotSupported   = "action_not_supported"
	CodeActionTimeout        = "action_timeout"
	CodeActionOutcomeUnknown = "action_outcome_unknown"
	CodeSensitiveText        = "sensitive_text"
	CodeInternal             = "internal_error"
	computerProtocolVersion  = 1
	computerProviderVersion  = "1.0.0"
	snapshotTTL              = 2 * time.Minute
)

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	cause   error
}

func NewError(code, message string, cause error) *Error {
	return &Error{Code: code, Message: message, cause: cause}
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.cause }

func ErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var coded *Error
	if errors.As(err, &coded) {
		return coded.Code
	}
	return CodeInternal
}

type PermissionState string

const (
	PermissionGranted PermissionState = "granted"
	PermissionDenied  PermissionState = "denied"
	PermissionUnknown PermissionState = "unknown"
)

type PermissionStatus struct {
	Accessibility PermissionState `json:"accessibility"`
	Automation    PermissionState `json:"automation"`
	Screenshot    PermissionState `json:"screenshot"`
}

type Capabilities struct {
	Provider        string `json:"provider"`
	ProviderVersion string `json:"providerVersion"`
	ProtocolVersion int    `json:"protocolVersion"`
	Platform        string `json:"platform"`
	Supports        struct {
		Apps struct {
			List      bool `json:"list"`
			BundleIDs bool `json:"bundleIds"`
			PIDs      bool `json:"pids"`
		} `json:"apps"`
		Windows struct {
			List          bool `json:"list"`
			TargetByID    bool `json:"targetById"`
			TargetByIndex bool `json:"targetByIndex"`
		} `json:"windows"`
		Observation struct {
			AccessibilityTree bool `json:"accessibilityTree"`
			Screenshot        bool `json:"screenshot"`
			ElementFrames     bool `json:"elementFrames"`
		} `json:"observation"`
		Actions struct {
			Click         bool `json:"click"`
			SetValue      bool `json:"setValue"`
			TypeText      bool `json:"typeText"`
			PressKey      bool `json:"pressKey"`
			Hotkey        bool `json:"hotkey"`
			Scroll        bool `json:"scroll"`
			Drag          bool `json:"drag"`
			PerformAction bool `json:"performAction"`
			PasteText     bool `json:"pasteText"`
		} `json:"actions"`
	} `json:"supports"`
}

type Frame struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type App struct {
	Name        string `json:"name"`
	BundleID    string `json:"bundleId,omitempty"`
	PID         int    `json:"pid"`
	Frontmost   bool   `json:"frontmost"`
	WindowCount int    `json:"windowCount"`
}

type Window struct {
	ID          int    `json:"id"`
	Index       int    `json:"index"`
	Title       string `json:"title"`
	Frame       Frame  `json:"frame"`
	Focused     bool   `json:"focused"`
	Fingerprint string `json:"-"`
}

type Element struct {
	Index       int      `json:"index"`
	Path        []int    `json:"-"`
	Role        string   `json:"role"`
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	Value       string   `json:"value,omitempty"`
	Frame       Frame    `json:"frame"`
	Actions     []string `json:"actions,omitempty"`
	Fingerprint string   `json:"-"`
}

type Snapshot struct {
	TreeText     string    `json:"treeText"`
	ElementCount int       `json:"elementCount"`
	Walked       int       `json:"walked,omitempty"`
	Truncated    bool      `json:"truncated,omitempty"`
	Elements     []Element `json:"elements,omitempty"`
}

// ScreenshotAttachment is a best-effort capture taken after a GetAppState or
// Perform call, written to a temporary file rather than inlined as base64 —
// inlining would bloat every action response by the image's full encoded
// size even when the caller never looks at it. The file lives under the
// OS temp directory and is swept once it is older than screenshotFileTTL;
// callers that want to keep it should copy it out promptly.
type ScreenshotAttachment struct {
	Path   string `json:"path"`
	Format string `json:"format"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type State struct {
	App             App                   `json:"app"`
	Window          Window                `json:"window"`
	Snapshot        Snapshot              `json:"snapshot"`
	RefreshError    *Error                `json:"refreshError,omitempty"`
	Screenshot      *ScreenshotAttachment `json:"screenshot,omitempty"`
	ScreenshotError *Error                `json:"screenshotError,omitempty"`
}

type Target struct {
	App    App
	Window Window
}

type StateRequest struct {
	App          string
	WindowIndex  *int
	WindowID     *int
	SessionID    string
	NoScreenshot bool
}

type ActionKind string

const (
	ActionClick     ActionKind = "click"
	ActionSetValue  ActionKind = "set-value"
	ActionTypeText  ActionKind = "type-text"
	ActionPasteText ActionKind = "paste-text"
	ActionPressKey  ActionKind = "press-key"
	ActionHotkey    ActionKind = "hotkey"
	ActionScroll    ActionKind = "scroll"
	ActionDrag      ActionKind = "drag"
	ActionSecondary ActionKind = "perform-secondary-action"
)

type ActionRequest struct {
	App              string
	WindowIndex      *int
	WindowID         *int
	Kind             ActionKind
	ElementIndex     *int
	FromElementIndex *int
	ToElementIndex   *int
	X                *int
	Y                *int
	FromX            *int
	FromY            *int
	ToX              *int
	ToY              *int
	Text             string
	Key              string
	Direction        string
	Amount           int
	SecondaryAction  string
	MouseButton      string
	ClickCount       *int
	Modifiers        string
	RestoreWindow    bool
	SessionID        string
	NoScreenshot     bool
}

type CachedElement struct {
	Index       int      `json:"index"`
	Path        []int    `json:"path"`
	Role        string   `json:"role"`
	Frame       Frame    `json:"frame"`
	Fingerprint string   `json:"fingerprint"`
	Actions     []string `json:"actions,omitempty"`
}

type SnapshotRecord struct {
	SessionID         string          `json:"sessionId,omitempty"`
	PID               int             `json:"pid"`
	BundleID          string          `json:"bundleId"`
	WindowID          int             `json:"windowId"`
	WindowFingerprint string          `json:"windowFingerprint"`
	CreatedAt         time.Time       `json:"createdAt"`
	Elements          []CachedElement `json:"elements"`
}

func (r SnapshotRecord) Key() string { return snapshotKey(r.SessionID, r.PID, r.WindowID) }

// snapshotKey namespaces the cache by session so two concurrent computer-use
// workflows driving the same app/window don't stomp on each other's element
// indexes — the empty, unnamespaced session (the default when a caller never
// passes --session) keeps its original two-part key so existing on-disk
// caches from before session support remain valid.
func snapshotKey(sessionID string, pid, windowID int) string {
	if sessionID == "" {
		return fmt.Sprintf("%d-%d", pid, windowID)
	}
	return fmt.Sprintf("%s_%d-%d", sessionID, pid, windowID)
}

type PerformRequest struct {
	Target                    Target
	Kind                      ActionKind
	ExpectedWindowFingerprint string
	ExpectedElement           *CachedElement
	ExpectedFromElement       *CachedElement
	ExpectedToElement         *CachedElement
	X                         *int
	Y                         *int
	FromX                     *int
	FromY                     *int
	ToX                       *int
	ToY                       *int
	Text                      string
	Key                       string
	Direction                 string
	Amount                    int
	SecondaryAction           string
	MouseButton               string
	ClickCount                *int
	Modifiers                 string
	RestoreWindow             bool
}

type Provider interface {
	Capabilities(context.Context) (Capabilities, error)
	Permissions(context.Context) (PermissionStatus, error)
	ListApps(context.Context) ([]App, error)
	ListWindows(context.Context, App) ([]Window, error)
	GetState(context.Context, Target) (State, error)
	Perform(context.Context, PerformRequest) error
	Screenshot(context.Context, Target) ([]byte, error)
}

type SnapshotStore interface {
	Save(context.Context, SnapshotRecord) error
	Load(ctx context.Context, sessionID string, pid, windowID int) (SnapshotRecord, error)
	Delete(ctx context.Context, sessionID string, pid, windowID int) error
}

type OperationLocker interface {
	Lock(context.Context) (func(), error)
}

var ErrSnapshotNotFound = errors.New("computer-use snapshot not found")
