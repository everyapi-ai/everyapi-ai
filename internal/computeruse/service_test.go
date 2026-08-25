package computeruse

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeProvider struct {
	apps                []App
	windows             []Window
	state               State
	stateErr            error
	performErr          error
	performed           []PerformRequest
	screenshotPNG       []byte
	screenshotErr       error
	screenshotCalls     int
	mu                  sync.Mutex
	listCalls           int
	requestedPermission string
}

func (f *fakeProvider) Capabilities(context.Context) (Capabilities, error) {
	return Capabilities{Provider: "fake", Platform: "test", ProtocolVersion: 1}, nil
}

func (f *fakeProvider) Permissions(context.Context) (PermissionStatus, error) {
	return PermissionStatus{Accessibility: PermissionGranted}, nil
}

func (f *fakeProvider) RequestPermission(_ context.Context, kind string) error {
	f.requestedPermission = kind
	return nil
}

func (f *fakeProvider) ListApps(context.Context) ([]App, error) {
	f.mu.Lock()
	f.listCalls++
	f.mu.Unlock()
	return append([]App(nil), f.apps...), nil
}

func (f *fakeProvider) ListWindows(context.Context, App) ([]Window, error) {
	return append([]Window(nil), f.windows...), nil
}

func (f *fakeProvider) GetState(context.Context, Target) (State, error) {
	return f.state, f.stateErr
}

func (f *fakeProvider) Perform(_ context.Context, req PerformRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.performed = append(f.performed, req)
	return f.performErr
}

func (f *fakeProvider) Screenshot(context.Context, Target) ([]byte, error) {
	f.mu.Lock()
	f.screenshotCalls++
	f.mu.Unlock()
	if f.screenshotErr != nil {
		return nil, f.screenshotErr
	}
	if f.screenshotPNG != nil {
		return f.screenshotPNG, nil
	}
	return fakePNG(), nil
}

// fakePNG is a real, decodable 3x2 PNG — distinct width and height so a test
// asserting on ScreenshotAttachment.Width/Height can't pass by accident with
// the dimensions swapped.
func fakePNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func (f *fakeProvider) lastPerform() (PerformRequest, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.performed) == 0 {
		return PerformRequest{}, false
	}
	return f.performed[len(f.performed)-1], true
}

type memoryStore struct {
	records map[string]SnapshotRecord
}

type blockingListProvider struct {
	*fakeProvider
	entered chan struct{}
	release chan struct{}
}

type failingOperationLockerStore struct {
	*memoryStore
}

func (failingOperationLockerStore) Lock(context.Context) (func(), error) {
	return nil, errors.New("operation lock should not be acquired")
}

func (p *blockingListProvider) ListApps(ctx context.Context) ([]App, error) {
	close(p.entered)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.release:
		return p.fakeProvider.ListApps(ctx)
	}
}

func newMemoryStore() *memoryStore {
	return &memoryStore{records: make(map[string]SnapshotRecord)}
}

func (s *memoryStore) Save(_ context.Context, record SnapshotRecord) error {
	s.records[record.Key()] = record
	return nil
}

func (s *memoryStore) Load(_ context.Context, sessionID string, pid, windowID int) (SnapshotRecord, error) {
	record, ok := s.records[snapshotKey(sessionID, pid, windowID)]
	if !ok {
		return SnapshotRecord{}, ErrSnapshotNotFound
	}
	return record, nil
}

func (s *memoryStore) Delete(_ context.Context, sessionID string, pid, windowID int) error {
	delete(s.records, snapshotKey(sessionID, pid, windowID))
	return nil
}

func fixtureProvider() *fakeProvider {
	app := App{Name: "TextEdit", BundleID: "com.apple.TextEdit", PID: 42, Frontmost: true, WindowCount: 1}
	window := Window{ID: 7, Index: 0, Title: "Untitled", Frame: Frame{X: 100, Y: 80, Width: 800, Height: 600}, Fingerprint: "window-fingerprint"}
	return &fakeProvider{
		apps:    []App{app},
		windows: []Window{window},
		state: State{
			App:    app,
			Window: window,
			Snapshot: Snapshot{
				TreeText: "[12] AXTextArea value=hello",
				Elements: []Element{{Index: 12, Path: []int{3, 1}, Role: "AXTextArea", Value: "hello", Frame: Frame{X: 10, Y: 20, Width: 200, Height: 80}, Fingerprint: "element-fingerprint", Actions: []string{"AXPress", "AXSetValue"}}},
			},
		},
	}
}

func testStripeCredential() string {
	return "sk_" + "test_" + strings.Repeat("1", 20)
}

