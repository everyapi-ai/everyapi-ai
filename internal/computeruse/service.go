package computeruse

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Service struct {
	provider Provider
	store    SnapshotStore
	locker   OperationLocker
	now      func() time.Time
}

func NewService(provider Provider, store SnapshotStore, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	locker, ok := store.(OperationLocker)
	if !ok {
		locker = newLocalOperationLocker()
	}
	return &Service{provider: provider, store: store, locker: locker, now: now}
}

func (s *Service) Capabilities(ctx context.Context) (Capabilities, error) {
	return s.provider.Capabilities(ctx)
}

func (s *Service) Permissions(ctx context.Context) (PermissionStatus, error) {
	return s.provider.Permissions(ctx)
}

func (s *Service) ListApps(ctx context.Context) ([]App, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return s.listAppsLocked(ctx)
}

func (s *Service) listAppsLocked(ctx context.Context) ([]App, error) {
	apps, err := s.provider.ListApps(ctx)
	if err != nil {
		return nil, err
	}
	for i := range apps {
		apps[i].Name = redactSensitiveText(apps[i].Name)
		apps[i].BundleID = redactSensitiveText(apps[i].BundleID)
	}
	return apps, nil
}

func (s *Service) ResolveApp(ctx context.Context, selector string) (App, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return App{}, err
	}
	defer unlock()
	return s.resolveAppLocked(ctx, selector)
}

func (s *Service) resolveAppLocked(ctx context.Context, selector string) (App, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return App{}, NewError(CodeInvalidArgument, "app selector is required", nil)
	}
	apps, err := s.provider.ListApps(ctx)
	if err != nil {
		return App{}, err
	}
	var matches []App
	if strings.HasPrefix(selector, "pid:") {
		pid, parseErr := strconv.Atoi(strings.TrimPrefix(selector, "pid:"))
		if parseErr != nil || pid <= 0 {
			return App{}, NewError(CodeInvalidArgument, "app PID selector must be pid:<positive integer>", parseErr)
		}
		for _, app := range apps {
			if app.PID == pid {
				matches = append(matches, app)
			}
		}
	} else {
		for _, app := range apps {
			if app.BundleID == selector {
				matches = append(matches, app)
			}
		}
		if len(matches) == 0 {
			for _, app := range apps {
				if app.Name == selector {
					matches = append(matches, app)
				}
			}
		}
	}
	if len(matches) == 0 {
		return App{}, NewError(CodeAppNotFound, fmt.Sprintf("application %q is not running; run 'everyapi computer list-apps'", selector), nil)
	}
	if len(matches) > 1 {
		candidates := make([]string, 0, len(matches))
		for _, app := range matches {
			identity := app.BundleID
			if identity == "" {
				identity = app.Name
			}
			candidates = append(candidates, fmt.Sprintf("%s (pid:%d)", redactSensitiveText(identity), app.PID))
		}
		return App{}, NewError(CodeAppAmbiguous, fmt.Sprintf("application name %q matches multiple processes: %s; use a bundle ID or pid:<number>", redactSensitiveText(selector), strings.Join(candidates, ", ")), nil)
	}
	if err := blockedAppError(matches[0]); err != nil {
		return App{}, err
	}
	return matches[0], nil
}

