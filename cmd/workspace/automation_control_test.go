package workspace

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func startAutomationControlFixture(t *testing.T, schedules []any) (string, <-chan map[string]any) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	commands := make(chan map[string]any, 8)
	done := make(chan struct{})
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("automation control fixture did not stop")
		}
	})
	go func() {
		defer close(done)
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			func() {
				defer connection.Close()
				var prefix [4]byte
				if _, err := io.ReadFull(connection, prefix[:]); err != nil {
					return
				}
				payload := make([]byte, binary.BigEndian.Uint32(prefix[:]))
				if _, err := io.ReadFull(connection, payload); err != nil {
					return
				}
				var request struct {
					Version   int            `json:"v"`
					Token     string         `json:"token"`
					RequestID string         `json:"request_id"`
					Command   map[string]any `json:"command"`
				}
				if json.Unmarshal(payload, &request) != nil || request.Version != 1 || request.Token != "fixture-token" {
					return
				}
				commands <- request.Command
				var data any
				switch request.Command["type"] {
				case "schedule_list":
					data = map[string]any{"schedules": schedules, "runs": []any{}, "templates": []any{}}
				case "ping":
					data = map[string]any{"methods": []any{map[string]any{"id": "schedule_update", "min_version": 1, "max_version": 1}}}
				case "schedule_add", "schedule_update":
					schedule := map[string]any{}
					for key, value := range request.Command {
						schedule[key] = value
					}
					if schedule["schedule_id"] != nil {
						schedule["id"] = schedule["schedule_id"]
					} else {
						schedule["id"] = "created-job"
					}
					schedule["enabled"] = true
					data = map[string]any{"schedule": schedule}
				default:
					data = map[string]any{"accepted": true}
				}
				response, _ := json.Marshal(map[string]any{
					"v": request.Version, "request_id": request.RequestID, "status": "ok", "data": data,
				})
				binary.BigEndian.PutUint32(prefix[:], uint32(len(response)))
				_, _ = connection.Write(append(prefix[:], response...))
			}()
		}
	}()

	directory := t.TempDir()
	statePath := filepath.Join(directory, "control.json")
	state, _ := json.Marshal(map[string]any{
		"v": 1, "address": listener.Addr().String(), "token": "fixture-token", "pid": os.Getpid(),
	})
	if err := os.WriteFile(statePath, state, 0o600); err != nil {
		t.Fatal(err)
	}
	return statePath, commands
}

func nextAutomationControlCommand(t *testing.T, commands <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case command := <-commands:
		return command
	case <-time.After(2 * time.Second):
		t.Fatal("automation control command was not received")
		return nil
	}
}

func TestAutomationsListUsesRunningDesktopScheduler(t *testing.T) {
	statePath, commands := startAutomationControlFixture(t, []any{map[string]any{
		"id": "job-1", "name": "Repository health", "session": "session-1", "text": "review changes",
	}})

	value, err := automations([]string{"list", "--control-state", statePath})
	if err != nil {
		t.Fatal(err)
	}
	items, ok := value.([]any)
	if !ok || len(items) != 1 || items[0].(map[string]any)["id"] != "job-1" {
		t.Fatalf("automations list = %#v", value)
	}
	if command := nextAutomationControlCommand(t, commands); command["type"] != "schedule_list" {
		t.Fatalf("control command = %#v", command)
	}
}

func TestAutomationsSnapshotReturnsTheCompleteDesktopProjection(t *testing.T) {
	statePath, commands := startAutomationControlFixture(t, []any{map[string]any{
		"id": "job-1", "name": "Repository health", "session": "session-1", "text": "review changes",
	}})

	value, err := automations([]string{"snapshot", "--control-state", statePath})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("automations snapshot = %#v", value)
	}
	if schedules := snapshot["schedules"].([]any); len(schedules) != 1 || schedules[0].(map[string]any)["id"] != "job-1" {
		t.Fatalf("snapshot schedules = %#v", snapshot["schedules"])
	}
	for _, field := range []string{"runs", "templates"} {
		if _, ok := snapshot[field].([]any); !ok {
			t.Fatalf("snapshot %s = %#v", field, snapshot[field])
		}
	}
	if command := nextAutomationControlCommand(t, commands); command["type"] != "schedule_list" {
		t.Fatalf("control command = %#v", command)
	}
}

func TestAutomationSnapshotSchemaOnlyExposesControlState(t *testing.T) {
	flags := schemaAutomationFlags("automations snapshot")
	if len(flags) != 1 || flags[0] != "control-state" {
		t.Fatalf("automations snapshot flags = %#v", flags)
	}
}