func TestCapabilitiesAndPermissionsDoNotRequireOperationLock(t *testing.T) {
	provider := fixtureProvider()
	store := failingOperationLockerStore{memoryStore: newMemoryStore()}
	service := NewService(provider, store, time.Now)
	if _, err := service.Capabilities(context.Background()); err != nil {
		t.Fatalf("Capabilities acquired the operation lock: %v", err)
	}
	if _, err := service.Permissions(context.Background()); err != nil {
		t.Fatalf("Permissions acquired the operation lock: %v", err)
	}
}

func TestRequestPermissionValidatesTheClosedPermissionSet(t *testing.T) {
	provider := &fakeProvider{}
	service := NewService(provider, newMemoryStore(), time.Now)
	if err := service.RequestPermission(context.Background(), "screen-recording"); err != nil {
		t.Fatalf("request permission: %v", err)
	}
	if provider.requestedPermission != "screen-recording" {
		t.Fatalf("requested permission = %q", provider.requestedPermission)
	}
	if err := service.RequestPermission(context.Background(), "camera"); ErrorCode(err) != CodeInvalidArgument {
		t.Fatalf("camera request error = %v", err)
	}
}

func TestResolveAppSupportsBundleNameAndPIDAndRejectsAmbiguity(t *testing.T) {
	provider := fixtureProvider()
	provider.apps = append(provider.apps, App{Name: "TextEdit", BundleID: "example.second.TextEdit", PID: 84})
	service := NewService(provider, newMemoryStore(), time.Now)

	for _, selector := range []string{"com.apple.TextEdit", "pid:42"} {
		app, err := service.ResolveApp(context.Background(), selector)
		if err != nil {
			t.Fatalf("ResolveApp(%q): %v", selector, err)
		}
		if app.PID != 42 {
			t.Fatalf("ResolveApp(%q) PID = %d, want 42", selector, app.PID)
		}
	}

	_, err := service.ResolveApp(context.Background(), "TextEdit")
	if ErrorCode(err) != CodeAppAmbiguous {
		t.Fatalf("ambiguous name error = %v (%q), want %q", err, ErrorCode(err), CodeAppAmbiguous)
	}
	for _, candidate := range []string{"com.apple.TextEdit (pid:42)", "example.second.TextEdit (pid:84)"} {
		if !strings.Contains(err.Error(), candidate) {
			t.Fatalf("ambiguous name error %q is missing candidate %q", err, candidate)
		}
	}
}

func TestResolveAppBlocksSensitiveApplicationsByBundleID(t *testing.T) {
	provider := fixtureProvider()
	secret := testStripeCredential()
	provider.apps = []App{{Name: "\x1b[31m" + secret, BundleID: "com.apple.Terminal", PID: 55}}
	service := NewService(provider, newMemoryStore(), time.Now)

	_, err := service.ResolveApp(context.Background(), "com.apple.Terminal")
	if ErrorCode(err) != CodeAppBlocked {
		t.Fatalf("blocked app error = %v (%q), want %q", err, ErrorCode(err), CodeAppBlocked)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "\x1b") || !strings.Contains(err.Error(), "[REDACTED:stripe_key]") {
		t.Fatalf("blocked app error was not redacted: %q", err)
	}
}

func TestResolveAppBlocksSensitiveBundleIDsCaseInsensitively(t *testing.T) {
	provider := fixtureProvider()
	provider.apps = []App{{Name: "Terminal", BundleID: "COM.APPLE.TERMINAL", PID: 55}}
	service := NewService(provider, newMemoryStore(), time.Now)
	_, err := service.ResolveApp(context.Background(), "COM.APPLE.TERMINAL")
	if ErrorCode(err) != CodeAppBlocked {
		t.Fatalf("blocked app error = %v (%q), want %q", err, ErrorCode(err), CodeAppBlocked)
	}
}

func TestGetAppStateCachesOnlyOpaqueElementIdentityAndRedactsSecrets(t *testing.T) {
	provider := fixtureProvider()
	secret := testStripeCredential()
	provider.state.Snapshot.TreeText = "token \x1b[31m" + secret + "\x1b[0m"
	provider.state.Snapshot.Elements[0].Value = "\x1b[31m" + secret + "\x1b[0m"
	store := newMemoryStore()
	now := time.Date(2026, 8, 20, 1, 2, 3, 0, time.UTC)
	service := NewService(provider, store, func() time.Time { return now })

	state, err := service.GetAppState(context.Background(), StateRequest{App: "com.apple.TextEdit", WindowIndex: intPtr(0)})
	if err != nil {
		t.Fatalf("GetAppState: %v", err)
	}
	if state.Snapshot.TreeText != "token [REDACTED:stripe_key]" {
		t.Fatalf("TreeText = %q", state.Snapshot.TreeText)
	}
	if state.Snapshot.Elements[0].Value != "[REDACTED:stripe_key]" {
		t.Fatalf("element value = %q", state.Snapshot.Elements[0].Value)
	}
	record, err := store.Load(context.Background(), "", 42, 7)
	if err != nil {
		t.Fatalf("Load cached snapshot: %v", err)
	}
	if record.CreatedAt != now || len(record.Elements) != 1 {
		t.Fatalf("cached record = %+v", record)
	}
	if record.Elements[0].Fingerprint != "element-fingerprint" || record.Elements[0].Role != "AXTextArea" {
		t.Fatalf("cached element identity = %+v", record.Elements[0])
	}
	if record.Elements[0].Path[0] != 3 {
		t.Fatalf("cached element path = %v", record.Elements[0].Path)
	}
}

