package workspace

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
)

type vmRuntimeStatus string

const (
	vmRuntimeProvisioning   vmRuntimeStatus = "provisioning"
	vmRuntimeRunning        vmRuntimeStatus = "running"
	vmRuntimeSuspended      vmRuntimeStatus = "suspended"
	vmRuntimeSuspendFailed  vmRuntimeStatus = "suspend_failed"
	vmRuntimeResumeFailed   vmRuntimeStatus = "resume_failed"
	vmRuntimeFailed         vmRuntimeStatus = "failed"
	vmRuntimeCleanupPending vmRuntimeStatus = "cleanup_pending"
	vmRuntimeCleanupFailed  vmRuntimeStatus = "cleanup_failed"
	vmRuntimeCleaned        vmRuntimeStatus = "cleaned"
)

type vmCleanupStatus string

const (
	vmCleanupNotStarted vmCleanupStatus = "not_started"
	vmCleanupDisabled   vmCleanupStatus = "disabled"
	vmCleanupRunning    vmCleanupStatus = "running"
	vmCleanupSucceeded  vmCleanupStatus = "succeeded"
	vmCleanupFailed     vmCleanupStatus = "failed"
)

type vmRuntimeRecord struct {
	ID                   string          `json:"id"`
	RecipeID             string          `json:"recipeId"`
	Recipe               vmRecipe        `json:"recipe"`
	RepoPath             string          `json:"repoPath"`
	ProjectID            string          `json:"projectId,omitempty"`
	WorkspaceID          string          `json:"workspaceId,omitempty"`
	WorkspaceName        string          `json:"workspaceName,omitempty"`
	RepoURL              string          `json:"repoUrl,omitempty"`
	Branch               string          `json:"branch,omitempty"`
	Status               vmRuntimeStatus `json:"status"`
	CleanupStatus        vmCleanupStatus `json:"cleanupStatus"`
	CleanupLastAttemptAt int64           `json:"cleanupLastAttemptAt,omitempty"`
	LastError            string          `json:"lastError,omitempty"`
	CreatedAt            int64           `json:"createdAt"`
	UpdatedAt            int64           `json:"updatedAt"`
	RecipeResult         json.RawMessage `json:"recipeResult,omitempty"`
}

type vmRuntimeStore struct {
	Version  int               `json:"version"`
	Runtimes []vmRuntimeRecord `json:"runtimes"`
}

type vmLifecycleAction string

const (
	vmActionSuspend vmLifecycleAction = "suspend"
	vmActionResume  vmLifecycleAction = "resume"
	vmActionCleanup vmLifecycleAction = "cleanup"
)

type vmCleanupInfo struct {
	RuntimeID string          `json:"runtimeId"`
	Command   string          `json:"command,omitempty"`
	Payload   json.RawMessage `json:"payload"`
	Disabled  bool            `json:"disabled"`
}

func vmRuntimeCommand(args []string) (any, error) {
	operation := "list"
	if len(args) > 0 {
		operation = args[0]
	}
	switch operation {
	case "list":
		store, err := loadVMRuntimeStore()
		if err != nil {
			return nil, err
		}
		return store.Runtimes, nil
	case "show":
		id, err := vmRuntimeID(args[1:])
		if err != nil {
			return nil, err
		}
		store, err := loadVMRuntimeStore()
		if err != nil {
			return nil, err
		}
		_, record, err := findVMRuntime(store, id)
		return record, err
	case "create":
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		return vmRuntimeCreate(ctx, args[1:], shellVMRecipeRunner{})
	case string(vmActionSuspend):
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		return vmRuntimeAction(ctx, vmActionSuspend, args[1:], shellVMRecipeRunner{})
	case string(vmActionResume):
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		return vmRuntimeAction(ctx, vmActionResume, args[1:], shellVMRecipeRunner{})
	case string(vmActionCleanup):
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		return vmRuntimeAction(ctx, vmActionCleanup, args[1:], shellVMRecipeRunner{})
	case "cleanup-info":
		id, err := vmRuntimeID(args[1:])
		if err != nil {
			return nil, err
		}
		store, err := loadVMRuntimeStore()
		if err != nil {
			return nil, err
		}
		_, record, err := findVMRuntime(store, id)
		if err != nil {
			return nil, err
		}
		return buildVMCleanupInfo(record)
	case "forget":
		id, err := vmRuntimeID(args[1:])
		if err != nil {
			return nil, err
		}
		store, err := loadVMRuntimeStore()
		if err != nil {
			return nil, err
		}
		index, record, err := findVMRuntime(store, id)
		if err != nil {
			return nil, err
		}
		if record.Status != vmRuntimeCleaned && !hasFlag(args[1:], "--force") {
			return nil, errors.New("runtime must be cleaned before forgetting it; pass --force to remove only the local record")
		}
		store.Runtimes = append(store.Runtimes[:index], store.Runtimes[index+1:]...)
		if err := saveVMRuntimeStore(store); err != nil {
			return nil, err
		}
		return map[string]any{"id": id, "forgotten": true}, nil
	default:
		return nil, fmt.Errorf("unknown vm runtime subcommand %q", operation)
	}
}

