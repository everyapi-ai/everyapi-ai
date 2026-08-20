package computeruse

import (
	"bytes"
	"image"
	_ "image/png"
	"os"
	"path/filepath"
	"time"
)

// screenshotFileTTL bounds how long an auto-captured screenshot file is kept
// before sweepExpiredScreenshots removes it. These are throwaway artifacts
// meant for a caller to inspect immediately after an action, not a durable
// record, so an hour is generous rather than tuned.
const screenshotFileTTL = time.Hour

func screenshotDir() string {
	return filepath.Join(os.TempDir(), "everyapi-computer-use-screenshots")
}

// writeScreenshotFile decodes just enough of the PNG to report its
// dimensions, then writes the full bytes to a fresh file under
// screenshotDir. It sweeps expired files from that directory first so a
// process that runs for a long time doesn't accumulate one file per action
// forever.
func writeScreenshotFile(png []byte) (*ScreenshotAttachment, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(png))
	if err != nil {
		return nil, err
	}
	dir := screenshotDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	sweepExpiredScreenshots(dir)
	file, err := os.CreateTemp(dir, "*.png")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if _, err := file.Write(png); err != nil {
		os.Remove(file.Name())
		return nil, err
	}
	return &ScreenshotAttachment{Path: file.Name(), Format: "png", Width: config.Width, Height: config.Height}, nil
}

func sweepExpiredScreenshots(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-screenshotFileTTL)
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, entry.Name()))
	}
}
