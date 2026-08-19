// Package artifacts publishes self-contained HTML reports through the EveryAPI artifact service.
package artifacts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

const maxArtifactBytes int64 = 5 << 20

var artifactURLPath = regexp.MustCompile(`^/[A-Za-z0-9_-]{12}$`)

var (
	serviceBaseURL = "https://artifacts.everyapi.ai"
	httpClient     = &http.Client{Timeout: 2 * time.Minute}
)

type publishResult struct {
	URL       string `json:"url"`
	ExpiresAt string `json:"expires_at"`
}

func Run(args []string) error {
	if len(args) == 0 || (len(args) == 1 && isHelp(args[0])) {
		cliout.Println(i18n.T("artifacts.usage"))
		return nil
	}
	if args[0] != "share" {
		return fmt.Errorf(i18n.T("artifacts.unknown_sub"), args[0])
	}
	if len(args) != 2 {
		return errors.New(i18n.T("artifacts.usage"))
	}
	creds, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := cliout.SignalCtx()
	defer stop()
	result, err := publish(ctx, httpClient, serviceBaseURL, creds, args[1])
	if err != nil {
		return err
	}
	cliout.Println(cliout.Sanitize(result.URL))
	return nil
}

func isHelp(arg string) bool {
	return arg == "help" || arg == "--help" || arg == "-h"
}

func publish(ctx context.Context, client *http.Client, baseURL string, creds *config.Credentials, filePath string) (publishResult, error) {
	if creds == nil || strings.TrimSpace(creds.AccessToken) == "" || creds.UserID <= 0 {
		return publishResult{}, config.ErrNoCredentials
	}
	authOrigin := config.ResolveAPIBaseForBase(creds.APIBase)
	if authOrigin != config.DefaultAPIBase && authOrigin != config.ChinaAPIBase {
		return publishResult{}, errors.New(i18n.T("artifacts.official_only"))
	}
	ext := strings.ToLower(filepath.Ext(filePath))
	if ext != ".html" && ext != ".htm" {
		return publishResult{}, errors.New(i18n.T("artifacts.html_required"))
	}
	info, err := os.Stat(filePath)
	if err != nil {
		return publishResult{}, fmt.Errorf("stat artifact: %w", err)
	}
	if !info.Mode().IsRegular() {
		return publishResult{}, errors.New(i18n.T("artifacts.regular_required"))
	}
	file, err := openArtifact(filePath)
	if err != nil {
		return publishResult{}, fmt.Errorf("open artifact: %w", err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return publishResult{}, fmt.Errorf("stat artifact: %w", err)
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return publishResult{}, errors.New(i18n.T("artifacts.regular_required"))
	}
	if openedInfo.Size() > maxArtifactBytes {
		return publishResult{}, fmt.Errorf(i18n.T("artifacts.too_large"), openedInfo.Size(), maxArtifactBytes)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/api/artifacts", file)
	if err != nil {
		return publishResult{}, fmt.Errorf("create artifact request: %w", err)
	}
	req.ContentLength = openedInfo.Size()
	req.Header.Set("Authorization", "Bearer "+creds.AccessToken)
	req.Header.Set("EveryAPI-User-Id", strconv.Itoa(creds.UserID))
	req.Header.Set("Content-Type", "text/html; charset=utf-8")
	req.Header.Set("X-EveryAPI-Auth-Origin", authOrigin)
	req.Header.Set("X-Artifact-Filename", base64.RawURLEncoding.EncodeToString([]byte(filepath.Base(filePath))))
	resp, err := client.Do(req)
	if err != nil {
		return publishResult{}, fmt.Errorf("publish artifact: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return publishResult{}, fmt.Errorf("read artifact response: %w", err)
	}
	if resp.StatusCode != http.StatusCreated {
		var failure struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &failure) == nil && failure.Error != "" {
			return publishResult{}, fmt.Errorf("artifact service rejected the upload: %s", cliout.Sanitize(failure.Error))
		}
		return publishResult{}, fmt.Errorf("artifact service returned HTTP %d", resp.StatusCode)
	}
	var result publishResult
	if err := json.Unmarshal(body, &result); err != nil {
		return publishResult{}, fmt.Errorf("decode artifact response: %w", err)
	}
	if result.URL == "" || result.ExpiresAt == "" {
		return publishResult{}, errors.New("artifact service returned an incomplete response")
	}
	if err := validatePublishResult(result); err != nil {
		return publishResult{}, err
	}
	return result, nil
}

func validatePublishResult(result publishResult) error {
	publicURL, err := url.Parse(result.URL)
	if err != nil || publicURL.Scheme != "https" || publicURL.Host != "artifacts.everyapi.ai" || publicURL.User != nil || publicURL.RawPath != "" || publicURL.RawQuery != "" || publicURL.Fragment != "" || !artifactURLPath.MatchString(publicURL.Path) {
		return errors.New("artifact service returned an invalid public URL")
	}
	if _, err := time.Parse(time.RFC3339, result.ExpiresAt); err != nil {
		return errors.New("artifact service returned an invalid expiration time")
	}
	return nil
}