func vmRuntimeCreate(ctx context.Context, args []string, runner vmRecipeRunner) (vmRuntimeRecord, error) {
	positions := positional(args)
	recipeID := flagValue(args, "recipe-id", "")
	if recipeID == "" && len(positions) > 0 {
		recipeID = positions[0]
	}
	if recipeID == "" {
		return vmRuntimeRecord{}, errors.New("VM recipe id is required")
	}
	repoPath, err := canonicalVMRepoPath(flagValue(args, "repo-path", "."))
	if err != nil {
		return vmRuntimeRecord{}, err
	}
	_, recipes, err := loadVMRecipes(repoPath)
	if err != nil {
		return vmRuntimeRecord{}, err
	}
	recipe, err := findVMRecipe(recipes, recipeID)
	if err != nil {
		return vmRuntimeRecord{}, err
	}
	checks := doctorVMRecipe(repoPath, recipe)
	if !vmChecksOK(checks) {
		return vmRuntimeRecord{}, fmt.Errorf("VM recipe %q failed non-destructive doctor checks", recipeID)
	}
	instanceID := flagValue(args, "instance-id", "")
	if instanceID == "" {
		instanceID, err = newVMInstanceID()
		if err != nil {
			return vmRuntimeRecord{}, err
		}
	}
	if !vmRecipeIDPattern.MatchString(instanceID) {
		return vmRuntimeRecord{}, fmt.Errorf("instance id %q must use 1-64 lowercase letters, numbers, dots, underscores, or hyphens", instanceID)
	}
	store, err := loadVMRuntimeStore()
	if err != nil {
		return vmRuntimeRecord{}, err
	}
	if _, _, findErr := findVMRuntime(store, instanceID); findErr == nil {
		return vmRuntimeRecord{}, fmt.Errorf("VM runtime %q already exists", instanceID)
	}
	now := vmNowMillis()
	record := vmRuntimeRecord{
		ID: instanceID, RecipeID: recipe.ID, Recipe: recipe, RepoPath: repoPath,
		ProjectID: flagValue(args, "project-id", ""), WorkspaceID: flagValue(args, "workspace-id", ""), WorkspaceName: flagValue(args, "workspace-name", ""),
		RepoURL: flagValue(args, "repo-url", ""), Branch: flagValue(args, "branch", ""),
		Status: vmRuntimeProvisioning, CleanupStatus: vmCleanupNotStarted, CreatedAt: now, UpdatedAt: now,
	}
	if recipe.DestroyDisabled {
		record.CleanupStatus = vmCleanupDisabled
	}
	store.Runtimes = append(store.Runtimes, record)
	if err := saveVMRuntimeStore(store); err != nil {
		return vmRuntimeRecord{}, err
	}
	runContext := vmContextFromRecord(record)
	result := runner.Run(ctx, vmRunRequest{Command: recipe.Create, RepoPath: repoPath, Mode: vmModeCreate, Context: runContext, ResultSchemaVersion: vmRecipeResultSchemaVersion(recipe)})
	if result.Err != nil || result.ExitCode != 0 {
		record.Status = vmRuntimeFailed
		record.LastError = vmProcessFailure("provision", result)
		record.UpdatedAt = vmNowMillis()
		persistErr := updateVMRuntimeRecord(record)
		return record, errors.Join(errors.New(record.LastError), persistErr)
	}
	parsed, err := parseVMRecipeResult([]byte(result.Stdout), recipe.CheckoutMode)
	if err != nil {
		record.Status = vmRuntimeFailed
		record.LastError = err.Error()
		record.UpdatedAt = vmNowMillis()
		persistErr := updateVMRuntimeRecord(record)
		return record, errors.Join(err, persistErr)
	}
	record.Status = vmRuntimeRunning
	record.RecipeResult = parsed.Raw
	record.UpdatedAt = vmNowMillis()
	if err := updateVMRuntimeRecord(record); err != nil {
		return record, err
	}
	return record, nil
}