func TestElementActionUsesFreshCachedIdentity(t *testing.T) {
	provider := fixtureProvider()
	store := newMemoryStore()
	now := time.Date(2026, 8, 20, 1, 2, 3, 0, time.UTC)
	service := NewService(provider, store, func() time.Time { return now })
	if _, err := service.GetAppState(context.Background(), StateRequest{App: "com.apple.TextEdit", WindowIndex: intPtr(0)}); err != nil {
		t.Fatalf("GetAppState: %v", err)
	}

	_, err := service.Perform(context.Background(), ActionRequest{App: "com.apple.TextEdit", WindowIndex: intPtr(0), Kind: ActionClick, ElementIndex: intPtr(12)})
	if err != nil {
		t.Fatalf("Perform: %v", err)
	}
	req, ok := provider.lastPerform()
	if !ok {
		t.Fatal("provider was not called")
	}
	if req.ExpectedElement == nil || req.ExpectedElement.Fingerprint != "element-fingerprint" || req.ExpectedWindowFingerprint != "window-fingerprint" {
		t.Fatalf("perform identity = %+v", req)
	}
}

func TestElementActionRejectsMissingAndExpiredSnapshots(t *testing.T) {
	provider := fixtureProvider()
	store := newMemoryStore()
	now := time.Date(2026, 8, 20, 1, 2, 3, 0, time.UTC)
	service := NewService(provider, store, func() time.Time { return now })
	req := ActionRequest{App: "com.apple.TextEdit", WindowIndex: intPtr(0), Kind: ActionClick, ElementIndex: intPtr(12)}

	_, err := service.Perform(context.Background(), req)
	if ErrorCode(err) != CodeElementStale {
		t.Fatalf("missing snapshot error = %v (%q), want %q", err, ErrorCode(err), CodeElementStale)
	}

	if _, err := service.GetAppState(context.Background(), StateRequest{App: "com.apple.TextEdit", WindowIndex: intPtr(0)}); err != nil {
		t.Fatalf("GetAppState: %v", err)
	}
	now = now.Add(snapshotTTL + time.Second)
	_, err = service.Perform(context.Background(), req)
	if ErrorCode(err) != CodeElementStale {
		t.Fatalf("expired snapshot error = %v (%q), want %q", err, ErrorCode(err), CodeElementStale)
	}
}

func TestOutboundTextRejectsDetectedCredentialsBeforeProviderCall(t *testing.T) {
	provider := fixtureProvider()
	service := NewService(provider, newMemoryStore(), time.Now)

	_, err := service.Perform(context.Background(), ActionRequest{App: "com.apple.TextEdit", WindowIndex: intPtr(0), Kind: ActionTypeText, Text: "github_pat_abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"})
	if ErrorCode(err) != CodeSensitiveText {
		t.Fatalf("secret text error = %v (%q), want %q", err, ErrorCode(err), CodeSensitiveText)
	}
	if _, ok := provider.lastPerform(); ok {
		t.Fatal("provider was called with secret-bearing text")
	}
}

func TestInvalidActionFailsBeforeProviderDiscovery(t *testing.T) {
	provider := fixtureProvider()
	service := NewService(provider, newMemoryStore(), time.Now)
	_, err := service.Perform(context.Background(), ActionRequest{App: "com.apple.TextEdit", Kind: ActionKind("launch")})
	if ErrorCode(err) != CodeInvalidArgument {
		t.Fatalf("invalid action error = %v (%q)", err, ErrorCode(err))
	}
	provider.mu.Lock()
	listCalls := provider.listCalls
	provider.mu.Unlock()
	if listCalls != 0 {
		t.Fatalf("provider ListApps calls = %d, want 0", listCalls)
	}
}

