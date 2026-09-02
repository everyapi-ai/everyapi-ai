package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestVMRecipeListAndDoctorReadEveryAPIYAML(t *testing.T) {
	repo := t.TempDir()
	writeVMTestFile(t, filepath.Join(repo, "scripts", "create.sh"), "#!/bin/sh\n", 0o700)
	writeVMTestFile(t, filepath.Join(repo, "scripts", "destroy.sh"), "#!/bin/sh\n", 0o700)
	writeVMTestFile(t, filepath.Join(repo, "everyapi.yaml"), `
scripts:
  setup: ./scripts/setup.sh
environmentRecipes:
  - id: cloud-sandbox
    name: Cloud sandbox
    description: Disposable provider runtime
    create: ./scripts/create.sh
    suspend: ./scripts/suspend.sh
    destroy: ./scripts/destroy.sh
`, 0o600)

	value, err := vmCommand([]string{"recipe", "list", "--repo-path", repo})
	if err != nil {
		t.Fatal(err)
	}
	list := value.(vmRecipeListResult)
	canonicalRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if list.ConfigPath != filepath.Join(canonicalRepo, "everyapi.yaml") || len(list.Recipes) != 1 {
		t.Fatalf("recipe list = %#v", list)
	}
	if list.Recipes[0].ID != "cloud-sandbox" || list.Recipes[0].Available {
		t.Fatalf("recipe = %#v, want unavailable because suspend script is missing", list.Recipes[0])
	}

	value, err = vmCommand([]string{"recipe", "doctor", "cloud-sandbox", "--repo-path", repo})
	if err == nil {
		t.Fatal("doctor should return a failing command status for a missing lifecycle script")
	}
	doctor := value.(vmDoctorResult)
	if doctor.OK {
		t.Fatalf("doctor = %#v, want failed missing suspend script", doctor)
	}
	if !hasVMCheck(doctor.Checks, "recipe.suspend", vmCheckFail) {
		t.Fatalf("doctor checks = %#v", doctor.Checks)
	}
}

func TestVMRecipeConfigRejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on Windows")
	}
	repo := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.yaml")
	writeVMTestFile(t, outside, "environmentRecipes: []\n", 0o600)
	if err := os.Symlink(outside, filepath.Join(repo, "everyapi.yaml")); err != nil {
		t.Fatal(err)
	}
	_, err := vmCommand([]string{"recipe", "list", "--repo-path", repo})
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("vm recipe list error = %v, want symlink rejection", err)
	}
}

func TestVMDoctorProvisionAlwaysCleansUpAndRedactsTranscript(t *testing.T) {
	repo := vmTestRepo(t)
	runner := &fakeVMRecipeRunner{results: []vmProcessResult{
		{Stdout: validVMRecipeResult("/workspace/project", "super-secret")},
		{Stdout: "destroyed super-secret\n"},
	}}

	result, err := vmDoctorWithRunner(context.Background(), repo, "cloud-sandbox", true, runner)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || len(runner.calls) != 2 {
		t.Fatalf("doctor = %#v, calls = %#v", result, runner.calls)
	}
	if runner.calls[0].Mode != vmModeCreate || runner.calls[1].Mode != vmModeDestroy {
		t.Fatalf("runner modes = %#v", runner.calls)
	}
	if strings.Contains(result.ProvisionTranscript.Provision.Stdout, "super-secret") || strings.Contains(result.ProvisionTranscript.Destroy.Stdout, "super-secret") {
		t.Fatalf("doctor transcript leaked secret: %#v", result.ProvisionTranscript)
	}
	if !strings.Contains(runner.calls[1].Stdin, `"mode":"destroy"`) || !strings.Contains(runner.calls[1].Stdin, `"recipeId":"cloud-sandbox"`) {
		t.Fatalf("cleanup stdin = %q", runner.calls[1].Stdin)
	}
}