func vmRuntimeAction(ctx context.Context, action vmLifecycleAction, args []string, runner vmRecipeRunner) (vmRuntimeRecord, error) {
	id, err := vmRuntimeID(args)
	if err != nil {
		return vmRuntimeRecord{}, err
	}
	store, err := loadVMRuntimeStore()
	if err != nil {
		return vmRuntimeRecord{}, err
	}
	_, record, err := findVMRuntime(store, id)
	if err != nil {
		return vmRuntimeRecord{}, err
	}
	mode := vmModeSuspend
	command := record.Recipe.Suspend
	switch action {
	case vmActionSuspend:
		if record.Status != vmRuntimeRunning && record.Status != vmRuntimeSuspendFailed {
			return record, fmt.Errorf("runtime %q cannot suspend from status %q", id, record.Status)
		}
		if command == "" {
			return record, fmt.Errorf("VM recipe %q has no suspend action", record.RecipeID)
		}
	case vmActionResume:
		if record.Status != vmRuntimeSuspended && record.Status != vmRuntimeResumeFailed {
			return record, fmt.Errorf("runtime %q cannot resume from status %q", id, record.Status)
		}
		mode, command = vmModeResume, record.Recipe.Resume
		if command == "" {
			return record, fmt.Errorf("VM recipe %q has no resume action", record.RecipeID)
		}
	case vmActionCleanup:
		if record.Status == vmRuntimeCleaned {
			return record, nil
		}
		if record.Recipe.DestroyDisabled || record.Recipe.Destroy == "" {
			record.Status = vmRuntimeCleaned
			record.CleanupStatus = vmCleanupDisabled
			record.UpdatedAt = vmNowMillis()
			return record, updateVMRuntimeRecord(record)
		}
		mode, command = vmModeDestroy, record.Recipe.Destroy
		record.Status = vmRuntimeCleanupPending
		record.CleanupStatus = vmCleanupRunning
		record.CleanupLastAttemptAt = vmNowMillis()
		record.UpdatedAt = record.CleanupLastAttemptAt
		if err := updateVMRuntimeRecord(record); err != nil {
			return record, err
		}
	default:
		return record, fmt.Errorf("unknown VM runtime action %q", action)
	}
	result := runVMRecipeLifecycle(ctx, runner, record.Recipe, record.RepoPath, vmContextFromRecord(record), mode, record.RecipeResult)
	if result.Err != nil || result.ExitCode != 0 {
		record.LastError = vmProcessFailure(string(action), result)
		switch action {
		case vmActionSuspend:
			record.Status = vmRuntimeSuspendFailed
		case vmActionResume:
			record.Status = vmRuntimeResumeFailed
		case vmActionCleanup:
			record.Status = vmRuntimeCleanupFailed
			record.CleanupStatus = vmCleanupFailed
		}
		record.UpdatedAt = vmNowMillis()
		persistErr := updateVMRuntimeRecord(record)
		return record, errors.Join(errors.New(record.LastError), persistErr)
	}
	if action == vmActionResume {
		parsed, parseErr := parseVMRecipeResult([]byte(result.Stdout), record.Recipe.CheckoutMode)
		if parseErr != nil {
			record.Status = vmRuntimeResumeFailed
			record.LastError = parseErr.Error()
			record.UpdatedAt = vmNowMillis()
			persistErr := updateVMRuntimeRecord(record)
			return record, errors.Join(parseErr, persistErr)
		}
		record.RecipeResult = parsed.Raw
	}
	record.LastError = ""
	switch action {
	case vmActionSuspend:
		record.Status = vmRuntimeSuspended
	case vmActionResume:
		record.Status = vmRuntimeRunning
	case vmActionCleanup:
		record.Status = vmRuntimeCleaned
		record.CleanupStatus = vmCleanupSucceeded
	}
	record.UpdatedAt = vmNowMillis()
	if err := updateVMRuntimeRecord(record); err != nil {
		return record, err
	}
	return record, nil
}