func TestInvalidTargetFailsBeforeProviderDiscovery(t *testing.T) {
	provider := fixtureProvider()
	service := NewService(provider, newMemoryStore(), time.Now)
	negative := -1
	_, err := service.GetAppState(context.Background(), StateRequest{App: "com.apple.TextEdit", WindowIndex: &negative})
	if ErrorCode(err) != CodeInvalidArgument {
		t.Fatalf("invalid target error = %v (%q)", err, ErrorCode(err))
	}
	provider.mu.Lock()
	listCalls := provider.listCalls
	provider.mu.Unlock()
	if listCalls != 0 {
		t.Fatalf("provider ListApps calls = %d, want 0", listCalls)
	}
}

func TestResolveTargetByWindowID(t *testing.T) {
	provider := fixtureProvider()
	provider.windows = append(provider.windows, Window{ID: 9, Index: 1, Title: "Second", Frame: Frame{X: 0, Y: 0, Width: 400, Height: 300}, Fingerprint: "second-window"})
	service := NewService(provider, newMemoryStore(), time.Now)
	target, err := service.resolveTarget(context.Background(), "com.apple.TextEdit", nil, intPtr(9))
	if err != nil {
		t.Fatalf("resolveTarget by window id: %v", err)
	}
	if target.Window.ID != 9 || target.Window.Index != 1 {
		t.Fatalf("resolveTarget by window id resolved %+v, want ID=9 Index=1", target.Window)
	}
}

func TestResolveTargetByWindowIDNotFound(t *testing.T) {
	provider := fixtureProvider()
	service := NewService(provider, newMemoryStore(), time.Now)
	_, err := service.resolveTarget(context.Background(), "com.apple.TextEdit", nil, intPtr(999))
	if ErrorCode(err) != CodeWindowNotFound {
		t.Fatalf("resolveTarget by unknown window id error = %v (%q), want %q", err, ErrorCode(err), CodeWindowNotFound)
	}
}

func TestScreenshotReturnsProviderBytesForResolvedWindow(t *testing.T) {
	provider := fixtureProvider()
	service := NewService(provider, newMemoryStore(), time.Now)
	got, err := service.Screenshot(context.Background(), StateRequest{App: "com.apple.TextEdit", WindowIndex: intPtr(0)})
	if err != nil {
		t.Fatalf("Screenshot: %v", err)
	}
	if !bytes.Equal(got, fakePNG()) {
		t.Fatalf("Screenshot bytes did not match the provider's PNG (len=%d)", len(got))
	}
}

func TestScreenshotRejectsInvalidTargetBeforeProviderDiscovery(t *testing.T) {
	provider := fixtureProvider()
	service := NewService(provider, newMemoryStore(), time.Now)
	negative := -1
	_, err := service.Screenshot(context.Background(), StateRequest{App: "com.apple.TextEdit", WindowIndex: &negative})
	if ErrorCode(err) != CodeInvalidArgument {
		t.Fatalf("Screenshot invalid target error = %v (%q)", err, ErrorCode(err))
	}
	provider.mu.Lock()
	listCalls := provider.listCalls
	provider.mu.Unlock()
	if listCalls != 0 {
		t.Fatalf("provider ListApps calls = %d, want 0", listCalls)
	}
}

func TestGetAppStateAttachesAScreenshotByDefault(t *testing.T) {
	provider := fixtureProvider()
	service := NewService(provider, newMemoryStore(), time.Now)
	state, err := service.GetAppState(context.Background(), StateRequest{App: "com.apple.TextEdit", WindowIndex: intPtr(0)})
	if err != nil {
		t.Fatalf("GetAppState: %v", err)
	}
	if state.ScreenshotError != nil {
		t.Fatalf("ScreenshotError = %v, want nil", state.ScreenshotError)
	}
	if state.Screenshot == nil {
		t.Fatal("Screenshot is nil, want an attachment")
	}
	if state.Screenshot.Format != "png" || state.Screenshot.Width != 3 || state.Screenshot.Height != 2 {
		t.Fatalf("Screenshot = %+v, want format=png width=3 height=2", state.Screenshot)
	}
	data, err := os.ReadFile(state.Screenshot.Path)
	if err != nil {
		t.Fatalf("read screenshot file: %v", err)
	}
	if !bytes.Equal(data, fakePNG()) {
		t.Fatal("screenshot file on disk does not match the provider's PNG bytes")
	}
	_ = os.Remove(state.Screenshot.Path)
}

func TestGetAppStateSkipsScreenshotWhenRequested(t *testing.T) {
	provider := fixtureProvider()
	service := NewService(provider, newMemoryStore(), time.Now)
	state, err := service.GetAppState(context.Background(), StateRequest{App: "com.apple.TextEdit", WindowIndex: intPtr(0), NoScreenshot: true})
	if err != nil {
		t.Fatalf("GetAppState: %v", err)
	}
	if state.Screenshot != nil || state.ScreenshotError != nil {
		t.Fatalf("state = %+v, want no screenshot and no screenshotError", state)
	}
	provider.mu.Lock()
	calls := provider.screenshotCalls
	provider.mu.Unlock()
	if calls != 0 {
		t.Fatalf("provider.Screenshot calls = %d, want 0", calls)
	}
}

