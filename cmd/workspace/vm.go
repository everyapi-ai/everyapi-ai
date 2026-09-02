package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type vmOverviewResult struct {
	Runtime  string            `json:"runtime"`
	Arch     string            `json:"arch"`
	Recipes  []vmRecipe        `json:"recipes"`
	Runtimes []vmRuntimeRecord `json:"runtimes"`
}

func vmCommand(args []string) (any, error) {
	if len(args) == 0 || args[0] == "list" {
		repoPath := flagValue(args, "repo-path", ".")
		_, recipes, recipeErr := loadVMRecipes(repoPath)
		if recipeErr != nil && !strings.Contains(recipeErr.Error(), "no "+vmConfigFile+" found") {
			return nil, recipeErr
		}
		if recipes == nil {
			recipes = []vmRecipe{}
		}
		store, err := loadVMRuntimeStore()
		if err != nil {
			return nil, err
		}
		return vmOverviewResult{Runtime: runtime.GOOS, Arch: runtime.GOARCH, Recipes: recipes, Runtimes: store.Runtimes}, nil
	}
	switch args[0] {
	case "recipe", "recipes":
		return vmRecipeCommand(args[1:])
	case "runtime", "runtimes":
		return vmRuntimeCommand(args[1:])
	default:
		return nil, fmt.Errorf("unknown vm subcommand %q", args[0])
	}
}

func vmRecipeCommand(args []string) (any, error) {
	operation := "list"
	if len(args) > 0 {
		operation = args[0]
	}
	switch operation {
	case "list":
		repoPath := flagValue(args[1:], "repo-path", ".")
		canonical, err := canonicalVMRepoPath(repoPath)
		if err != nil {
			return nil, err
		}
		configPath, recipes, err := loadVMRecipes(canonical)
		if err != nil {
			return nil, err
		}
		for index := range recipes {
			checks := doctorVMRecipe(canonical, recipes[index])
			recipes[index].Available = vmChecksOK(checks)
		}
		return vmRecipeListResult{Runtime: runtime.GOOS, Arch: runtime.GOARCH, RepoPath: canonical, ConfigPath: configPath, Recipes: recipes}, nil
	case "doctor":
		positions := positional(args[1:])
		recipeID := flagValue(args[1:], "recipe-id", "")
		if recipeID == "" && len(positions) > 0 {
			recipeID = positions[0]
		}
		if recipeID == "" {
			return nil, errors.New("VM recipe id is required")
		}
		repoPath := flagValue(args[1:], "repo-path", ".")
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		doctor, err := vmDoctorWithRunner(ctx, repoPath, recipeID, hasFlag(args[1:], "--provision") || hasFlag(args[1:], "--connect"), shellVMRecipeRunner{})
		if err != nil {
			return doctor, err
		}
		if !doctor.OK {
			return doctor, fmt.Errorf("VM recipe %q failed doctor checks", recipeID)
		}
		return doctor, nil
	default:
		return nil, fmt.Errorf("unknown vm recipe subcommand %q", operation)
	}
}

