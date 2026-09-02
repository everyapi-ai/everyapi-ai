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
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

const (
	maxArtifactBytes             int64 = 5 << 20
	maxArtifactListResponseBytes int64 = 1 << 20
	maxArtifactListPages               = 100
	maxArtifactProjectBytes            = 128
)

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

type artifactListItem struct {
	URL       string `json:"url"`
	Filename  string `json:"filename"`
	Title     string `json:"title,omitempty"`
	Project   string `json:"project,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at,omitempty"`
	ExpiresAt string `json:"expires_at"`
}

type artifactListPage struct {
	Artifacts  []artifactListItem `json:"artifacts"`
	NextCursor string             `json:"next_cursor,omitempty"`
}

type artifactListResult struct {
	Artifacts []artifactListItem `json:"artifacts"`
}

func Run(args []string) error {
	if len(args) == 0 || hasHelpArg(args) {
		cliout.Println(i18n.T("artifacts.usage"))
		return nil
	}
	if args[0] != "share" && args[0] != "update" && args[0] != "delete" && args[0] != "list" && args[0] != "unshare" {
		return fmt.Errorf(i18n.T("artifacts.unknown_sub"), args[0])
	}
	operands, asJSON := artifactOperands(args[1:])
	if args[0] == "delete" && len(operands) == 0 {
		if id := artifactFlagValue(args[1:], "id"); id != "" {
			operands = []string{id}
		}
	}
	if args[0] == "update" && len(operands) == 1 {
		// The file-only form resolves the URL saved by share. The legacy
		// URL-plus-file form remains accepted for backwards compatibility.
		if url, loadErr := artifactForFile(operands[0]); loadErr == nil && url != "" {
			operands = append([]string{url}, operands...)
		}
	}
	if args[0] == "unshare" && len(operands) == 1 {
		if url, loadErr := artifactForFile(operands[0]); loadErr == nil && url != "" {
			operands = append([]string{url}, operands...)
		}
	}
	expected := 1
	if args[0] == "update" || args[0] == "unshare" {
		expected = 2
	} else if args[0] == "list" {
		expected = 0
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
	baseURL := serviceBaseURL
	if value := artifactFlagValue(args[1:], "api-url"); value != "" {
		baseURL = strings.TrimRight(value, "/")
	}
	switch args[0] {
	case "share":
		result, err := publish(ctx, httpClient, baseURL, creds, operands[0])
		if err != nil {
			return err
		}
		_ = rememberArtifactFile(operands[0], result.URL)
		return printPublishResult(result, asJSON)
	case "update":
		result, err := updateArtifact(ctx, httpClient, baseURL, creds, operands[0], operands[1])
		if err != nil {
			return err
		}
		return printPublishResult(result, asJSON)
	case "delete":
		artifactURL := operands[0]
		if !strings.Contains(artifactURL, "://") {
			artifactURL = "https://artifacts.everyapi.ai/" + strings.TrimPrefix(artifactURL, "/")
		}
		result, err := deleteArtifact(ctx, httpClient, baseURL, creds, artifactURL)
		if err != nil {
			return err
		}
		return printDeleteResult(result, asJSON)
	case "unshare":
		artifactURL := operands[0]
		if !strings.Contains(artifactURL, "://") {
			artifactURL = "https://artifacts.everyapi.ai/" + strings.TrimPrefix(artifactURL, "/")
		}
		result, err := deleteArtifact(ctx, httpClient, baseURL, creds, artifactURL)
		if err != nil {
			return err
		}
		_ = forgetArtifactFile(operands[len(operands)-1])
		return printDeleteResult(result, asJSON)
	case "list":
		result, err := listArtifacts(ctx, httpClient, baseURL, creds)
		if err != nil {
			return err
		}
		return printListResult(result, asJSON)
	}
	return errors.New(i18n.T("artifacts.usage"))
}

func hasHelpArg(args []string) bool {
	for _, arg := range args {
		if isHelp(arg) {
			return true
		}
	}
	return false
}

func isHelp(arg string) bool {
	return arg == "help" || arg == "--help" || arg == "-h"
}

func artifactOperands(args []string) ([]string, bool) {
	operands := make([]string, 0, len(args))
	asJSON := false
	skipNext := false
	for _, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if arg == "--json" {
			asJSON = true
			continue
		}
		if arg == "--api-url" || arg == "--cursor" {
			skipNext = true
			continue
		}
		if arg == "--id" {
			skipNext = true
			continue
		}
		if strings.HasPrefix(arg, "--api-url=") || strings.HasPrefix(arg, "--cursor=") {
			continue
		}
		if strings.HasPrefix(arg, "--id=") {
			continue
		}
		operands = append(operands, arg)
	}
	return operands, asJSON
}