func TestGetAppStateReportsScreenshotFailureWithoutFailingTheCall(t *testing.T) {
	provider := fixtureProvider()
	provider.screenshotErr = errors.New("screen recording permission denied")
	service := NewService(provider, newMemoryStore(), time.Now)
	state, err := service.GetAppState(context.Background(), StateRequest{App: "com.apple.TextEdit", WindowIndex: intPtr(0)})
	if err != nil {
		t.Fatalf("GetAppState: %v", err)
	}
	if state.Screenshot != nil {
		t.Fatalf("Screenshot = %+v, want nil", state.Screenshot)
	}
	if state.ScreenshotError == nil || !strings.Contains(state.ScreenshotError.Message, "screen recording permission denied") {
		t.Fatalf("ScreenshotError = %v, want it to mention the provider failure", state.ScreenshotError)
	}
}

func TestPerformAttachesAScreenshotToTheRefreshedState(t *testing.T) {
	provider := fixtureProvider()
	service := NewService(provider, newMemoryStore(), time.Now)
	x, y := 10, 20
	state, err := service.Perform(context.Background(), ActionRequest{App: "com.apple.TextEdit", WindowIndex: intPtr(0), Kind: ActionClick, X: &x, Y: &y})
	if err != nil {
		t.Fatalf("Perform: %v", err)
	}
	if state.Screenshot == nil {
		t.Fatal("Screenshot is nil, want an attachment after a successful action")
	}
	_ = os.Remove(state.Screenshot.Path)
}

func TestPerformSkipsScreenshotWhenRequested(t *testing.T) {
	provider := fixtureProvider()
	service := NewService(provider, newMemoryStore(), time.Now)
	x, y := 10, 20
	state, err := service.Perform(context.Background(), ActionRequest{App: "com.apple.TextEdit", WindowIndex: intPtr(0), Kind: ActionClick, X: &x, Y: &y, NoScreenshot: true})
	if err != nil {
		t.Fatalf("Perform: %v", err)
	}
	if state.Screenshot != nil {
		t.Fatalf("Screenshot = %+v, want nil", state.Screenshot)
	}
	provider.mu.Lock()
	calls := provider.screenshotCalls
	provider.mu.Unlock()
	if calls != 0 {
		t.Fatalf("provider.Screenshot calls = %d, want 0", calls)
	}
}

func TestClickRejectsInvalidMouseButtonAndClickCount(t *testing.T) {
	provider := fixtureProvider()
	service := NewService(provider, newMemoryStore(), time.Now)
	requests := []ActionRequest{
		{App: "com.apple.TextEdit", WindowIndex: intPtr(0), Kind: ActionClick, ElementIndex: intPtr(12), MouseButton: "double"},
		{App: "com.apple.TextEdit", WindowIndex: intPtr(0), Kind: ActionClick, ElementIndex: intPtr(12), ClickCount: intPtr(0)},
	}
	for _, request := range requests {
		if _, err := service.Perform(context.Background(), request); ErrorCode(err) != CodeInvalidArgument {
			t.Fatalf("click %+v error = %v (%q), want %q", request, err, ErrorCode(err), CodeInvalidArgument)
		}
	}
	if _, ok := provider.lastPerform(); ok {
		t.Fatal("provider performed an action with an invalid mouse-button/click-count")
	}
}

func TestClickForwardsMouseButtonClickCountModifiersAndRestoreWindow(t *testing.T) {
	provider := fixtureProvider()
	service := NewService(provider, newMemoryStore(), time.Now)
	if _, err := service.GetAppState(context.Background(), StateRequest{App: "com.apple.TextEdit", WindowIndex: intPtr(0)}); err != nil {
		t.Fatalf("GetAppState: %v", err)
	}
	request := ActionRequest{App: "com.apple.TextEdit", WindowIndex: intPtr(0), Kind: ActionClick, ElementIndex: intPtr(12), MouseButton: "right", ClickCount: intPtr(2), Modifiers: "shift+cmd", RestoreWindow: true}
	if _, err := service.Perform(context.Background(), request); err != nil {
		t.Fatalf("Perform: %v", err)
	}
	performed, ok := provider.lastPerform()
	if !ok {
		t.Fatal("provider did not receive the click")
	}
	if performed.MouseButton != "right" || performed.ClickCount == nil || *performed.ClickCount != 2 || performed.Modifiers != "shift+cmd" || !performed.RestoreWindow {
		t.Fatalf("forwarded PerformRequest = %+v, want MouseButton=right ClickCount=2 Modifiers=shift+cmd RestoreWindow=true", performed)
	}
}