func runVMRecipeLifecycle(ctx context.Context, runner vmRecipeRunner, recipe vmRecipe, repoPath string, runContext vmRunContext, mode vmMode, result json.RawMessage) vmProcessResult {
	command := ""
	switch mode {
	case vmModeSuspend:
		command = recipe.Suspend
	case vmModeResume:
		command = recipe.Resume
	case vmModeDestroy:
		command = recipe.Destroy
	}
	if command == "" {
		return vmProcessResult{}
	}
	payload, err := buildVMLifecyclePayload(mode, runContext, result)
	if err != nil {
		return vmProcessResult{ExitCode: -1, Err: err}
	}
	return runner.Run(ctx, vmRunRequest{Command: command, RepoPath: repoPath, Mode: mode, Context: runContext, ResultSchemaVersion: vmRecipeResultSchemaVersion(recipe), Stdin: string(payload) + "\n"})
}

func buildVMLifecyclePayload(mode vmMode, runContext vmRunContext, result json.RawMessage) (json.RawMessage, error) {
	var recipeResult any
	if len(result) > 0 {
		if err := json.Unmarshal(result, &recipeResult); err != nil {
			return nil, err
		}
	}
	return json.Marshal(map[string]any{
		"schemaVersion": 1,
		"mode":          mode,
		"recipeId":      runContext.RecipeID,
		"instanceId":    runContext.InstanceID,
		"projectId":     runContext.ProjectID,
		"workspaceId":   runContext.WorkspaceID,
		"workspaceName": runContext.WorkspaceName,
		"recipeResult":  recipeResult,
	})
}

func buildVMCleanupInfo(record vmRuntimeRecord) (vmCleanupInfo, error) {
	payload, err := buildVMLifecyclePayload(vmModeDestroy, vmContextFromRecord(record), record.RecipeResult)
	if err != nil {
		return vmCleanupInfo{}, err
	}
	info := vmCleanupInfo{RuntimeID: record.ID, Payload: payload, Disabled: record.Recipe.DestroyDisabled || record.Recipe.Destroy == ""}
	if !info.Disabled {
		encoded := base64.StdEncoding.EncodeToString(append(payload, '\n'))
		info.Command = vmManualCleanupCommand(encoded, record.Recipe.Destroy)
	}
	return info, nil
}

func vmContextFromRecord(record vmRuntimeRecord) vmRunContext {
	return vmRunContext{InstanceID: record.ID, RecipeID: record.RecipeID, ProjectID: record.ProjectID, WorkspaceID: record.WorkspaceID, WorkspaceName: record.WorkspaceName, RepoPath: record.RepoPath, RepoURL: record.RepoURL, Branch: record.Branch}
}

func newVMInstanceID() (string, error) {
	value := make([]byte, 12)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return "vm-" + hex.EncodeToString(value), nil
}

func vmRuntimeID(args []string) (string, error) {
	id := flagValue(args, "id", "")
	if id == "" {
		positions := positional(args)
		if len(positions) > 0 {
			id = positions[0]
		}
	}
	if id == "" {
		return "", errors.New("VM runtime id is required")
	}
	return id, nil
}