func TestVMRuntimeLifecyclePersistsRecipeSnapshot(t *testing.T) {
	repo := vmTestRepo(t)
	stateDir := t.TempDir()
	t.Setenv("EVERYAPI_WORKSPACE_STATE_DIR", stateDir)
	runner := &fakeVMRecipeRunner{results: []vmProcessResult{
		{Stdout: validVMRecipeResult("/workspace/project", "token-one")},
		{Stdout: "suspended\n"},
		{Stdout: validVMRecipeResult("/workspace/project", "token-two")},
		{Stdout: "destroyed\n"},
	}}

	created, err := vmRuntimeCreate(context.Background(), []string{"cloud-sandbox", "--repo-path", repo, "--instance-id", "vm-test"}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != "vm-test" || created.Status != vmRuntimeRunning || created.Recipe.ID != "cloud-sandbox" {
		t.Fatalf("created runtime = %#v", created)
	}
	if _, err := vmRuntimeAction(context.Background(), vmActionSuspend, []string{"vm-test"}, runner); err != nil {
		t.Fatal(err)
	}
	resumed, err := vmRuntimeAction(context.Background(), vmActionResume, []string{"vm-test"}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Status != vmRuntimeRunning || !strings.Contains(string(resumed.RecipeResult), "token-two") {
		t.Fatalf("resumed runtime = %#v", resumed)
	}
	cleaned, err := vmRuntimeAction(context.Background(), vmActionCleanup, []string{"vm-test"}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if cleaned.Status != vmRuntimeCleaned || cleaned.CleanupStatus != vmCleanupSucceeded {
		t.Fatalf("cleaned runtime = %#v", cleaned)
	}

	store, err := loadVMRuntimeStore()
	if err != nil {
		t.Fatal(err)
	}
	if len(store.Runtimes) != 1 || store.Runtimes[0].Recipe.Create != "./scripts/create.sh" {
		t.Fatalf("runtime store = %#v", store)
	}
	info, err := os.Stat(filepath.Join(stateDir, vmRuntimeStoreFile))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("runtime store permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestVMRuntimeCreateRejectsInvalidResultAndKeepsFailureRecord(t *testing.T) {
	repo := vmTestRepo(t)
	t.Setenv("EVERYAPI_WORKSPACE_STATE_DIR", t.TempDir())
	runner := &fakeVMRecipeRunner{results: []vmProcessResult{{Stdout: "not json\n"}}}

	record, err := vmRuntimeCreate(context.Background(), []string{"cloud-sandbox", "--repo-path", repo, "--instance-id", "vm-bad"}, runner)
	if err == nil || !strings.Contains(err.Error(), "one JSON object") {
		t.Fatalf("create error = %v", err)
	}
	if record.Status != vmRuntimeFailed || record.LastError == "" {
		t.Fatalf("failed runtime = %#v", record)
	}
	store, loadErr := loadVMRuntimeStore()
	if loadErr != nil || len(store.Runtimes) != 1 || store.Runtimes[0].Status != vmRuntimeFailed {
		t.Fatalf("runtime store = %#v, err = %v", store, loadErr)
	}
}

func TestVMRuntimeCleanupFailureRemainsRetryable(t *testing.T) {
	repo := vmTestRepo(t)
	t.Setenv("EVERYAPI_WORKSPACE_STATE_DIR", t.TempDir())
	createRunner := &fakeVMRecipeRunner{results: []vmProcessResult{{Stdout: validVMRecipeResult("/workspace/project", "token")}}}
	if _, err := vmRuntimeCreate(context.Background(), []string{"cloud-sandbox", "--repo-path", repo, "--instance-id", "vm-retry"}, createRunner); err != nil {
		t.Fatal(err)
	}
	failingRunner := &fakeVMRecipeRunner{results: []vmProcessResult{{ExitCode: 12, Stderr: "provider refused cleanup"}}}
	record, err := vmRuntimeAction(context.Background(), vmActionCleanup, []string{"vm-retry"}, failingRunner)
	if err == nil || record.Status != vmRuntimeCleanupFailed || record.CleanupStatus != vmCleanupFailed {
		t.Fatalf("cleanup record = %#v, err = %v", record, err)
	}
	retryRunner := &fakeVMRecipeRunner{results: []vmProcessResult{{Stdout: "done\n"}}}
	record, err = vmRuntimeAction(context.Background(), vmActionCleanup, []string{"vm-retry"}, retryRunner)
	if err != nil || record.Status != vmRuntimeCleaned {
		t.Fatalf("retry record = %#v, err = %v", record, err)
	}
}

func TestVMRuntimeCancelStopsActiveCleanup(t *testing.T) {
	repo := vmTestRepo(t)
	stateDir := t.TempDir()
	t.Setenv("EVERYAPI_WORKSPACE_STATE_DIR", stateDir)
	createRunner := &fakeVMRecipeRunner{results: []vmProcessResult{{Stdout: validVMRecipeResult("/workspace/project", "token")}}}
	if _, err := vmRuntimeCreate(context.Background(), []string{"cloud-sandbox", "--repo-path", repo, "--instance-id", "vm-cancel"}, createRunner); err != nil {
		t.Fatal(err)
	}

	type actionResult struct {
		record vmRuntimeRecord
		err    error
	}
	runner := &blockingVMRecipeRunner{started: make(chan struct{})}
	done := make(chan actionResult, 1)
	go func() {
		record, err := vmRuntimeAction(context.Background(), vmActionCleanup, []string{"vm-cancel"}, runner)
		done <- actionResult{record: record, err: err}
	}()
	select {
	case <-runner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup did not start")
	}
	if _, err := vmRuntimeAction(context.Background(), vmActionCleanup, []string{"vm-cancel"}, &fakeVMRecipeRunner{}); err == nil || !strings.Contains(err.Error(), "already has an active cleanup operation") {
		t.Fatalf("concurrent cleanup error = %v", err)
	}

	value, err := vmRuntimeCommand([]string{"cancel", "vm-cancel"})
	if err != nil {
		t.Fatal(err)
	}
	cancelResult := value.(vmRuntimeCancelResult)
	if cancelResult.RuntimeID != "vm-cancel" || cancelResult.Action != string(vmActionCleanup) || !cancelResult.CancellationRequested {
		t.Fatalf("cancel result = %#v", cancelResult)
	}

	select {
	case result := <-done:
		if result.err == nil || !strings.Contains(result.err.Error(), "cleanup cancelled by request") {
			t.Fatalf("cleanup result = %#v, err = %v", result.record, result.err)
		}
		if result.record.Status != vmRuntimeCleanupFailed || result.record.CleanupStatus != vmCleanupFailed || result.record.ActiveOperation != nil {
			t.Fatalf("cancelled runtime = %#v", result.record)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup was not cancelled")
	}

	store, err := loadVMRuntimeStore()
	if err != nil {
		t.Fatal(err)
	}
	if len(store.Runtimes) != 1 || store.Runtimes[0].ActiveOperation != nil || !strings.Contains(store.Runtimes[0].LastError, "cancelled by request") {
		t.Fatalf("runtime store = %#v", store)
	}
	if _, err := os.Stat(filepath.Join(stateDir, ".vm-cancel-"+cancelResult.OperationID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancellation marker remains: %v", err)
	}
}

func TestVMRuntimeCancelRejectsIdleRuntime(t *testing.T) {
	repo := vmTestRepo(t)
	t.Setenv("EVERYAPI_WORKSPACE_STATE_DIR", t.TempDir())
	runner := &fakeVMRecipeRunner{results: []vmProcessResult{{Stdout: validVMRecipeResult("/workspace/project", "token")}}}
	if _, err := vmRuntimeCreate(context.Background(), []string{"cloud-sandbox", "--repo-path", repo, "--instance-id", "vm-idle"}, runner); err != nil {
		t.Fatal(err)
	}
	if _, err := vmRuntimeCommand([]string{"cancel", "vm-idle"}); err == nil || !strings.Contains(err.Error(), "no active operation") {
		t.Fatalf("cancel idle runtime error = %v", err)
	}
}

func TestVMShellRunnerExecutesLifecycleContract(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX scripts")
	}
	repo := t.TempDir()
	t.Setenv("EVERYAPI_WORKSPACE_STATE_DIR", t.TempDir())
	writeVMTestFile(t, filepath.Join(repo, "everyapi.yaml"), `
environmentRecipes:
  - id: shell-test
    name: Shell contract test
    create: ./scripts/create.sh
    suspend: ./scripts/suspend.sh
    resume: ./scripts/resume.sh
    destroy: ./scripts/destroy.sh
`, 0o600)
	writeVMTestFile(t, filepath.Join(repo, "scripts", "create.sh"), `#!/bin/sh
printf '{"schemaVersion":1,"connection":{"type":"everyapi-server","pairingCode":"everyapi://pair?code=test","projectRoot":"/runtime/%s"}}\n' "$EVERYAPI_VM_INSTANCE_ID"
`, 0o700)
	writeVMTestFile(t, filepath.Join(repo, "scripts", "suspend.sh"), `#!/bin/sh
payload=$(cat)
case "$payload" in *'"mode":"suspend"'*) exit 0;; *) exit 9;; esac
`, 0o700)
	writeVMTestFile(t, filepath.Join(repo, "scripts", "resume.sh"), `#!/bin/sh
payload=$(cat)
case "$payload" in *'"mode":"resume"'*) ;; *) exit 9;; esac
printf '{"schemaVersion":1,"connection":{"type":"everyapi-server","pairingCode":"everyapi://pair?code=test","projectRoot":"/runtime/%s"}}\n' "$EVERYAPI_VM_INSTANCE_ID"
`, 0o700)
	writeVMTestFile(t, filepath.Join(repo, "scripts", "destroy.sh"), `#!/bin/sh
payload=$(cat)
case "$payload" in *'"mode":"destroy"'*) exit 0;; *) exit 9;; esac
`, 0o700)

	runner := shellVMRecipeRunner{}
	record, err := vmRuntimeCreate(context.Background(), []string{"shell-test", "--repo-path", repo, "--instance-id", "shell-runtime"}, runner)
	if err != nil || record.Status != vmRuntimeRunning {
		t.Fatalf("shell create = %#v, %v", record, err)
	}
	if record, err = vmRuntimeAction(context.Background(), vmActionSuspend, []string{record.ID}, runner); err != nil || record.Status != vmRuntimeSuspended {
		t.Fatalf("shell suspend = %#v, %v", record, err)
	}
	if record, err = vmRuntimeAction(context.Background(), vmActionResume, []string{record.ID}, runner); err != nil || record.Status != vmRuntimeRunning {
		t.Fatalf("shell resume = %#v, %v", record, err)
	}
	if record, err = vmRuntimeAction(context.Background(), vmActionCleanup, []string{record.ID}, runner); err != nil || record.Status != vmRuntimeCleaned {
		t.Fatalf("shell cleanup = %#v, %v", record, err)
	}
}

func TestVMRecipeResultRequiresAbsoluteProjectRoot(t *testing.T) {
	_, err := parseVMRecipeResult([]byte(validVMRecipeResult("relative/path", "token")), vmCheckoutEveryAPIWorktree)
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("parse result error = %v", err)
	}
}

func TestVMEnvironmentOverridesInheritedLifecycleValues(t *testing.T) {
	request := vmRunRequest{Mode: vmModeCreate, Context: vmRunContext{InstanceID: "fresh", RecipeID: "recipe"}, ResultSchemaVersion: 1}
	merged := mergeVMEnvironment([]string{"PATH=/bin", "EVERYAPI_VM_INSTANCE_ID=stale"}, vmRecipeEnvironment(request))
	values := map[string][]string{}
	for _, entry := range merged {
		key, value, _ := strings.Cut(entry, "=")
		values[key] = append(values[key], value)
	}
	if got := values["EVERYAPI_VM_INSTANCE_ID"]; len(got) != 1 || got[0] != "fresh" {
		t.Fatalf("merged VM instance env = %#v", got)
	}
}

func TestVMAgentContextExposesFullLifecycle(t *testing.T) {
	value, err := agentContext(nil)
	if err != nil {
		t.Fatal(err)
	}
	commands := value.(map[string]any)["commands"].([]map[string]any)
	want := map[string]bool{
		"vm recipe list":          false,
		"vm recipe doctor":        false,
		"vm runtime list":         false,
		"vm runtime show":         false,
		"vm runtime create":       false,
		"vm runtime suspend":      false,
		"vm runtime resume":       false,
		"vm runtime cleanup":      false,
		"vm runtime cancel":       false,
		"vm runtime cleanup-info": false,
		"vm runtime forget":       false,
	}
	for _, command := range commands {
		name, _ := command["command"].(string)
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	for command, found := range want {
		if !found {
			t.Errorf("agent context is missing %q", command)
		}
	}
}

type fakeVMRecipeRunner struct {
	results []vmProcessResult
	calls   []vmRunRequest
}

type blockingVMRecipeRunner struct {
	started chan struct{}
}

func (r *blockingVMRecipeRunner) Run(ctx context.Context, _ vmRunRequest) vmProcessResult {
	close(r.started)
	<-ctx.Done()
	return vmProcessResult{ExitCode: -1, Err: context.Cause(ctx)}
}

func (f *fakeVMRecipeRunner) Run(_ context.Context, request vmRunRequest) vmProcessResult {
	f.calls = append(f.calls, request)
	if len(f.results) == 0 {
		return vmProcessResult{Err: errors.New("unexpected recipe command")}
	}
	result := f.results[0]
	f.results = f.results[1:]
	return result
}

func vmTestRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	for _, name := range []string{"create.sh", "suspend.sh", "resume.sh", "destroy.sh"} {
		writeVMTestFile(t, filepath.Join(repo, "scripts", name), "#!/bin/sh\n", 0o700)
	}
	writeVMTestFile(t, filepath.Join(repo, "everyapi.yaml"), `
scripts:
  setup: ./scripts/setup.sh
environmentRecipes:
  - id: cloud-sandbox
    name: Cloud sandbox
    create: ./scripts/create.sh
    suspend: ./scripts/suspend.sh
    resume: ./scripts/resume.sh
    destroy: ./scripts/destroy.sh
`, 0o600)
	return repo
}

func validVMRecipeResult(projectRoot, secret string) string {
	value := map[string]any{
		"schemaVersion": 1,
		"connection": map[string]any{
			"type":        "everyapi-server",
			"pairingCode": "everyapi://pair?code=example",
			"projectRoot": projectRoot,
		},
		"userData": map[string]any{"apiToken": secret},
	}
	data, _ := json.Marshal(value)
	return string(data) + "\n"
}

func hasVMCheck(checks []vmCheck, id string, status vmCheckStatus) bool {
	for _, check := range checks {
		if check.ID == id && check.Status == status {
			return true
		}
	}
	return false
}

func writeVMTestFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}