func TestPasteTextRejectsEmptyAndSensitiveText(t *testing.T) {
	provider := fixtureProvider()
	service := NewService(provider, newMemoryStore(), time.Now)
	if _, err := service.Perform(context.Background(), ActionRequest{App: "com.apple.TextEdit", WindowIndex: intPtr(0), Kind: ActionPasteText, Text: ""}); ErrorCode(err) != CodeInvalidArgument {
		t.Fatalf("empty paste-text error = %v (%q)", err, ErrorCode(err))
	}
	if _, err := service.Perform(context.Background(), ActionRequest{App: "com.apple.TextEdit", WindowIndex: intPtr(0), Kind: ActionPasteText, Text: testStripeCredential()}); ErrorCode(err) != CodeSensitiveText {
		t.Fatalf("sensitive paste-text error = %v (%q), want %q", err, ErrorCode(err), CodeSensitiveText)
	}
	if _, ok := provider.lastPerform(); ok {
		t.Fatal("provider performed a rejected paste-text")
	}
}

func TestCoordinateActionCannotEscapeSelectedWindow(t *testing.T) {
	provider := fixtureProvider()
	service := NewService(provider, newMemoryStore(), time.Now)
	x, y := 801, 20
	_, err := service.Perform(context.Background(), ActionRequest{App: "com.apple.TextEdit", WindowIndex: intPtr(0), Kind: ActionClick, X: &x, Y: &y})
	if ErrorCode(err) != CodeInvalidArgument {
		t.Fatalf("out-of-bounds action error = %v (%q)", err, ErrorCode(err))
	}
	if _, ok := provider.lastPerform(); ok {
		t.Fatal("provider performed an out-of-bounds action")
	}
}

func TestActionRejectsMixedTargetShapesBeforeProviderDiscovery(t *testing.T) {
	provider := fixtureProvider()
	service := NewService(provider, newMemoryStore(), time.Now)
	x, y := 10, 20
	requests := []ActionRequest{
		{App: "com.apple.TextEdit", Kind: ActionScroll, Direction: "down", ElementIndex: intPtr(12), X: &x, Y: &y},
		{App: "com.apple.TextEdit", Kind: ActionDrag, FromElementIndex: intPtr(12), ToElementIndex: intPtr(13), FromX: &x},
	}
	for _, request := range requests {
		if _, err := service.Perform(context.Background(), request); ErrorCode(err) != CodeInvalidArgument {
			t.Fatalf("mixed action %+v error = %v (%q)", request, err, ErrorCode(err))
		}
	}
	provider.mu.Lock()
	listCalls := provider.listCalls
	provider.mu.Unlock()
	if listCalls != 0 {
		t.Fatalf("provider ListApps calls = %d, want 0", listCalls)
	}
}

func TestServicesSharingFileStoreSerializeProviderOperations(t *testing.T) {
	root := filepath.Join(t.TempDir(), "computer-state")
	firstProvider := &blockingListProvider{fakeProvider: fixtureProvider(), entered: make(chan struct{}), release: make(chan struct{})}
	secondProvider := fixtureProvider()
	first := NewService(firstProvider, NewFileStore(root), time.Now)
	second := NewService(secondProvider, NewFileStore(root), time.Now)
	firstDone := make(chan error, 1)
	go func() {
		_, err := first.ListApps(context.Background())
		firstDone <- err
	}()
	<-firstProvider.entered
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := second.ListApps(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("second ListApps error = %v, want context.Canceled", err)
	}
	secondProvider.mu.Lock()
	secondCalls := secondProvider.listCalls
	secondProvider.mu.Unlock()
	if secondCalls != 0 {
		t.Fatalf("second provider calls = %d, want 0", secondCalls)
	}
	close(firstProvider.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first ListApps: %v", err)
	}
}

func TestSecondaryActionMustBeAdvertisedByLatestSnapshot(t *testing.T) {
	provider := fixtureProvider()
	store := newMemoryStore()
	service := NewService(provider, store, time.Now)
	if _, err := service.GetAppState(context.Background(), StateRequest{App: "com.apple.TextEdit", WindowIndex: intPtr(0)}); err != nil {
		t.Fatalf("GetAppState: %v", err)
	}
	_, err := service.Perform(context.Background(), ActionRequest{App: "com.apple.TextEdit", WindowIndex: intPtr(0), Kind: ActionSecondary, ElementIndex: intPtr(12), SecondaryAction: "AXShowMenu"})
	if ErrorCode(err) != CodeActionNotSupported {
		t.Fatalf("unadvertised action error = %v (%q)", err, ErrorCode(err))
	}
	if _, ok := provider.lastPerform(); ok {
		t.Fatal("provider performed an unadvertised accessibility action")
	}
}