func loadVMRuntimeStore() (vmRuntimeStore, error) {
	path, err := statePath(vmRuntimeStoreFile)
	if err != nil {
		return vmRuntimeStore{}, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return vmRuntimeStore{Version: 1, Runtimes: []vmRuntimeRecord{}}, nil
	}
	if err != nil {
		return vmRuntimeStore{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return vmRuntimeStore{}, errors.New("VM runtime store must be a regular non-symbolic-link file")
	}
	if info.Size() > maxVMRuntimeStoreBytes {
		return vmRuntimeStore{}, errors.New("VM runtime store exceeds its durable capacity")
	}
	file, err := os.Open(path)
	if err != nil {
		return vmRuntimeStore{}, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return vmRuntimeStore{}, err
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return vmRuntimeStore{}, errors.New("VM runtime store changed while it was being opened")
	}
	if err := file.Chmod(0o600); err != nil {
		return vmRuntimeStore{}, err
	}
	data, err := io.ReadAll(io.LimitReader(file, maxVMRuntimeStoreBytes+1))
	if err != nil {
		return vmRuntimeStore{}, err
	}
	if len(data) > maxVMRuntimeStoreBytes {
		return vmRuntimeStore{}, errors.New("VM runtime store exceeds its durable capacity")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var store vmRuntimeStore
	if err := decoder.Decode(&store); err != nil {
		return vmRuntimeStore{}, fmt.Errorf("read VM runtime store: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return vmRuntimeStore{}, errors.New("VM runtime store must contain one JSON object")
	}
	if store.Version != 1 {
		return vmRuntimeStore{}, fmt.Errorf("unsupported VM runtime store version %d", store.Version)
	}
	if len(store.Runtimes) > 1000 {
		return vmRuntimeStore{}, errors.New("VM runtime store exceeds 1000 records")
	}
	seen := make(map[string]struct{}, len(store.Runtimes))
	for _, record := range store.Runtimes {
		if err := validateVMRuntimeRecord(record); err != nil {
			return vmRuntimeStore{}, fmt.Errorf("VM runtime store contains an invalid record %q: %w", record.ID, err)
		}
		if _, exists := seen[record.ID]; exists {
			return vmRuntimeStore{}, fmt.Errorf("VM runtime store contains duplicate id %q", record.ID)
		}
		seen[record.ID] = struct{}{}
	}
	sortVMRuntimes(store.Runtimes)
	return store, nil
}

func saveVMRuntimeStore(store vmRuntimeStore) error {
	store.Version = 1
	seen := make(map[string]struct{}, len(store.Runtimes))
	for _, record := range store.Runtimes {
		if err := validateVMRuntimeRecord(record); err != nil {
			return fmt.Errorf("refuse to persist invalid VM runtime %q: %w", record.ID, err)
		}
		if _, exists := seen[record.ID]; exists {
			return fmt.Errorf("refuse to persist duplicate VM runtime id %q", record.ID)
		}
		seen[record.ID] = struct{}{}
	}
	sortVMRuntimes(store.Runtimes)
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > maxVMRuntimeStoreBytes {
		return errors.New("VM runtime store exceeds its durable capacity")
	}
	path, err := statePath(vmRuntimeStoreFile)
	if err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("VM runtime store must not be a symbolic link")
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".vm-runtimes-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return nil
}

func updateVMRuntimeRecord(record vmRuntimeRecord) error {
	store, err := loadVMRuntimeStore()
	if err != nil {
		return err
	}
	index, _, err := findVMRuntime(store, record.ID)
	if err != nil {
		return err
	}
	store.Runtimes[index] = record
	return saveVMRuntimeStore(store)
}

func findVMRuntime(store vmRuntimeStore, id string) (int, vmRuntimeRecord, error) {
	for index, record := range store.Runtimes {
		if record.ID == id {
			return index, record, nil
		}
	}
	return -1, vmRuntimeRecord{}, fmt.Errorf("VM runtime %q not found", id)
}

func sortVMRuntimes(records []vmRuntimeRecord) {
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].CreatedAt == records[j].CreatedAt {
			return strings.Compare(records[i].ID, records[j].ID) < 0
		}
		return records[i].CreatedAt > records[j].CreatedAt
	})
}

func validateVMRuntimeRecord(record vmRuntimeRecord) error {
	if !vmRecipeIDPattern.MatchString(record.ID) || !vmRecipeIDPattern.MatchString(record.RecipeID) {
		return errors.New("invalid runtime or recipe id")
	}
	if record.Recipe.ID != record.RecipeID || record.Recipe.Create == "" {
		return errors.New("invalid recipe snapshot")
	}
	if !filepath.IsAbs(record.RepoPath) {
		return errors.New("repoPath must be absolute")
	}
	validStatuses := map[vmRuntimeStatus]bool{
		vmRuntimeProvisioning: true, vmRuntimeRunning: true, vmRuntimeSuspended: true,
		vmRuntimeSuspendFailed: true, vmRuntimeResumeFailed: true, vmRuntimeFailed: true,
		vmRuntimeCleanupPending: true, vmRuntimeCleanupFailed: true, vmRuntimeCleaned: true,
	}
	if !validStatuses[record.Status] {
		return fmt.Errorf("unsupported status %q", record.Status)
	}
	validCleanupStatuses := map[vmCleanupStatus]bool{
		vmCleanupNotStarted: true, vmCleanupDisabled: true, vmCleanupRunning: true,
		vmCleanupSucceeded: true, vmCleanupFailed: true,
	}
	if !validCleanupStatuses[record.CleanupStatus] {
		return fmt.Errorf("unsupported cleanup status %q", record.CleanupStatus)
	}
	if record.CreatedAt <= 0 || record.UpdatedAt <= 0 {
		return errors.New("createdAt and updatedAt must be positive")
	}
	if len(record.RecipeResult) > 0 {
		if _, err := parseVMRecipeResult(record.RecipeResult, record.Recipe.CheckoutMode); err != nil {
			return fmt.Errorf("invalid recipe result: %w", err)
		}
	}
	return nil
}
