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
	httpClient     = &http.Client{Timeout: 2 * time.Minute, CheckRedirect: refuseArtifactRedirects}
)

func refuseArtifactRedirects(_ *http.Request, _ []*http.Request) error {
	return http.ErrUseLastResponse
}

type publishResult struct {
	URL       string `json:"url"`
	ExpiresAt string `json:"expires_at"`
}

type deleteResult struct {
	URL     string `json:"url"`
	Deleted bool   `json:"deleted"`
}

func Run(args []string) error {
	if len(args) == 0 || (len(args) == 1 && isHelp(args[0])) {
		cliout.Println(i18n.T("artifacts.usage"))
		return nil
	}
	if args[0] != "share" && args[0] != "update" && args[0] != "delete" {
		return fmt.Errorf(i18n.T("artifacts.unknown_sub"), args[0])
	}
	operands, asJSON := artifactOperands(args[1:])
	expected := 1
	if args[0] == "update" {
		expected = 2
	}
	if len(operands) != expected {
		return errors.New(i18n.T("artifacts.usage"))
	}
	creds, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := cliout.SignalCtx()
	defer stop()
	switch args[0] {
	case "share":
		result, err := publish(ctx, httpClient, serviceBaseURL, creds, operands[0])
		if err != nil {
			return err
		}
		return printPublishResult(result, asJSON)
	case "update":
		result, err := updateArtifact(ctx, httpClient, serviceBaseURL, creds, operands[0], operands[1])
		if err != nil {
			return err
		}
		return printPublishResult(result, asJSON)
	case "delete":
		result, err := deleteArtifact(ctx, httpClient, serviceBaseURL, creds, operands[0])
		if err != nil {
			return err
		}
		return printDeleteResult(result, asJSON)
	}
	return errors.New(i18n.T("artifacts.usage"))
}

func isHelp(arg string) bool {
	return arg == "help" || arg == "--help" || arg == "-h"
}

func artifactOperands(args []string) ([]string, bool) {
	operands := make([]string, 0, len(args))
	asJSON := false
	for _, arg := range args {
		if arg == "--json" {
			asJSON = true
			continue
		}
		operands = append(operands, arg)
	}
	return operands, asJSON
}

func publish(ctx context.Context, client *http.Client, baseURL string, creds *config.Credentials, filePath string) (publishResult, error) {
	return uploadArtifact(ctx, client, creds, http.MethodPost, strings.TrimRight(baseURL, "/")+"/api/artifacts", http.StatusCreated, filePath)
}

func updateArtifact(ctx context.Context, client *http.Client, baseURL string, creds *config.Credentials, artifactURL, filePath string) (publishResult, error) {
	id, err := artifactIDFromURL(artifactURL)
	if err != nil {
		return publishResult{}, err
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/api/artifacts/" + id
	result, err := uploadArtifact(ctx, client, creds, http.MethodPut, endpoint, http.StatusOK, filePath)
	if err != nil {
		return publishResult{}, err
	}
	resultID, err := artifactIDFromURL(result.URL)
	if err != nil || resultID != id {
		return publishResult{}, fmt.Errorf("artifact service returned URL for %s, expected %s", resultID, id)
	}
	return result, nil
}

func uploadArtifact(ctx context.Context, client *http.Client, creds *config.Credentials, method, endpoint string, expectedStatus int, filePath string) (publishResult, error) {
	authOrigin, err := artifactAuthOrigin(creds)
	if err != nil {
		return publishResult{}, err
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
	req, err := http.NewRequestWithContext(ctx, method, endpoint, file)
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
	if resp.StatusCode != expectedStatus {
		return publishResult{}, artifactServiceError(resp.StatusCode, body)
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

func deleteArtifact(ctx context.Context, client *http.Client, baseURL string, creds *config.Credentials, artifactURL string) (deleteResult, error) {
	id, err := artifactIDFromURL(artifactURL)
	if err != nil {
		return deleteResult{}, err
	}
	authOrigin, err := artifactAuthOrigin(creds)
	if err != nil {
		return deleteResult{}, err
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/api/artifacts/" + id
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return deleteResult{}, fmt.Errorf("create artifact request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+creds.AccessToken)
	req.Header.Set("EveryAPI-User-Id", strconv.Itoa(creds.UserID))
	req.Header.Set("X-EveryAPI-Auth-Origin", authOrigin)
	resp, err := client.Do(req)
	if err != nil {
		return deleteResult{}, fmt.Errorf("delete artifact: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return deleteResult{}, fmt.Errorf("read artifact response: %w", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		return deleteResult{}, artifactServiceError(resp.StatusCode, body)
	}
	return deleteResult{URL: artifactURL, Deleted: true}, nil
}

func artifactAuthOrigin(creds *config.Credentials) (string, error) {
	if creds == nil || strings.TrimSpace(creds.AccessToken) == "" || creds.UserID <= 0 {
		return "", config.ErrNoCredentials
	}
	authOrigin := config.ResolveAPIBaseForBase(creds.APIBase)
	if authOrigin != config.DefaultAPIBase && authOrigin != config.ChinaAPIBase {
		return "", errors.New(i18n.T("artifacts.official_only"))
	}
	return authOrigin, nil
}

func artifactServiceError(statusCode int, body []byte) error {
	var failure struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &failure) == nil && failure.Error != "" {
		return fmt.Errorf("artifact service rejected the request: %s", cliout.Sanitize(failure.Error))
	}
	return fmt.Errorf("artifact service returned HTTP %d", statusCode)
}

func printPublishResult(result publishResult, asJSON bool) error {
	if asJSON {
		if err := json.NewEncoder(cliout.Out).Encode(result); err != nil {
			return fmt.Errorf("write artifact JSON: %w", err)
		}
		return nil
	}
	cliout.Println(cliout.Sanitize(result.URL))
	return nil
}

func printDeleteResult(result deleteResult, asJSON bool) error {
	if asJSON {
		if err := json.NewEncoder(cliout.Out).Encode(result); err != nil {
			return fmt.Errorf("write artifact JSON: %w", err)
		}
		return nil
	}
	cliout.Printf(i18n.T("artifacts.deleted")+"\n", cliout.Sanitize(result.URL))
	return nil
}

func validatePublishResult(result publishResult) error {
	if _, err := artifactIDFromURL(result.URL); err != nil {
		return errors.New("artifact service returned an invalid public URL")
	}
	if _, err := time.Parse(time.RFC3339, result.ExpiresAt); err != nil {
		return errors.New("artifact service returned an invalid expiration time")
	}
	return nil
}

func artifactIDFromURL(value string) (string, error) {
	publicURL, err := url.Parse(value)
	if err != nil || publicURL.Scheme != "https" || publicURL.Host != "artifacts.everyapi.ai" || publicURL.User != nil || publicURL.RawPath != "" || publicURL.RawQuery != "" || publicURL.Fragment != "" || !artifactURLPath.MatchString(publicURL.Path) {
		return "", errors.New("invalid artifact URL")
	}
	return strings.TrimPrefix(publicURL.Path, "/"), nil
}
