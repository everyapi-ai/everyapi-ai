package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	vmConfigFile             = "everyapi.yaml"
	vmConfigFileAlternate    = "everyapi.yml"
	vmRuntimeStoreFile       = "vm-runtimes.json"
	maxVMConfigBytes         = 1024 * 1024
	maxVMConfigNodes         = 20_000
	maxVMConfigAliases       = 100
	maxVMRecipes             = 100
	maxVMFieldBytes          = 16_384
	maxVMProcessCaptureBytes = 1024 * 1024
	maxVMDiagnosticBytes     = 16_000
	maxVMRuntimeStoreBytes   = 1024 * 1024
)

type vmCheckoutMode string

const (
	vmCheckoutEveryAPIWorktree vmCheckoutMode = "everyapi-worktree"
	vmCheckoutProvisionedRoot  vmCheckoutMode = "provisioned-root"
)

type vmRecipe struct {
	ID              string         `json:"id" yaml:"id"`
	Name            string         `json:"name" yaml:"name"`
	Description     string         `json:"description,omitempty" yaml:"description,omitempty"`
	Create          string         `json:"create" yaml:"create"`
	CheckoutMode    vmCheckoutMode `json:"checkoutMode,omitempty" yaml:"checkoutMode,omitempty"`
	Suspend         string         `json:"suspend,omitempty" yaml:"suspend,omitempty"`
	Resume          string         `json:"resume,omitempty" yaml:"resume,omitempty"`
	Destroy         string         `json:"destroy,omitempty" yaml:"destroy,omitempty"`
	DestroyDisabled bool           `json:"destroyDisabled,omitempty" yaml:"-"`
	Available       bool           `json:"available" yaml:"-"`
}

type vmRecipeDocument struct {
	EnvironmentRecipes []vmRecipeYAML `yaml:"environmentRecipes"`
	Extra              map[string]any `yaml:",inline"`
}

type vmRecipeYAML struct {
	ID           string         `yaml:"id"`
	Name         string         `yaml:"name"`
	Description  string         `yaml:"description"`
	Create       string         `yaml:"create"`
	Command      string         `yaml:"command"`
	CheckoutMode vmCheckoutMode `yaml:"checkoutMode"`
	Suspend      string         `yaml:"suspend"`
	Resume       string         `yaml:"resume"`
	Destroy      string         `yaml:"destroy"`
	Cleanup      string         `yaml:"cleanup"`
}

type vmRecipeListResult struct {
	Runtime    string     `json:"runtime"`
	Arch       string     `json:"arch"`
	RepoPath   string     `json:"repoPath"`
	ConfigPath string     `json:"configPath"`
	Recipes    []vmRecipe `json:"recipes"`
}

type vmCheckStatus string

const (
	vmCheckPass vmCheckStatus = "pass"
	vmCheckWarn vmCheckStatus = "warn"
	vmCheckFail vmCheckStatus = "fail"
)

type vmCheck struct {
	ID          string        `json:"id"`
	Status      vmCheckStatus `json:"status"`
	Message     string        `json:"message"`
	Remediation string        `json:"remediation,omitempty"`
}

type vmTranscriptStage struct {
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	Error    string `json:"error,omitempty"`
}

type vmProvisionTranscript struct {
	Provision vmTranscriptStage `json:"provision"`
	Destroy   vmTranscriptStage `json:"destroy"`
}

type vmDoctorResult struct {
	RecipeID            string                 `json:"recipeId"`
	RepoPath            string                 `json:"repoPath"`
	ConfigPath          string                 `json:"configPath,omitempty"`
	OK                  bool                   `json:"ok"`
	Checks              []vmCheck              `json:"checks"`
	ProvisionTranscript *vmProvisionTranscript `json:"provisionTranscript,omitempty"`
}

type vmMode string

const (
	vmModeCreate  vmMode = "create"
	vmModeSuspend vmMode = "suspend"
	vmModeResume  vmMode = "resume"
	vmModeDestroy vmMode = "destroy"
)

type vmRunContext struct {
	InstanceID    string
	RecipeID      string
	ProjectID     string
	WorkspaceID   string
	WorkspaceName string
	RepoPath      string
	RepoURL       string
	Branch        string
}

type vmRunRequest struct {
	Command             string
	RepoPath            string
	Mode                vmMode
	Context             vmRunContext
	ResultSchemaVersion int
	Stdin               string
}

type vmProcessResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Err      error
}

type vmRecipeRunner interface {
	Run(context.Context, vmRunRequest) vmProcessResult
}

type shellVMRecipeRunner struct{}

type cappedVMWriter struct {
	max  int
	data []byte
}

func (w *cappedVMWriter) Write(value []byte) (int, error) {
	original := len(value)
	if w.max <= 0 {
		return original, nil
	}
	if len(w.data)+len(value) <= w.max {
		w.data = append(w.data, value...)
		return original, nil
	}
	combined := append(append([]byte{}, w.data...), value...)
	half := w.max / 2
	omitted := len(combined) - half*2
	marker := []byte(fmt.Sprintf("\n…[%d bytes omitted]…\n", omitted))
	remaining := w.max - len(marker)
	if remaining < 2 {
		remaining = w.max
		marker = nil
	}
	head := remaining / 2
	tail := remaining - head
	w.data = append(append(append([]byte{}, combined[:head]...), marker...), combined[len(combined)-tail:]...)
	return original, nil
}

func (w *cappedVMWriter) String() string { return string(w.data) }

func (shellVMRecipeRunner) Run(ctx context.Context, request vmRunRequest) vmProcessResult {
	command := newVMRecipeCommand(ctx, request.Command, request.RepoPath)
	command.Env = mergeVMEnvironment(os.Environ(), vmRecipeEnvironment(request))
	if request.Stdin != "" {
		command.Stdin = strings.NewReader(request.Stdin)
	}
	stdout := &cappedVMWriter{max: maxVMProcessCaptureBytes}
	stderr := &cappedVMWriter{max: maxVMProcessCaptureBytes}
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
	result := vmProcessResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if err == nil {
		return result
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result
	}
	result.ExitCode = -1
	result.Err = err
	return result
}

func vmRecipeEnvironment(request vmRunRequest) []string {
	return []string{
		"EVERYAPI_VM_MODE=" + string(request.Mode),
		"EVERYAPI_VM_INSTANCE_ID=" + request.Context.InstanceID,
		"EVERYAPI_RECIPE_ID=" + request.Context.RecipeID,
		"EVERYAPI_PROJECT_ID=" + request.Context.ProjectID,
		"EVERYAPI_WORKSPACE_ID=" + request.Context.WorkspaceID,
		"EVERYAPI_WORKSPACE_NAME=" + request.Context.WorkspaceName,
		"EVERYAPI_REPO_PATH=" + request.Context.RepoPath,
		"EVERYAPI_REPO_URL=" + request.Context.RepoURL,
		"EVERYAPI_REPO_BRANCH=" + request.Context.Branch,
		"EVERYAPI_VM_RESULT_SCHEMA_VERSION=" + strconv.Itoa(request.ResultSchemaVersion),
	}
}

func mergeVMEnvironment(base, overrides []string) []string {
	keys := make(map[string]struct{}, len(overrides))
	for _, entry := range overrides {
		key, _, _ := strings.Cut(entry, "=")
		if runtime.GOOS == "windows" {
			key = strings.ToUpper(key)
		}
		keys[key] = struct{}{}
	}
	merged := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if runtime.GOOS == "windows" {
			key = strings.ToUpper(key)
		}
		if _, replaced := keys[key]; !replaced {
			merged = append(merged, entry)
		}
	}
	return append(merged, overrides...)
}

var vmRecipeIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