func TestAutomationsDiscoverDefaultDesktopControlState(t *testing.T) {
	statePath, commands := startAutomationControlFixture(t, []any{map[string]any{"id": "job-1"}})
	state, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	controlDir := filepath.Join(home, ".everyapi")
	if err := os.MkdirAll(controlDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(controlDir, "control.json"), state, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("EVERYAPI_HOME", "")
	t.Setenv("EVERYAPI_CONTROL_STATE", "")
	t.Setenv("EVERYAPI_WORKSPACE_STATE_DIR", "")

	value, err := automations([]string{"list"})
	if err != nil {
		t.Fatal(err)
	}
	if items, ok := value.([]any); !ok || len(items) != 1 {
		t.Fatalf("automations list = %#v", value)
	}
	if command := nextAutomationControlCommand(t, commands); command["type"] != "schedule_list" {
		t.Fatalf("control command = %#v", command)
	}
}

func TestAutomationsCreateCarriesFullDesktopSchedulerContract(t *testing.T) {
	statePath, commands := startAutomationControlFixture(t, nil)
	value, err := automations([]string{
		"create", "--control-state", statePath,
		"--session", "session-1", "--name", "Weekday review", "--prompt", "review changes",
		"--schedule", "0 9 * * 1-5", "--missed-run-policy", "run_once", "--missed-run-grace-minutes", "90",
		"--workspace-mode", "new_run", "--fresh-session", "--base-branch", "main",
		"--precheck", "test -f ready.flag", "--precheck-timeout", "30", "--precheck-approved",
	})
	if err != nil {
		t.Fatal(err)
	}
	if value.(map[string]any)["id"] != "created-job" {
		t.Fatalf("created automation = %#v", value)
	}
	_ = nextAutomationControlCommand(t, commands) // list before mutation
	_ = nextAutomationControlCommand(t, commands) // capability probe
	command := nextAutomationControlCommand(t, commands)
	if command["type"] != "schedule_add" || command["session"] != "session-1" || command["name"] != "Weekday review" {
		t.Fatalf("create command = %#v", command)
	}
	if command["every_seconds"] != float64(0) || command["schedule_expression"] != "0 9 * * 1-5" || command["missed_run_policy"] != "run_once" {
		t.Fatalf("schedule fields = %#v", command)
	}
	execution := command["execution"].(map[string]any)
	if execution["workspace_mode"] != "new_run" || execution["session_mode"] != "fresh" || execution["base_ref"] != "main" {
		t.Fatalf("execution policy = %#v", execution)
	}
	precondition := command["precondition"].(map[string]any)
	if precondition["timeout_seconds"] != float64(30) || len(fmt.Sprint(precondition["trusted_sha256"])) != 64 {
		t.Fatalf("precondition = %#v", precondition)
	}
}

func TestAutomationsEditUsesInPlaceSchedulerUpdate(t *testing.T) {
	statePath, commands := startAutomationControlFixture(t, []any{map[string]any{
		"id": "job-1", "name": "Old", "session": "session-1", "text": "old prompt",
		"every_seconds": float64(3600), "missed_run_policy": "skip", "enabled": true,
		"execution": map[string]any{"workspace_mode": "worktree", "session_mode": "reuse"},
	}})

	value, err := automations([]string{
		"edit", "job-1", "--control-state", statePath, "--name", "New", "--prompt", "new prompt", "--every-seconds", "7200", "--enabled", "false",
	})
	if err != nil {
		t.Fatal(err)
	}
	if value.(map[string]any)["id"] != "job-1" {
		t.Fatalf("updated automation = %#v", value)
	}
	_ = nextAutomationControlCommand(t, commands)
	_ = nextAutomationControlCommand(t, commands)
	command := nextAutomationControlCommand(t, commands)
	if command["type"] != "schedule_update" || command["schedule_id"] != "job-1" || command["text"] != "new prompt" || command["every_seconds"] != float64(7200) {
		t.Fatalf("update command = %#v", command)
	}
	enabled := nextAutomationControlCommand(t, commands)
	if enabled["type"] != "schedule_set_enabled" || enabled["schedule_id"] != "job-1" || enabled["enabled"] != false {
		t.Fatalf("enabled command = %#v", enabled)
	}
}

func TestAutomationControlStateRejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not generally available to an unprivileged Windows test")
	}
	target := filepath.Join(t.TempDir(), "target.json")
	if err := os.WriteFile(target, []byte(`{"v":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "control.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := automations([]string{"list", "--control-state", link}); err == nil {
		t.Fatal("symlinked control state was accepted")
	}
}

func TestAutomationSchemaExposesDesktopControlFields(t *testing.T) {
	flags := schemaAutomationFlags("automations create")
	for _, expected := range []string{"control-state", "session", "every-seconds", "missed-run-policy", "workspace-mode", "precheck-approved"} {
		if !containsString(flags, expected) {
			t.Fatalf("automations create flags = %#v, missing %q", flags, expected)
		}
	}
}