func artifactFlagValue(args []string, name string) string {
	prefix := "--" + name + "="
	for i, arg := range args {
		if strings.HasPrefix(arg, prefix) {
			return strings.TrimPrefix(arg, prefix)
		}
		if arg == "--"+name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func artifactMapPath() (string, error) {
	dir := os.Getenv("EVERYAPI_WORKSPACE_STATE_DIR")
	if dir == "" {
		var err error
		dir, err = os.UserConfigDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(dir, "everyapi")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "artifacts.json"), nil
}

func artifactForFile(file string) (string, error) {
	path, err := artifactMapPath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var items map[string]string
	if err := json.Unmarshal(data, &items); err != nil {
		return "", err
	}
	abs, _ := filepath.Abs(file)
	return items[abs], nil
}

func rememberArtifactFile(file, artifactURL string) error {
	path, err := artifactMapPath()
	if err != nil {
		return err
	}
	items := map[string]string{}
	if data, readErr := os.ReadFile(path); readErr == nil {
		_ = json.Unmarshal(data, &items)
	}
	abs, _ := filepath.Abs(file)
	items[abs] = artifactURL
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func forgetArtifactFile(file string) error {
	path, err := artifactMapPath()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var items map[string]string
	if json.Unmarshal(data, &items) != nil {
		return nil
	}
	abs, _ := filepath.Abs(file)
	delete(items, abs)
	newData, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(newData, '\n'), 0o600)
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
	warnings, err := artifactViewerWarnings(file, openedInfo.Size())
	if err != nil {
		return publishResult{}, err
	}
	printArtifactWarnings(filepath.Base(filePath), warnings)
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
	if project := currentArtifactProject(ctx); project != "" {
		req.Header.Set("X-Artifact-Project", base64.RawURLEncoding.EncodeToString([]byte(project)))
	}
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

// warnOut is where publish-time advisories go. Stderr, not cliout.Out: `artifacts share --json` is parsed by the agents that publish most reports, and a warning on stdout would corrupt that contract.
var warnOut io.Writer = os.Stderr

// The artifact viewer serves reports from an isolated origin under `default-src 'none'` — inline CSS and JS only, `data:`/`blob:` images and fonts, `connect-src 'none'`, and a sandbox with no same-origin privilege. An author gets no feedback about any of that: a blocked stylesheet or a failed fetch degrades silently, and the report simply looks wrong to whoever opens it.
//
// So these two classes are reported at the only moment the author is still present. They are the ones the service genuinely cannot repair on the reader's behalf, unlike the colour scheme and link target, which the worker rewrites. Detection is deliberately coarse and never blocks the publish: a false positive costs a line on stderr, while refusing to upload would cost the report.
//
// The JS check reads only <script> bodies. Scanning the whole document would flag any report that merely DISCUSSES fetch() or localStorage in its prose — and reports about web code do that constantly.
var (
	artifactScriptBlock       = regexp.MustCompile(`(?is)<script\b[^>]*>(.*?)</script\s*>`)
	artifactExternalResources = []*regexp.Regexp{
		regexp.MustCompile(`(?is)<(?:script|img|iframe|source|video|audio|embed|object|track)\b[^>]*?\s(?:src|data)\s*=\s*["']?\s*(?:https?:)?//[^\s"'>]+`),
		regexp.MustCompile(`(?is)<link\b[^>]*?\shref\s*=\s*["']?\s*(?:https?:)?//[^\s"'>]+`),
		regexp.MustCompile(`(?i)url\(\s*["']?\s*(?:https?:)?//[^)"'\s]+`),
	}
	artifactBlockedAPIs = regexp.MustCompile(`(?i)\b(?:fetch\s*\(|XMLHttpRequest|localStorage|sessionStorage|indexedDB|navigator\s*\.\s*sendBeacon|new\s+EventSource|new\s+WebSocket)`)
)

type artifactWarning struct {
	key    string
	sample string
}

// artifactViewerWarnings inspects the very bytes that are about to be uploaded, then rewinds. Re-reading the path instead would open a second window for the swap that the caller's SameFile check just closed. A read failure yields no warnings — advisory output must not fail a publish — but a failed rewind is fatal, because the request body would otherwise be silently short.
func artifactViewerWarnings(file *os.File, size int64) ([]artifactWarning, error) {
	content := make([]byte, size)
	_, readErr := io.ReadFull(file, content)
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewind artifact: %w", err)
	}
	if readErr != nil {
		return nil, nil
	}
	return scanArtifactForViewerLimits(string(content)), nil
}

func scanArtifactForViewerLimits(content string) []artifactWarning {
	var warnings []artifactWarning
	for _, pattern := range artifactExternalResources {
		if match := pattern.FindString(content); match != "" {
			warnings = append(warnings, artifactWarning{key: "artifacts.lint_external", sample: match})
			break
		}
	}
	for _, script := range artifactScriptBlock.FindAllStringSubmatch(content, -1) {
		if match := artifactBlockedAPIs.FindString(script[1]); match != "" {
			warnings = append(warnings, artifactWarning{key: "artifacts.lint_blocked_api", sample: match})
			break
		}
	}
	return warnings
}

func printArtifactWarnings(filename string, warnings []artifactWarning) {
	if len(warnings) == 0 {
		return
	}
	fmt.Fprintf(warnOut, i18n.T("artifacts.lint_intro")+"\n", cliout.Sanitize(filename))
	for _, warning := range warnings {
		fmt.Fprintf(warnOut, i18n.T(warning.key)+"\n", cliout.Sanitize(truncateArtifactSample(warning.sample)))
	}
	fmt.Fprintln(warnOut, i18n.T("artifacts.lint_hint"))
}

// Truncation counts runes, not bytes: a sample is arbitrary document content and cutting mid-sequence would print a replacement character.
func truncateArtifactSample(sample string) string {
	const limit = 80
	sample = strings.Join(strings.Fields(sample), " ")
	runes := []rune(sample)
	if len(runes) <= limit {
		return sample
	}
	return string(runes[:limit]) + "…"
}

func currentArtifactProject(ctx context.Context) string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	root := cwd
	if output, commandErr := exec.CommandContext(ctx, "git", "-C", cwd, "rev-parse", "--show-toplevel").Output(); commandErr == nil {
		if candidate := strings.TrimSpace(string(output)); candidate != "" {
			root = candidate
		}
	}
	project := ""
	if output, commandErr := exec.CommandContext(ctx, "git", "-C", root, "config", "--get", "remote.origin.url").Output(); commandErr == nil {
		project = artifactProjectFromRemote(string(output))
	}
	if project == "" {
		project = filepath.Base(filepath.Clean(root))
	}
	return normalizeArtifactProject(project)
}

func artifactProjectFromRemote(remote string) string {
	value := strings.TrimRight(strings.TrimSpace(remote), "/\\")
	if separator := strings.LastIndexAny(value, "/\\:"); separator >= 0 {
		value = value[separator+1:]
	}
	return strings.TrimSuffix(value, ".git")
}

func normalizeArtifactProject(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" || value == "." || value == string(filepath.Separator) {
		return ""
	}
	var normalized strings.Builder
	for _, character := range value {
		if normalized.Len()+len(string(character)) > maxArtifactProjectBytes {
			break
		}
		normalized.WriteRune(character)
	}
	return normalized.String()
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

func listArtifacts(ctx context.Context, client *http.Client, baseURL string, creds *config.Credentials) (artifactListResult, error) {
	authOrigin, err := artifactAuthOrigin(creds)
	if err != nil {
		return artifactListResult{}, err
	}
	result := artifactListResult{Artifacts: []artifactListItem{}}
	cursor := ""
	seenCursors := map[string]struct{}{}
	for pageNumber := 0; pageNumber < maxArtifactListPages; pageNumber++ {
		endpoint := strings.TrimRight(baseURL, "/") + "/api/artifacts"
		if cursor != "" {
			endpoint += "?" + url.Values{"cursor": []string{cursor}}.Encode()
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return artifactListResult{}, fmt.Errorf("create artifact list request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+creds.AccessToken)
		req.Header.Set("EveryAPI-User-Id", strconv.Itoa(creds.UserID))
		req.Header.Set("X-EveryAPI-Auth-Origin", authOrigin)
		resp, err := client.Do(req)
		if err != nil {
			return artifactListResult{}, fmt.Errorf("list artifacts: %w", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxArtifactListResponseBytes))
		_ = resp.Body.Close()
		if readErr != nil {
			return artifactListResult{}, fmt.Errorf("read artifact list response: %w", readErr)
		}
		if resp.StatusCode != http.StatusOK {
			return artifactListResult{}, artifactServiceError(resp.StatusCode, body)
		}
		var page artifactListPage
		if err := json.Unmarshal(body, &page); err != nil {
			return artifactListResult{}, fmt.Errorf("decode artifact list response: %w", err)
		}
		for _, artifact := range page.Artifacts {
			if err := validateArtifactListItem(artifact); err != nil {
				return artifactListResult{}, err
			}
		}
		result.Artifacts = append(result.Artifacts, page.Artifacts...)
		if page.NextCursor == "" {
			sort.SliceStable(result.Artifacts, func(i, j int) bool {
				return result.Artifacts[i].CreatedAt > result.Artifacts[j].CreatedAt
			})
			return result, nil
		}
		if len(page.NextCursor) > 4096 {
			return artifactListResult{}, errors.New("artifact service returned an invalid list cursor")
		}
		if _, repeated := seenCursors[page.NextCursor]; repeated {
			return artifactListResult{}, errors.New("artifact service repeated a list cursor")
		}
		seenCursors[page.NextCursor] = struct{}{}
		cursor = page.NextCursor
	}
	return artifactListResult{}, fmt.Errorf("artifact list exceeded %d pages", maxArtifactListPages)
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

func printListResult(result artifactListResult, asJSON bool) error {
	if asJSON {
		if err := json.NewEncoder(cliout.Out).Encode(result); err != nil {
			return fmt.Errorf("write artifact list JSON: %w", err)
		}
		return nil
	}
	for _, artifact := range result.Artifacts {
		cliout.Println(cliout.Sanitize(artifact.URL))
	}
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

func validateArtifactListItem(item artifactListItem) error {
	if _, err := artifactIDFromURL(item.URL); err != nil {
		return errors.New("artifact service returned an invalid URL in the artifact list")
	}
	if strings.TrimSpace(item.Filename) == "" {
		return errors.New("artifact service returned an empty filename in the artifact list")
	}
	if _, err := time.Parse(time.RFC3339, item.CreatedAt); err != nil {
		return errors.New("artifact service returned an invalid creation time in the artifact list")
	}
	if item.UpdatedAt != "" {
		if _, err := time.Parse(time.RFC3339, item.UpdatedAt); err != nil {
			return errors.New("artifact service returned an invalid update time in the artifact list")
		}
	}
	if _, err := time.Parse(time.RFC3339, item.ExpiresAt); err != nil {
		return errors.New("artifact service returned an invalid expiration time in the artifact list")
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
