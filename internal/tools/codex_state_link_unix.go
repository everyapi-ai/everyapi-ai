//go:build !windows

package tools

import "os"

func linkCodexStateDirectory(target, link string) error {
	return os.Symlink(target, link)
}
