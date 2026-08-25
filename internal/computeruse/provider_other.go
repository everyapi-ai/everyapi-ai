//go:build !darwin

package computeruse

import (
	"context"
	"runtime"
)

type unsupportedProvider struct{}

func newPlatformProvider(string) (Provider, error) { return unsupportedProvider{}, nil }

func unsupportedPlatformError() error {
	return NewError(CodeUnsupportedPlatform, "computer use is currently supported on macOS; this build is running on "+runtime.GOOS, nil)
}

func (unsupportedProvider) Capabilities(context.Context) (Capabilities, error) {
	return Capabilities{Provider: "unsupported", ProviderVersion: computerProviderVersion, ProtocolVersion: computerProtocolVersion, Platform: runtime.GOOS}, nil
}

func (unsupportedProvider) Permissions(context.Context) (PermissionStatus, error) {
	return PermissionStatus{}, unsupportedPlatformError()
}

func (unsupportedProvider) RequestPermission(context.Context, string) error {
	return unsupportedPlatformError()
}

func (unsupportedProvider) ListApps(context.Context) ([]App, error) {
	return nil, unsupportedPlatformError()
}

func (unsupportedProvider) ListWindows(context.Context, App) ([]Window, error) {
	return nil, unsupportedPlatformError()
}

func (unsupportedProvider) GetState(context.Context, Target) (State, error) {
	return State{}, unsupportedPlatformError()
}

func (unsupportedProvider) Perform(context.Context, PerformRequest) error {
	return unsupportedPlatformError()
}

func (unsupportedProvider) Screenshot(context.Context, Target) ([]byte, error) {
	return nil, unsupportedPlatformError()
}