func TestCoordinateActionValidatesFreshResolvedWindowFingerprint(t *testing.T) {
	provider := fixtureProvider()
	service := NewService(provider, newMemoryStore(), time.Now)
	x, y := 10, 20
	if _, err := service.Perform(context.Background(), ActionRequest{App: "com.apple.TextEdit", WindowIndex: intPtr(0), Kind: ActionClick, X: &x, Y: &y}); err != nil {
		t.Fatalf("Perform: %v", err)
	}
	request, ok := provider.lastPerform()
	if !ok || request.ExpectedWindowFingerprint != "window-fingerprint" {
		t.Fatalf("perform request = %+v", request)
	}
}

func TestConcurrentSessionsMaintainIndependentElementCaches(t *testing.T) {
	provider := fixtureProvider()
	service := NewService(provider, newMemoryStore(), time.Now)
	if _, err := service.GetAppState(context.Background(), StateRequest{App: "com.apple.TextEdit", WindowIndex: intPtr(0), SessionID: "agent-a"}); err != nil {
		t.Fatalf("GetAppState agent-a: %v", err)
	}
	// A second workflow observes the same window under a different session
	// after the target's identity has moved on — this must land only in
	// agent-b's namespace, not overwrite agent-a's cached element.
	provider.state.Snapshot.Elements[0].Fingerprint = "different-fingerprint"
	if _, err := service.GetAppState(context.Background(), StateRequest{App: "com.apple.TextEdit", WindowIndex: intPtr(0), SessionID: "agent-b"}); err != nil {
		t.Fatalf("GetAppState agent-b: %v", err)
	}
	if _, err := service.Perform(context.Background(), ActionRequest{App: "com.apple.TextEdit", WindowIndex: intPtr(0), Kind: ActionClick, ElementIndex: intPtr(12), SessionID: "agent-a"}); err != nil {
		t.Fatalf("Perform agent-a after agent-b's overlapping observation: %v", err)
	}
	request, ok := provider.lastPerform()
	if !ok || request.ExpectedElement == nil || request.ExpectedElement.Fingerprint != "element-fingerprint" {
		t.Fatalf("agent-a's action used %+v, want its own cached element-fingerprint fingerprint", request)
	}
}

func TestSessionIDRejectsPathUnsafeCharacters(t *testing.T) {
	provider := fixtureProvider()
	service := NewService(provider, newMemoryStore(), time.Now)
	_, err := service.GetAppState(context.Background(), StateRequest{App: "com.apple.TextEdit", WindowIndex: intPtr(0), SessionID: "../escape"})
	if ErrorCode(err) != CodeInvalidArgument {
		t.Fatalf("path-unsafe session id error = %v (%q), want %q", err, ErrorCode(err), CodeInvalidArgument)
	}
}

func TestCachedElementRejectsBundleMismatchAfterPIDReuse(t *testing.T) {
	provider := fixtureProvider()
	store := newMemoryStore()
	service := NewService(provider, store, time.Now)
	if _, err := service.GetAppState(context.Background(), StateRequest{App: "com.apple.TextEdit", WindowIndex: intPtr(0)}); err != nil {
		t.Fatalf("GetAppState: %v", err)
	}
	record := store.records[snapshotKey("", 42, 7)]
	record.BundleID = "example.reused.pid"
	store.records[snapshotKey("", 42, 7)] = record
	_, err := service.Perform(context.Background(), ActionRequest{App: "com.apple.TextEdit", WindowIndex: intPtr(0), Kind: ActionClick, ElementIndex: intPtr(12)})
	if ErrorCode(err) != CodeElementStale {
		t.Fatalf("PID reuse error = %v (%q)", err, ErrorCode(err))
	}
	if _, ok := provider.lastPerform(); ok {
		t.Fatal("provider performed an action with a mismatched cached bundle")
	}
}

func TestGetAppStateRejectsProviderIdentityMismatch(t *testing.T) {
	provider := fixtureProvider()
	provider.state.App.BundleID = "example.reused.pid"
	service := NewService(provider, newMemoryStore(), time.Now)
	_, err := service.GetAppState(context.Background(), StateRequest{App: "com.apple.TextEdit", WindowIndex: intPtr(0)})
	if ErrorCode(err) != CodeAppStale {
		t.Fatalf("provider identity mismatch error = %v (%q)", err, ErrorCode(err))
	}
}