func loadVMRecipes(repoPath string) (string, []vmRecipe, error) {
	repoPath, err := canonicalVMRepoPath(repoPath)
	if err != nil {
		return "", nil, err
	}
	configPath := filepath.Join(repoPath, vmConfigFile)
	if _, err := os.Lstat(configPath); errors.Is(err, os.ErrNotExist) {
		alternate := filepath.Join(repoPath, vmConfigFileAlternate)
		if _, alternateErr := os.Lstat(alternate); alternateErr == nil {
			configPath = alternate
		}
	}
	info, err := os.Lstat(configPath)
	if errors.Is(err, os.ErrNotExist) {
		return configPath, nil, fmt.Errorf("no %s found at %s", vmConfigFile, configPath)
	}
	if err != nil {
		return configPath, nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return configPath, nil, fmt.Errorf("VM recipe config must not be a symbolic link: %s", configPath)
	}
	if !info.Mode().IsRegular() {
		return configPath, nil, fmt.Errorf("VM recipe config is not a regular file: %s", configPath)
	}
	if info.Size() > maxVMConfigBytes {
		return configPath, nil, fmt.Errorf("VM recipe config exceeds %d bytes", maxVMConfigBytes)
	}
	file, err := os.Open(configPath)
	if err != nil {
		return configPath, nil, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return configPath, nil, err
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return configPath, nil, fmt.Errorf("VM recipe config changed while it was being opened: %s", configPath)
	}
	content, err := io.ReadAll(io.LimitReader(file, maxVMConfigBytes+1))
	if err != nil {
		return configPath, nil, err
	}
	if len(content) > maxVMConfigBytes {
		return configPath, nil, fmt.Errorf("VM recipe config exceeds %d bytes", maxVMConfigBytes)
	}
	var root yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	if err := decoder.Decode(&root); err != nil {
		return configPath, nil, fmt.Errorf("parse %s: %w", filepath.Base(configPath), err)
	}
	if nodes, aliases := countVMYAMLNodes(&root); nodes > maxVMConfigNodes || aliases > maxVMConfigAliases {
		return configPath, nil, fmt.Errorf("VM recipe config is too structurally complex")
	}
	var document vmRecipeDocument
	strictDecoder := yaml.NewDecoder(bytes.NewReader(content))
	strictDecoder.KnownFields(true)
	if err := strictDecoder.Decode(&document); err != nil {
		return configPath, nil, fmt.Errorf("parse %s: %w", filepath.Base(configPath), err)
	}
	if len(document.EnvironmentRecipes) > maxVMRecipes {
		return configPath, nil, fmt.Errorf("at most %d environment recipes are supported", maxVMRecipes)
	}
	recipes := make([]vmRecipe, 0, len(document.EnvironmentRecipes))
	seen := map[string]struct{}{}
	for index, raw := range document.EnvironmentRecipes {
		recipe, normalizeErr := normalizeVMRecipe(raw)
		if normalizeErr != nil {
			return configPath, nil, fmt.Errorf("environmentRecipes[%d]: %w", index, normalizeErr)
		}
		if _, exists := seen[recipe.ID]; exists {
			return configPath, nil, fmt.Errorf("duplicate VM recipe id %q", recipe.ID)
		}
		seen[recipe.ID] = struct{}{}
		recipes = append(recipes, recipe)
	}
	return configPath, recipes, nil
}

func canonicalVMRepoPath(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		value = "."
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve repo path: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("repo path does not exist or is not a directory: %s", abs)
	}
	return filepath.Clean(resolved), nil
}

func countVMYAMLNodes(node *yaml.Node) (nodes, aliases int) {
	if node == nil {
		return 0, 0
	}
	nodes = 1
	if node.Kind == yaml.AliasNode {
		aliases++
	}
	for _, child := range node.Content {
		childNodes, childAliases := countVMYAMLNodes(child)
		nodes += childNodes
		aliases += childAliases
	}
	return nodes, aliases
}

func normalizeVMRecipe(raw vmRecipeYAML) (vmRecipe, error) {
	trim := func(value string) string { return strings.TrimSpace(value) }
	raw.ID, raw.Name, raw.Description = trim(raw.ID), trim(raw.Name), trim(raw.Description)
	raw.Create, raw.Command = trim(raw.Create), trim(raw.Command)
	raw.Suspend, raw.Resume, raw.Destroy, raw.Cleanup = trim(raw.Suspend), trim(raw.Resume), trim(raw.Destroy), trim(raw.Cleanup)
	for name, value := range map[string]string{"id": raw.ID, "name": raw.Name, "description": raw.Description, "create": raw.Create, "command": raw.Command, "suspend": raw.Suspend, "resume": raw.Resume, "destroy": raw.Destroy, "cleanup": raw.Cleanup} {
		if len(value) > maxVMFieldBytes {
			return vmRecipe{}, fmt.Errorf("%s exceeds %d bytes", name, maxVMFieldBytes)
		}
	}
	if !vmRecipeIDPattern.MatchString(raw.ID) {
		return vmRecipe{}, fmt.Errorf("id %q must use 1-64 lowercase letters, numbers, dots, underscores, or hyphens", raw.ID)
	}
	if raw.Name == "" {
		return vmRecipe{}, errors.New("name is required")
	}
	create := raw.Create
	if create == "" {
		create = raw.Command
	}
	if create == "" {
		return vmRecipe{}, errors.New("create is required")
	}
	mode := raw.CheckoutMode
	if mode == "" {
		mode = vmCheckoutEveryAPIWorktree
	}
	if mode != vmCheckoutEveryAPIWorktree && mode != vmCheckoutProvisionedRoot {
		return vmRecipe{}, fmt.Errorf("checkoutMode must be %q or %q", vmCheckoutEveryAPIWorktree, vmCheckoutProvisionedRoot)
	}
	destroy := raw.Destroy
	if destroy == "" {
		destroy = raw.Cleanup
	}
	recipe := vmRecipe{ID: raw.ID, Name: raw.Name, Description: raw.Description, Create: create, CheckoutMode: mode, Suspend: raw.Suspend, Resume: raw.Resume}
	if destroy == "none" {
		recipe.DestroyDisabled = true
	} else {
		recipe.Destroy = destroy
	}
	return recipe, nil
}