func vmDoctorWithRunner(ctx context.Context, repoPath, recipeID string, provision bool, runner vmRecipeRunner) (vmDoctorResult, error) {
	result := vmDoctorResult{RecipeID: recipeID, RepoPath: repoPath, Checks: []vmCheck{}}
	canonical, err := canonicalVMRepoPath(repoPath)
	if err != nil {
		result.Checks = append(result.Checks, vmCheck{ID: "repo.path", Status: vmCheckFail, Message: err.Error(), Remediation: "Pass a local repository directory containing everyapi.yaml."})
		return finalizeVMDoctor(result), nil
	}
	result.RepoPath = canonical
	configPath, recipes, err := loadVMRecipes(canonical)
	result.ConfigPath = configPath
	if err != nil {
		result.Checks = append(result.Checks, vmCheck{ID: "everyapi_yaml.parse", Status: vmCheckFail, Message: err.Error(), Remediation: "Add a valid environmentRecipes entry to everyapi.yaml."})
		return finalizeVMDoctor(result), nil
	}
	result.Checks = append(result.Checks, vmCheck{ID: "everyapi_yaml.parse", Status: vmCheckPass, Message: "everyapi.yaml parsed successfully."})
	recipe, err := findVMRecipe(recipes, recipeID)
	if err != nil {
		result.Checks = append(result.Checks, vmCheck{ID: "recipe.exists", Status: vmCheckFail, Message: err.Error(), Remediation: "Check the recipe id or add it to environmentRecipes."})
		return finalizeVMDoctor(result), nil
	}
	result.Checks = append(result.Checks, vmCheck{ID: "recipe.exists", Status: vmCheckPass, Message: fmt.Sprintf("Found recipe %q.", recipe.Name)})
	result.Checks = append(result.Checks, doctorVMRecipe(canonical, recipe)...)
	result = finalizeVMDoctor(result)
	if !provision {
		return result, nil
	}
	if !result.OK {
		result.Checks = append(result.Checks, vmCheck{ID: "recipe.provision.skipped", Status: vmCheckFail, Message: "Provisioning was skipped because non-destructive doctor checks failed.", Remediation: "Fix the failing checks before running --provision again."})
		return finalizeVMDoctor(result), nil
	}
	instanceID, err := newVMInstanceID()
	if err != nil {
		result.Checks = append(result.Checks, vmCheck{ID: "recipe.provision", Status: vmCheckFail, Message: "Could not create an isolated doctor instance id.", Remediation: "Retry the command."})
		return finalizeVMDoctor(result), nil
	}
	runContext := vmRunContext{InstanceID: instanceID, RecipeID: recipe.ID, RepoPath: canonical}
	start := runner.Run(ctx, vmRunRequest{Command: recipe.Create, RepoPath: canonical, Mode: vmModeCreate, Context: runContext, ResultSchemaVersion: vmRecipeResultSchemaVersion(recipe)})
	transcript := &vmProvisionTranscript{Provision: vmTranscriptFromProcess(start, nil)}
	result.ProvisionTranscript = transcript
	if start.Err != nil || start.ExitCode != 0 {
		message := vmProcessFailure("provision", start)
		result.Checks = append(result.Checks, vmCheck{ID: "recipe.provision", Status: vmCheckFail, Message: message, Remediation: "Inspect the provision transcript, fix the create action, and retry."})
		return finalizeVMDoctor(result), nil
	}
	parsed, parseErr := parseVMRecipeResult([]byte(start.Stdout), recipe.CheckoutMode)
	if parseErr != nil {
		transcript.Provision = vmTranscriptFromProcess(start, nil)
		transcript.Provision.Error = parseErr.Error()
		result.Checks = append(result.Checks, vmCheck{ID: "recipe.provision", Status: vmCheckFail, Message: parseErr.Error(), Remediation: "Print exactly one valid VM recipe result JSON object to stdout."})
		return finalizeVMDoctor(result), nil
	}
	transcript.Provision = vmTranscriptFromProcess(start, parsed.Secrets)
	result.Checks = append(result.Checks,
		vmCheck{ID: "recipe.provision", Status: vmCheckPass, Message: "Recipe ran successfully and produced a valid VM recipe result."},
		vmCheck{ID: "recipe.result.project_root", Status: vmCheckPass, Message: "Recipe returned projectRoot: " + parsed.ProjectRoot},
	)
	cleanupContext, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cleanupCancel()
	cleanup := runVMRecipeLifecycle(cleanupContext, runner, recipe, canonical, runContext, vmModeDestroy, parsed.Raw)
	transcript.Destroy = vmTranscriptFromProcess(cleanup, parsed.Secrets)
	if recipe.DestroyDisabled || recipe.Destroy == "" {
		result.Checks = append(result.Checks, vmCheck{ID: "recipe.destroy.run", Status: vmCheckWarn, Message: "Destroy was skipped because destroy is disabled or missing.", Remediation: "Destroy any resources created by this doctor run manually."})
	} else if cleanup.Err != nil || cleanup.ExitCode != 0 {
		result.Checks = append(result.Checks, vmCheck{ID: "recipe.destroy.run", Status: vmCheckFail, Message: vmProcessFailure("destroy", cleanup), Remediation: "Destroy provider resources manually, then fix the destroy action."})
	} else {
		result.Checks = append(result.Checks, vmCheck{ID: "recipe.destroy.run", Status: vmCheckPass, Message: "Destroy action ran successfully after provisioning."})
	}
	return finalizeVMDoctor(result), nil
}

