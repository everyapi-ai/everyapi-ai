//go:build windows

package artifacts

import "os"

func openArtifact(path string) (*os.File, error) {
	return os.Open(path)
}