func findVMRecipe(recipes []vmRecipe, id string) (vmRecipe, error) {
	for _, recipe := range recipes {
		if recipe.ID == id {
			return recipe, nil
		}
	}
	return vmRecipe{}, fmt.Errorf("VM recipe %q not found", id)
}

func vmRecipeResultSchemaVersion(recipe vmRecipe) int {
	if recipe.CheckoutMode == vmCheckoutProvisionedRoot {
		return 2
	}
	return 1
}

type vmRecipeResult struct {
	Raw         json.RawMessage
	ProjectRoot string
	Secrets     []string
}

func parseVMRecipeResult(stdout []byte, expectedMode vmCheckoutMode) (vmRecipeResult, error) {
	trimmed := bytes.TrimSpace(stdout)
	if len(trimmed) == 0 {
		return vmRecipeResult{}, errors.New("recipe produced no JSON result")
	}
	if len(trimmed) > maxVMProcessCaptureBytes {
		return vmRecipeResult{}, errors.New("recipe JSON result is too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil {
		return vmRecipeResult{}, errors.New("recipe stdout must be one JSON object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return vmRecipeResult{}, errors.New("recipe stdout must be one JSON object")
	}
	allowed := map[string]bool{"schemaVersion": true, "checkoutMode": true, "pairingCode": true, "projectRoot": true, "connection": true, "userData": true}
	for key := range object {
		if !allowed[key] {
			return vmRecipeResult{}, fmt.Errorf("recipe result contains unsupported field %q", key)
		}
	}
	var version int
	if err := json.Unmarshal(object["schemaVersion"], &version); err != nil || (version != 1 && version != 2) {
		return vmRecipeResult{}, errors.New("recipe result schemaVersion must be 1 or 2")
	}
	actualMode := vmCheckoutEveryAPIWorktree
	if version == 2 {
		actualMode = vmCheckoutProvisionedRoot
		var checkout string
		if err := json.Unmarshal(object["checkoutMode"], &checkout); err != nil || checkout != string(vmCheckoutProvisionedRoot) {
			return vmRecipeResult{}, fmt.Errorf("schemaVersion 2 requires checkoutMode %q", vmCheckoutProvisionedRoot)
		}
	}
	if actualMode != expectedMode {
		return vmRecipeResult{}, fmt.Errorf("recipe result checkout mode %q does not match configured mode %q", actualMode, expectedMode)
	}
	projectRoot := ""
	if connectionRaw, ok := object["connection"]; ok {
		var connection map[string]json.RawMessage
		if err := json.Unmarshal(connectionRaw, &connection); err != nil {
			return vmRecipeResult{}, errors.New("recipe result connection must be an object")
		}
		var connectionType string
		if err := json.Unmarshal(connection["type"], &connectionType); err != nil {
			return vmRecipeResult{}, errors.New("recipe result connection.type is required")
		}
		switch connectionType {
		case "everyapi-server":
			for key := range connection {
				if key != "type" && key != "pairingCode" && key != "projectRoot" {
					return vmRecipeResult{}, fmt.Errorf("server connection contains unsupported field %q", key)
				}
			}
			var pairingCode string
			if err := json.Unmarshal(connection["pairingCode"], &pairingCode); err != nil || strings.TrimSpace(pairingCode) == "" {
				return vmRecipeResult{}, errors.New("server connection pairingCode is required")
			}
			if !strings.HasPrefix(pairingCode, "everyapi://pair?code=") {
				return vmRecipeResult{}, errors.New("server connection pairingCode must be an EveryAPI pairing URL")
			}
			if err := json.Unmarshal(connection["projectRoot"], &projectRoot); err != nil {
				return vmRecipeResult{}, errors.New("server connection projectRoot is required")
			}
		case "ssh":
			for key := range connection {
				if key != "type" && key != "target" && key != "projectRoot" {
					return vmRecipeResult{}, fmt.Errorf("SSH connection contains unsupported field %q", key)
				}
			}
			var target map[string]any
			if err := json.Unmarshal(connection["target"], &target); err != nil || strings.TrimSpace(fmt.Sprint(target["host"])) == "" {
				return vmRecipeResult{}, errors.New("SSH connection target.host is required")
			}
			if err := json.Unmarshal(connection["projectRoot"], &projectRoot); err != nil {
				return vmRecipeResult{}, errors.New("SSH connection projectRoot is required")
			}
		default:
			return vmRecipeResult{}, fmt.Errorf("unsupported recipe connection type %q", connectionType)
		}
	} else {
		var pairingCode string
		if err := json.Unmarshal(object["pairingCode"], &pairingCode); err != nil || !strings.HasPrefix(pairingCode, "everyapi://pair?code=") {
			return vmRecipeResult{}, errors.New("legacy recipe result pairingCode must be an EveryAPI pairing URL")
		}
		if err := json.Unmarshal(object["projectRoot"], &projectRoot); err != nil {
			return vmRecipeResult{}, errors.New("legacy recipe result projectRoot is required")
		}
	}
	if !isAbsoluteVMRuntimePath(projectRoot) {
		return vmRecipeResult{}, errors.New("recipe result projectRoot must be an absolute runtime path")
	}
	secrets := collectVMResultSecrets(object["userData"])
	canonical, err := json.Marshal(object)
	if err != nil {
		return vmRecipeResult{}, err
	}
	return vmRecipeResult{Raw: canonical, ProjectRoot: projectRoot, Secrets: secrets}, nil
}

func isAbsoluteVMRuntimePath(value string) bool {
	return strings.HasPrefix(value, "/") || strings.HasPrefix(value, `\\`) || strings.HasPrefix(value, "//") || regexp.MustCompile(`^[A-Za-z]:[\\/]`).MatchString(value)
}

func collectVMResultSecrets(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	var secrets []string
	var walk func(any, string)
	walk = func(entry any, key string) {
		switch typed := entry.(type) {
		case map[string]any:
			for childKey, child := range typed {
				walk(child, childKey)
			}
		case []any:
			for _, child := range typed {
				walk(child, key)
			}
		case string:
			if regexp.MustCompile(`(?i)token|secret|password|api[-_]?key|access[-_]?key|private[-_]?key`).MatchString(key) && typed != "" {
				secrets = append(secrets, typed)
			}
		}
	}
	walk(value, "")
	return secrets
}

func redactVMDiagnostic(value string, secrets []string) string {
	redacted := regexp.MustCompile(`(?i)(https?://)[^/@\s:]+:[^/@\s]+@`).ReplaceAllString(value, `${1}[redacted]@`)
	redacted = regexp.MustCompile(`everyapi://pair\?code=[A-Za-z0-9_-]+`).ReplaceAllString(redacted, "everyapi://pair?code=[redacted]")
	redacted = regexp.MustCompile(`(?i)("(?:pairingCode|deviceToken|token|secret|password|apiKey|accessToken|identityFile|identityAgent|proxyCommand)"\s*:\s*)"[^"]*"`).ReplaceAllString(redacted, `${1}"[redacted]"`)
	for _, secret := range secrets {
		if len(secret) >= 3 {
			redacted = strings.ReplaceAll(redacted, secret, "[redacted]")
		}
	}
	if len(redacted) <= maxVMDiagnosticBytes {
		return redacted
	}
	half := maxVMDiagnosticBytes / 2
	return redacted[:half] + fmt.Sprintf("\n…[%d chars omitted]…\n", len(redacted)-half*2) + redacted[len(redacted)-half:]
}

func vmResultSchemaVersion(mode vmCheckoutMode) int {
	if mode == vmCheckoutProvisionedRoot {
		return 2
	}
	return 1
}

func vmNowMillis() int64 { return time.Now().UnixMilli() }