func doctorVMRecipe(repoPath string, recipe vmRecipe) []vmCheck {
	checks := []vmCheck{checkVMRecipeCommand(repoPath, recipe.Create, "recipe.create")}
	if recipe.DestroyDisabled {
		checks = append(checks, vmCheck{ID: "recipe.destroy", Status: vmCheckWarn, Message: "Destroy is explicitly disabled.", Remediation: "Only use destroy: none when provider resources are cleaned up elsewhere."})
	} else if recipe.Destroy == "" {
		checks = append(checks, vmCheck{ID: "recipe.destroy", Status: vmCheckWarn, Message: "No destroy action is configured.", Remediation: "Add destroy or explicitly set destroy: none."})
	} else {
		checks = append(checks, checkVMRecipeCommand(repoPath, recipe.Destroy, "recipe.destroy"))
	}
	if recipe.Suspend != "" {
		checks = append(checks, checkVMRecipeCommand(repoPath, recipe.Suspend, "recipe.suspend"))
	}
	if recipe.Resume != "" {
		checks = append(checks, checkVMRecipeCommand(repoPath, recipe.Resume, "recipe.resume"))
	}
	if (recipe.Suspend == "") != (recipe.Resume == "") {
		checks = append(checks, vmCheck{ID: "recipe.suspend_resume_pairing", Status: vmCheckWarn, Message: "Recipe defines only one of suspend/resume.", Remediation: "Define both lifecycle actions or neither."})
	}
	return checks
}

func checkVMRecipeCommand(repoPath, command, id string) vmCheck {
	executable := firstVMCommandToken(command)
	if executable == "" {
		return vmCheck{ID: id, Status: vmCheckFail, Message: "Command is empty.", Remediation: "Set a repository-relative command path."}
	}
	if filepath.IsAbs(executable) {
		if _, err := os.Stat(executable); err != nil {
			return vmCheck{ID: id, Status: vmCheckFail, Message: "Command path does not exist: " + executable, Remediation: "Create the executable or update the recipe command."}
		}
		return vmCheck{ID: id, Status: vmCheckWarn, Message: "Command uses an absolute path: " + executable, Remediation: "Prefer a repository-relative script so the recipe works across machines."}
	}
	if !strings.HasPrefix(executable, "./") && !strings.HasPrefix(executable, `.\`) {
		resolved, err := exec.LookPath(executable)
		if err != nil {
			return vmCheck{ID: id, Status: vmCheckFail, Message: "Command is not installed: " + executable, Remediation: "Install the provider CLI or use a repository-relative script."}
		}
		return vmCheck{ID: id, Status: vmCheckPass, Message: "Command is available: " + resolved}
	}
	path := filepath.Join(repoPath, filepath.Clean(executable))
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return vmCheck{ID: id, Status: vmCheckFail, Message: "Command path does not exist: " + executable, Remediation: "Create the script or update the recipe command path."}
	}
	if !samePath(resolved, repoPath) && !isWithin(resolved, repoPath) {
		return vmCheck{ID: id, Status: vmCheckFail, Message: "Command path escapes the repository: " + executable, Remediation: "Keep lifecycle scripts inside the repository."}
	}
	info, err := os.Stat(resolved)
	if err != nil || info.IsDir() {
		return vmCheck{ID: id, Status: vmCheckFail, Message: "Command path is not a file: " + executable, Remediation: "Create the script or update the recipe command path."}
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return vmCheck{ID: id, Status: vmCheckWarn, Message: "Command exists but is not executable: " + executable, Remediation: "Make it executable with chmod +x."}
	}
	return vmCheck{ID: id, Status: vmCheckPass, Message: "Command path exists: " + executable}
}

func firstVMCommandToken(command string) string {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return ""
	}
	if trimmed[0] == '\'' || trimmed[0] == '"' {
		quote := trimmed[0]
		if end := strings.IndexByte(trimmed[1:], quote); end >= 0 {
			return trimmed[1 : end+1]
		}
	}
	if index := strings.IndexAny(trimmed, " \t\r\n"); index >= 0 {
		return trimmed[:index]
	}
	return trimmed
}

func vmChecksOK(checks []vmCheck) bool {
	for _, check := range checks {
		if check.Status == vmCheckFail {
			return false
		}
	}
	return true
}

func finalizeVMDoctor(result vmDoctorResult) vmDoctorResult {
	result.OK = vmChecksOK(result.Checks)
	return result
}

func vmTranscriptFromProcess(result vmProcessResult, secrets []string) vmTranscriptStage {
	stage := vmTranscriptStage{ExitCode: result.ExitCode, Stdout: redactVMDiagnostic(result.Stdout, secrets), Stderr: redactVMDiagnostic(result.Stderr, secrets)}
	if result.Err != nil {
		stage.Error = redactVMDiagnostic(result.Err.Error(), secrets)
	}
	return stage
}

func vmProcessFailure(operation string, result vmProcessResult) string {
	if result.Err != nil {
		return fmt.Sprintf("%s could not start: %v", operation, result.Err)
	}
	return fmt.Sprintf("%s exited with code %d", operation, result.ExitCode)
}
