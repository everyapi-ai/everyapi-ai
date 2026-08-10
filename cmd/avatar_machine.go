package cmd

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

const avatarMachineProtocolVersion = 1

// avatarMaxBytes caps the decoded image. The backend normalizes avatars to a
// 256px square PNG, so anything approaching this is already anomalous; the cap
// exists so a hostile or misconfigured host cannot stream an unbounded body
// into the desktop's stdout reader.
const avatarMaxBytes = 384 * 1024

// avatarFetchTimeout bounds the single image request. The desktop calls this
// while painting its header, so a hanging host must fail fast rather than block.
const avatarFetchTimeout = 10 * time.Second

// avatarAllowedTypes is the set the backend's avatar pipeline can produce. The
// desktop renders the bytes as an <img> data URI, so restricting the type keeps
// an unexpected payload (SVG, HTML) from being handed to the webview.
var avatarAllowedTypes = map[string]struct{}{
	"image/png":  {},
	"image/jpeg": {},
	"image/webp": {},
}

type avatarMachineOutput struct {
	Version int    `json:"version"`
	MIME    string `json:"mime,omitempty"`
	Data    string `json:"data,omitempty"`
}

// AvatarMachine streams the signed-in account's profile picture as base64 for
// the desktop app, which cannot fetch it itself: its webview CSP allows no
// remote image origin, and the avatar host is the backend's ServerAddress —
// a different origin from the API base, unknowable at build time for
// self-hosted deployments. Doing the fetch here keeps the renderer's image
// policy closed and needs no HTTP client in the desktop shell.
//
// An account with no picture is not an error: the command reports a bodyless
// payload and the desktop falls back to its monogram.
func AvatarMachine(args []string) error {
	if !statusMachineRequested(args) {
		return machineStatusError("invalid_request", errors.New("avatar requires --format=json"))
	}

	unlock, err := acquireCredentialLock()
	if err != nil {
		return machineStatusError("unavailable", fmt.Errorf("lock credential cache: %w", err))
	}
	defer unlock()

	out := avatarMachineOutput{Version: avatarMachineProtocolVersion}
	creds, err := config.Load()
	if errors.Is(err, config.ErrNoCredentials) {
		return encodeAvatarOutput(out)
	}
	if err != nil {
		return machineStatusError("invalid_credentials", err)
	}
	target := strings.TrimSpace(creds.AvatarURL)
	if target == "" {
		return encodeAvatarOutput(out)
	}
	parsed, err := url.Parse(target)
	if err != nil || !isFetchableAvatarURL(parsed) {
		// A cached URL we will not fetch is treated as "no picture" rather than
		// an error: the desktop still has a monogram to show.
		return encodeAvatarOutput(out)
	}

	mime, data, err := fetchAvatar(parsed.String())
	if err != nil {
		return machineStatusError("unavailable", err)
	}
	out.MIME = mime
	out.Data = base64.StdEncoding.EncodeToString(data)
	return encodeAvatarOutput(out)
}

// isFetchableAvatarURL keeps the request on an ordinary web origin. The URL
// comes from our own backend, but it round-trips through a local file, so the
// scheme is checked rather than assumed.
func isFetchableAvatarURL(u *url.URL) bool {
	if u == nil || u.Host == "" || u.User != nil {
		return false
	}
	return u.Scheme == "https" || u.Scheme == "http"
}

func fetchAvatar(target string) (string, []byte, error) {
	client := &http.Client{Timeout: avatarFetchTimeout}
	request, err := http.NewRequestWithContext(cliout.WithCtx(), http.MethodGet, target, nil)
	if err != nil {
		return "", nil, fmt.Errorf("build avatar request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return "", nil, fmt.Errorf("fetch avatar: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("fetch avatar: status %d", response.StatusCode)
	}
	mime := strings.ToLower(strings.TrimSpace(strings.SplitN(response.Header.Get("Content-Type"), ";", 2)[0]))
	if _, ok := avatarAllowedTypes[mime]; !ok {
		return "", nil, fmt.Errorf("fetch avatar: unsupported content type %q", mime)
	}
	// Read one byte past the cap so an oversized body is rejected rather than
	// silently truncated into a corrupt image.
	data, err := io.ReadAll(io.LimitReader(response.Body, avatarMaxBytes+1))
	if err != nil {
		return "", nil, fmt.Errorf("read avatar: %w", err)
	}
	if len(data) == 0 {
		return "", nil, errors.New("read avatar: empty body")
	}
	if len(data) > avatarMaxBytes {
		return "", nil, fmt.Errorf("read avatar: larger than %d bytes", avatarMaxBytes)
	}
	return mime, data, nil
}

func encodeAvatarOutput(out avatarMachineOutput) error {
	if err := json.NewEncoder(cliout.Out).Encode(out); err != nil {
		return machineStatusError("unavailable", err)
	}
	return nil
}
