//go:build !darwin && !linux

package computeruse

import (
	"context"
	"fmt"
)

func lockOperationFile(context.Context, string) (func(), error) {
	return nil, fmt.Errorf("operation file locking is unsupported on this platform")
}
