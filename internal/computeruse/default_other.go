//go:build !darwin

package computeruse

import (
	"context"
	"time"
)

type unsupportedSnapshotStore struct{}

func (unsupportedSnapshotStore) Save(context.Context, SnapshotRecord) error {
	return unsupportedPlatformError()
}

func (unsupportedSnapshotStore) Load(context.Context, string, int, int) (SnapshotRecord, error) {
	return SnapshotRecord{}, unsupportedPlatformError()
}

func (unsupportedSnapshotStore) Delete(context.Context, string, int, int) error {
	return unsupportedPlatformError()
}

func newDefaultService() (*Service, error) {
	provider, err := newPlatformProvider("")
	if err != nil {
		return nil, err
	}
	return NewService(provider, unsupportedSnapshotStore{}, time.Now), nil
}