func TestSuccessfulActionDoesNotBecomeFailureWhenRefreshFails(t *testing.T) {
	provider := fixtureProvider()
	provider.stateErr = NewError(CodeWindowNotFound, "window closed", nil)
	store := newMemoryStore()
	store.records[snapshotKey("", 42, 7)] = SnapshotRecord{PID: 42, BundleID: "com.apple.TextEdit", WindowID: 7, CreatedAt: time.Now()}
	service := NewService(provider, store, time.Now)
	x, y := 10, 20
	state, err := service.Perform(context.Background(), ActionRequest{App: "com.apple.TextEdit", WindowIndex: intPtr(0), Kind: ActionClick, X: &x, Y: &y})
	if err != nil {
		t.Fatalf("Perform returned a retryable error after mutation: %v", err)
	}
	if state.RefreshError == nil || state.RefreshError.Code != CodeWindowNotFound {
		t.Fatalf("refresh error = %+v", state.RefreshError)
	}
	if _, ok := provider.lastPerform(); !ok {
		t.Fatal("provider action was not performed")
	}
	if _, err := store.Load(context.Background(), "", 42, 7); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("old snapshot survived the action: %v", err)
	}
}

func TestSetValueAllowsClearingAnElement(t *testing.T) {
	provider := fixtureProvider()
	service := NewService(provider, newMemoryStore(), time.Now)
	if _, err := service.GetAppState(context.Background(), StateRequest{App: "com.apple.TextEdit", WindowIndex: intPtr(0)}); err != nil {
		t.Fatalf("GetAppState: %v", err)
	}
	if _, err := service.Perform(context.Background(), ActionRequest{App: "com.apple.TextEdit", WindowIndex: intPtr(0), Kind: ActionSetValue, ElementIndex: intPtr(12), Text: ""}); err != nil {
		t.Fatalf("Perform empty set-value: %v", err)
	}
	request, ok := provider.lastPerform()
	if !ok || request.Kind != ActionSetValue || request.Text != "" {
		t.Fatalf("perform request = %+v", request)
	}
}

func TestRefreshFailureFallbackRedactsObservedTargetAndError(t *testing.T) {
	provider := fixtureProvider()
	secret := testStripeCredential()
	provider.apps[0].Name = "\x1b[31m" + secret
	provider.windows[0].Title = "\x1b[31m" + secret
	provider.stateErr = NewError(CodeWindowNotFound, "\x1b[31mwindow "+secret+" closed", nil)
	service := NewService(provider, newMemoryStore(), time.Now)
	x, y := 10, 20
	state, err := service.Perform(context.Background(), ActionRequest{App: "com.apple.TextEdit", WindowIndex: intPtr(0), Kind: ActionClick, X: &x, Y: &y})
	if err != nil {
		t.Fatalf("Perform: %v", err)
	}
	if state.RefreshError == nil {
		t.Fatal("refresh error is missing")
	}
	for label, value := range map[string]string{"app name": state.App.Name, "window title": state.Window.Title, "refresh error": state.RefreshError.Message} {
		if strings.Contains(value, secret) || strings.Contains(value, "\x1b") || !strings.Contains(value, "[REDACTED:stripe_key]") {
			t.Fatalf("%s was not sanitized and redacted: %q", label, value)
		}
	}
}

func TestWindowResolutionErrorRedactsObservedApplicationName(t *testing.T) {
	provider := fixtureProvider()
	secret := testStripeCredential()
	provider.apps[0].Name = "\x1b[31m" + secret
	provider.windows = nil
	service := NewService(provider, newMemoryStore(), time.Now)
	_, err := service.GetAppState(context.Background(), StateRequest{App: "com.apple.TextEdit"})
	if ErrorCode(err) != CodeWindowNotFound {
		t.Fatalf("GetAppState error = %v (%q)", err, ErrorCode(err))
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "\x1b") || !strings.Contains(err.Error(), "[REDACTED:stripe_key]") {
		t.Fatalf("window resolution error was not redacted: %q", err)
	}
}

func TestProviderStaleErrorIsPreservedAndNoRefreshRuns(t *testing.T) {
	provider := fixtureProvider()
	provider.performErr = NewError(CodeElementStale, "element changed", nil)
	service := NewService(provider, newMemoryStore(), time.Now)
	if _, err := service.GetAppState(context.Background(), StateRequest{App: "com.apple.TextEdit", WindowIndex: intPtr(0)}); err != nil {
		t.Fatalf("GetAppState: %v", err)
	}

	_, err := service.Perform(context.Background(), ActionRequest{App: "com.apple.TextEdit", WindowIndex: intPtr(0), Kind: ActionClick, ElementIndex: intPtr(12)})
	if ErrorCode(err) != CodeElementStale || !errors.Is(err, provider.performErr) {
		t.Fatalf("Perform error = %v (%q)", err, ErrorCode(err))
	}
}

func intPtr(v int) *int { return &v }