func (s *Service) ListWindows(ctx context.Context, selector string) ([]Window, error) {
	if strings.TrimSpace(selector) == "" {
		return nil, NewError(CodeInvalidArgument, "app selector is required", nil)
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	app, err := s.resolveAppLocked(ctx, selector)
	if err != nil {
		return nil, err
	}
	windows, err := s.provider.ListWindows(ctx, app)
	if err != nil {
		return nil, err
	}
	for i := range windows {
		windows[i].Title = redactSensitiveText(windows[i].Title)
	}
	return windows, nil
}

func (s *Service) resolveTarget(ctx context.Context, selector string, windowIndex, windowID *int) (Target, error) {
	if err := validateTargetRequest(selector, windowIndex); err != nil {
		return Target{}, err
	}
	app, err := s.resolveAppLocked(ctx, selector)
	if err != nil {
		return Target{}, err
	}
	windows, err := s.provider.ListWindows(ctx, app)
	if err != nil {
		return Target{}, err
	}
	if len(windows) == 0 {
		return Target{}, NewError(CodeWindowNotFound, fmt.Sprintf("application %q has no accessible windows", redactSensitiveText(app.Name)), nil)
	}
	if windowID != nil {
		for _, window := range windows {
			if window.ID == *windowID {
				return Target{App: app, Window: window}, nil
			}
		}
		return Target{}, NewError(CodeWindowNotFound, fmt.Sprintf("window id %d was not found for %q", *windowID, redactSensitiveText(app.Name)), nil)
	}
	if windowIndex != nil {
		for _, window := range windows {
			if window.Index == *windowIndex {
				return Target{App: app, Window: window}, nil
			}
		}
		return Target{}, NewError(CodeWindowNotFound, fmt.Sprintf("window index %d was not found for %q", *windowIndex, redactSensitiveText(app.Name)), nil)
	}
	for _, window := range windows {
		if window.Focused {
			return Target{App: app, Window: window}, nil
		}
	}
	return Target{App: app, Window: windows[0]}, nil
}

func (s *Service) GetAppState(ctx context.Context, req StateRequest) (State, error) {
	if err := validateTargetRequest(req.App, req.WindowIndex); err != nil {
		return State{}, err
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return State{}, err
	}
	defer unlock()
	return s.getAppStateLocked(ctx, req)
}

func (s *Service) getAppStateLocked(ctx context.Context, req StateRequest) (State, error) {
	target, err := s.resolveTarget(ctx, req.App, req.WindowIndex, req.WindowID)
	if err != nil {
		return State{}, err
	}
	state, err := s.provider.GetState(ctx, target)
	if err != nil {
		return State{}, err
	}
	if state.App.PID != target.App.PID || !strings.EqualFold(state.App.BundleID, target.App.BundleID) {
		return State{}, NewError(CodeAppStale, "the selected application identity changed during observation; run list-apps again", nil)
	}
	if state.Window.ID != target.Window.ID || state.Window.Fingerprint != target.Window.Fingerprint {
		return State{}, NewError(CodeWindowStale, "the selected window identity changed during observation; run list-windows again", nil)
	}
	state.Snapshot.ElementCount = len(state.Snapshot.Elements)
	record := SnapshotRecord{PID: state.App.PID, BundleID: state.App.BundleID, WindowID: state.Window.ID, WindowFingerprint: state.Window.Fingerprint, CreatedAt: s.now(), Elements: make([]CachedElement, 0, len(state.Snapshot.Elements))}
	for _, element := range state.Snapshot.Elements {
		record.Elements = append(record.Elements, CachedElement{Index: element.Index, Path: append([]int(nil), element.Path...), Role: element.Role, Frame: element.Frame, Fingerprint: element.Fingerprint, Actions: append([]string(nil), element.Actions...)})
	}
	if err := s.store.Save(ctx, record); err != nil {
		return State{}, NewError(CodeInternal, "save computer-use snapshot: "+err.Error(), err)
	}
	return redactState(state), nil
}

// Screenshot does not save a snapshot record: unlike GetAppState, its result
// is opaque image bytes, not an accessibility tree with element indexes a
// later action could reference, so there is nothing here for the snapshot
// cache to serve.
func (s *Service) Screenshot(ctx context.Context, req StateRequest) ([]byte, error) {
	if err := validateTargetRequest(req.App, req.WindowIndex); err != nil {
		return nil, err
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	target, err := s.resolveTarget(ctx, req.App, req.WindowIndex, req.WindowID)
	if err != nil {
		return nil, err
	}
	return s.provider.Screenshot(ctx, target)
}

func (s *Service) cachedElement(ctx context.Context, target Target, index int) (SnapshotRecord, *CachedElement, error) {
	record, err := s.store.Load(ctx, target.App.PID, target.Window.ID)
	if err != nil {
		if errors.Is(err, ErrSnapshotNotFound) {
			return SnapshotRecord{}, nil, NewError(CodeElementStale, "no current element snapshot; run get-app-state again", err)
		}
		return SnapshotRecord{}, nil, NewError(CodeInternal, "load computer-use snapshot: "+err.Error(), err)
	}
	if s.now().Sub(record.CreatedAt) > snapshotTTL || record.CreatedAt.After(s.now().Add(time.Second)) {
		return SnapshotRecord{}, nil, NewError(CodeElementStale, "the element snapshot expired; run get-app-state again", nil)
	}
	if !strings.EqualFold(record.BundleID, target.App.BundleID) {
		return SnapshotRecord{}, nil, NewError(CodeElementStale, "the cached application identity changed; run get-app-state again", nil)
	}
	for i := range record.Elements {
		if record.Elements[i].Index == index {
			return record, &record.Elements[i], nil
		}
	}
	return SnapshotRecord{}, nil, NewError(CodeElementNotFound, fmt.Sprintf("element index %d is not in the latest snapshot; run get-app-state again", index), nil)
}

func (s *Service) Perform(ctx context.Context, req ActionRequest) (State, error) {
	if err := validateTargetRequest(req.App, req.WindowIndex); err != nil {
		return State{}, err
	}
	if (req.Kind == ActionTypeText || req.Kind == ActionPasteText) && req.Text == "" {
		return State{}, NewError(CodeInvalidArgument, string(req.Kind)+" requires non-empty text", nil)
	}
	if req.Kind == ActionTypeText || req.Kind == ActionPasteText || req.Kind == ActionSetValue {
		if err := rejectSensitiveText(req.Text); err != nil {
			return State{}, err
		}
	}
	if err := validateActionRequest(req); err != nil {
		return State{}, err
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return State{}, err
	}
	defer unlock()
	target, err := s.resolveTarget(ctx, req.App, req.WindowIndex, req.WindowID)
	if err != nil {
		return State{}, err
	}
	if err := validateWindowCoordinates(req, target.Window); err != nil {
		return State{}, err
	}
	perform := PerformRequest{Target: target, Kind: req.Kind, ExpectedWindowFingerprint: target.Window.Fingerprint, X: req.X, Y: req.Y, FromX: req.FromX, FromY: req.FromY, ToX: req.ToX, ToY: req.ToY, Text: req.Text, Key: req.Key, Direction: req.Direction, Amount: req.Amount, SecondaryAction: req.SecondaryAction, MouseButton: req.MouseButton, ClickCount: req.ClickCount, Modifiers: req.Modifiers, RestoreWindow: req.RestoreWindow}
	if req.ElementIndex != nil {
		record, element, cacheErr := s.cachedElement(ctx, target, *req.ElementIndex)
		if cacheErr != nil {
			return State{}, cacheErr
		}
		perform.ExpectedWindowFingerprint = record.WindowFingerprint
		perform.ExpectedElement = element
		if req.Kind == ActionSecondary && !containsString(element.Actions, req.SecondaryAction) {
			return State{}, NewError(CodeActionNotSupported, fmt.Sprintf("element index %d did not advertise accessibility action %q; run get-app-state again", element.Index, req.SecondaryAction), nil)
		}
	}
	if req.FromElementIndex != nil {
		record, element, cacheErr := s.cachedElement(ctx, target, *req.FromElementIndex)
		if cacheErr != nil {
			return State{}, cacheErr
		}
		perform.ExpectedWindowFingerprint = record.WindowFingerprint
		perform.ExpectedFromElement = element
	}
	if req.ToElementIndex != nil {
		record, element, cacheErr := s.cachedElement(ctx, target, *req.ToElementIndex)
		if cacheErr != nil {
			return State{}, cacheErr
		}
		perform.ExpectedWindowFingerprint = record.WindowFingerprint
		perform.ExpectedToElement = element
	}
	if err := s.store.Delete(ctx, target.App.PID, target.Window.ID); err != nil {
		return State{}, NewError(CodeInternal, "invalidate computer-use snapshot before action: "+err.Error(), err)
	}
	if err := s.provider.Perform(ctx, perform); err != nil {
		return State{}, err
	}
	state, refreshErr := s.getAppStateLocked(ctx, StateRequest{App: req.App})
	if refreshErr != nil {
		return redactState(State{App: target.App, Window: target.Window, RefreshError: codedError(refreshErr)}), nil
	}
	return state, nil
}

func codedError(err error) *Error {
	var coded *Error
	if errors.As(err, &coded) {
		return &Error{Code: coded.Code, Message: redactSensitiveText(coded.Message)}
	}
	return &Error{Code: CodeInternal, Message: redactSensitiveText(err.Error())}
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func validateTargetRequest(selector string, windowIndex *int) error {
	if strings.TrimSpace(selector) == "" {
		return NewError(CodeInvalidArgument, "app selector is required", nil)
	}
	if windowIndex != nil && *windowIndex < 0 {
		return NewError(CodeInvalidArgument, "window-index must be zero or greater", nil)
	}
	return nil
}

func validateWindowCoordinates(req ActionRequest, window Window) error {
	check := func(label string, x, y *int) error {
		if x == nil || y == nil {
			return nil
		}
		if *x < 0 || *y < 0 || float64(*x) >= window.Frame.Width || float64(*y) >= window.Frame.Height {
			return NewError(CodeInvalidArgument, fmt.Sprintf("%s coordinates (%d,%d) are outside the selected %.0fx%.0f window", label, *x, *y, window.Frame.Width, window.Frame.Height), nil)
		}
		return nil
	}
	if err := check("action", req.X, req.Y); err != nil {
		return err
	}
	if err := check("drag source", req.FromX, req.FromY); err != nil {
		return err
	}
	return check("drag destination", req.ToX, req.ToY)
}

func (s *Service) lock(ctx context.Context) (func(), error) {
	unlock, err := s.locker.Lock(ctx)
	if err == nil {
		return unlock, nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, NewError(CodeActionTimeout, "another computer-use operation did not finish before the lock timeout", err)
	}
	return nil, err
}

type localOperationLocker struct {
	token chan struct{}
}

func newLocalOperationLocker() *localOperationLocker {
	locker := &localOperationLocker{token: make(chan struct{}, 1)}
	locker.token <- struct{}{}
	return locker
}

func (l *localOperationLocker) Lock(ctx context.Context) (func(), error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-l.token:
		return func() { l.token <- struct{}{} }, nil
	}
}

func validateActionRequest(req ActionRequest) error {
	switch req.Kind {
	case ActionClick:
		if req.ElementIndex == nil && (req.X == nil || req.Y == nil) {
			return NewError(CodeInvalidArgument, "click requires element-index or both x and y", nil)
		}
		if req.ElementIndex != nil && (req.X != nil || req.Y != nil) {
			return NewError(CodeInvalidArgument, "click accepts element-index or coordinates, not both", nil)
		}
		if req.ElementIndex != nil && *req.ElementIndex <= 0 {
			return NewError(CodeInvalidArgument, "element-index must be positive", nil)
		}
		if req.MouseButton != "" && req.MouseButton != "left" && req.MouseButton != "right" && req.MouseButton != "middle" {
			return NewError(CodeInvalidArgument, "mouse-button must be left, right, or middle", nil)
		}
		if req.ClickCount != nil && *req.ClickCount <= 0 {
			return NewError(CodeInvalidArgument, "click-count must be positive", nil)
		}
	case ActionSetValue:
		if req.ElementIndex == nil || *req.ElementIndex <= 0 {
			return NewError(CodeInvalidArgument, "set-value requires element-index", nil)
		}
	case ActionTypeText, ActionPasteText:
	case ActionPressKey, ActionHotkey:
		if strings.TrimSpace(req.Key) == "" {
			return NewError(CodeInvalidArgument, string(req.Kind)+" requires key", nil)
		}
	case ActionScroll:
		if req.Direction != "up" && req.Direction != "down" && req.Direction != "left" && req.Direction != "right" {
			return NewError(CodeInvalidArgument, "scroll direction must be up, down, left, or right", nil)
		}
		element := req.ElementIndex != nil
		coordinates := req.X != nil && req.Y != nil
		if element == coordinates || (!coordinates && (req.X != nil || req.Y != nil)) {
			return NewError(CodeInvalidArgument, "scroll requires either element-index or both x and y", nil)
		}
		if req.ElementIndex != nil && *req.ElementIndex <= 0 {
			return NewError(CodeInvalidArgument, "element-index must be positive", nil)
		}
		if req.Amount <= 0 {
			return NewError(CodeInvalidArgument, "scroll amount must be positive", nil)
		}
	case ActionDrag:
		elements := req.FromElementIndex != nil && req.ToElementIndex != nil
		coordinates := req.FromX != nil && req.FromY != nil && req.ToX != nil && req.ToY != nil
		anyElements := req.FromElementIndex != nil || req.ToElementIndex != nil
		anyCoordinates := req.FromX != nil || req.FromY != nil || req.ToX != nil || req.ToY != nil
		if elements == coordinates || (elements && anyCoordinates) || (coordinates && anyElements) || (!elements && anyElements) || (!coordinates && anyCoordinates) {
			return NewError(CodeInvalidArgument, "drag requires either two element indexes or four coordinates", nil)
		}
		if elements && (*req.FromElementIndex <= 0 || *req.ToElementIndex <= 0) {
			return NewError(CodeInvalidArgument, "drag element indexes must be positive", nil)
		}
	case ActionSecondary:
		if req.ElementIndex == nil || *req.ElementIndex <= 0 || strings.TrimSpace(req.SecondaryAction) == "" {
			return NewError(CodeInvalidArgument, "perform-secondary-action requires element-index and action", nil)
		}
	default:
		return NewError(CodeInvalidArgument, fmt.Sprintf("unknown computer action %q", req.Kind), nil)
	}
	return nil
}
