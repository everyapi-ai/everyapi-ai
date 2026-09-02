package workspace

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	automationControlProtocolVersion = 1
	automationControlMaxFrameBytes   = 1024 * 1024
	automationControlTimeout         = 10 * time.Second
)

type automationControlState struct {
	Version int    `json:"v"`
	Address string `json:"address"`
	Token   string `json:"token"`
}

type automationControlResponse struct {
	Version   int             `json:"v"`
	RequestID string          `json:"request_id"`
	Status    string          `json:"status"`
	Data      json.RawMessage `json:"data"`
	Code      string          `json:"code"`
	Message   string          `json:"message"`
}

// automationControlStatePath selects the authenticated desktop control runtime when it is
// available. An explicit path or EVERYAPI_CONTROL_STATE is authoritative: failure must surface
// instead of silently creating a second, disconnected local automation store.
func automationControlStatePath(args []string) (string, bool, error) {
	path := flagValue(args, "control-state", "")
	if path == "" {
		path = strings.TrimSpace(os.Getenv("EVERYAPI_CONTROL_STATE"))
	}
	if path != "" {
		return filepath.Clean(path), true, nil
	}
	if os.Getenv("EVERYAPI_WORKSPACE_STATE_DIR") != "" {
		return "", false, nil
	}
	if home := strings.TrimSpace(os.Getenv("EVERYAPI_HOME")); home != "" {
		path = filepath.Join(home, "control.json")
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false, err
		}
		path = filepath.Join(home, ".everyapi", "control.json")
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("desktop control state is not a regular file")
	}
	return path, true, nil
}

func readAutomationControlState(path string) (automationControlState, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return automationControlState{}, fmt.Errorf("read desktop control state: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return automationControlState{}, errors.New("desktop control state is not a regular file")
	}
	if info.Size() > automationControlMaxFrameBytes {
		return automationControlState{}, errors.New("desktop control state is too large")
	}
	file, err := os.Open(path)
	if err != nil {
		return automationControlState{}, fmt.Errorf("open desktop control state: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, automationControlMaxFrameBytes+1))
	if err != nil {
		return automationControlState{}, fmt.Errorf("read desktop control state: %w", err)
	}
	if len(data) > automationControlMaxFrameBytes {
		return automationControlState{}, errors.New("desktop control state is too large")
	}
	var state automationControlState
	if err := json.Unmarshal(data, &state); err != nil {
		return automationControlState{}, fmt.Errorf("decode desktop control state: %w", err)
	}
	if state.Version != automationControlProtocolVersion {
		return automationControlState{}, fmt.Errorf("unsupported desktop control protocol %d", state.Version)
	}
	host, _, err := net.SplitHostPort(state.Address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return automationControlState{}, errors.New("desktop control address must be a loopback socket")
	}
	if state.Token == "" {
		return automationControlState{}, errors.New("desktop control state has no authentication token")
	}
	return state, nil
}

func automationControlRequestID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

func callAutomationControl(path string, command map[string]any) (any, error) {
	state, err := readAutomationControlState(path)
	if err != nil {
		return nil, err
	}
	requestID, err := automationControlRequestID()
	if err != nil {
		return nil, fmt.Errorf("create desktop control request: %w", err)
	}
	request, err := json.Marshal(map[string]any{
		"v":          automationControlProtocolVersion,
		"token":      state.Token,
		"request_id": requestID,
		"command":    command,
	})
	if err != nil {
		return nil, err
	}
	if len(request) > automationControlMaxFrameBytes {
		return nil, errors.New("desktop control request is too large")
	}
	connection, err := net.DialTimeout("tcp", state.Address, automationControlTimeout)
	if err != nil {
		return nil, fmt.Errorf("connect to EveryAPI desktop automation service: %w", err)
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(automationControlTimeout)); err != nil {
		return nil, err
	}
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(request)))
	frame := append(prefix[:], request...)
	for len(frame) > 0 {
		written, writeErr := connection.Write(frame)
		if writeErr != nil {
			return nil, fmt.Errorf("write desktop control request: %w", writeErr)
		}
		if written == 0 {
			return nil, io.ErrUnexpectedEOF
		}
		frame = frame[written:]
	}
	if _, err := io.ReadFull(connection, prefix[:]); err != nil {
		return nil, fmt.Errorf("read desktop control response: %w", err)
	}
	length := binary.BigEndian.Uint32(prefix[:])
	if length > automationControlMaxFrameBytes {
		return nil, errors.New("desktop control response is too large")
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(connection, payload); err != nil {
		return nil, fmt.Errorf("read desktop control response: %w", err)
	}
	var response automationControlResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return nil, fmt.Errorf("decode desktop control response: %w", err)
	}
	if response.Version != automationControlProtocolVersion || response.RequestID != requestID {
		return nil, errors.New("desktop control response did not match the request")
	}
	if response.Status != "ok" {
		if response.Message == "" {
			response.Message = "desktop automation command failed"
		}
		if response.Code == "" {
			return nil, errors.New(response.Message)
		}
		return nil, fmt.Errorf("%s: %s", response.Code, response.Message)
	}
	if len(response.Data) == 0 {
		return map[string]any{}, nil
	}
	var data any
	if err := json.Unmarshal(response.Data, &data); err != nil {
		return nil, fmt.Errorf("decode desktop automation result: %w", err)
	}
	return data, nil
}

func automationControlList(path string) (map[string]any, error) {
	value, err := callAutomationControl(path, map[string]any{"type": "schedule_list"})
	if err != nil {
		return nil, err
	}
	data, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("desktop automation service returned an invalid list")
	}
	return data, nil
}

func automationControlRequireEditableSchedules(path string) error {
	value, err := callAutomationControl(path, map[string]any{"type": "ping"})
	if err != nil {
		return err
	}
	data, ok := value.(map[string]any)
	if !ok {
		return errors.New("desktop automation service returned invalid capabilities")
	}
	methods, _ := data["methods"].([]any)
	for _, value := range methods {
		method, _ := value.(map[string]any)
		if method["id"] == "schedule_update" {
			return nil
		}
	}
	return errors.New("the running EveryAPI desktop host does not support complete automation edits; update the desktop app")
}

func automationControlItems(data map[string]any, field string) []any {
	items, _ := data[field].([]any)
	if items == nil {
		return []any{}
	}
	return items
}

func automationControlFind(items []any, id string) (map[string]any, error) {
	if id == "" {
		return nil, errors.New("automation id is required")
	}
	var match map[string]any
	for _, value := range items {
		item, ok := value.(map[string]any)
		if !ok {
			continue
		}
		candidate := fmt.Sprint(item["id"])
		if candidate == id {
			return item, nil
		}
		if strings.HasPrefix(candidate, id) {
			if match != nil {
				return nil, fmt.Errorf("automation id prefix %q is ambiguous", id)
			}
			match = item
		}
	}
	if match != nil {
		return match, nil
	}
	return nil, fmt.Errorf("automation %q not found", id)
}

func automationUintFlag(args []string, name string, fallback uint64) (uint64, error) {
	raw := flagValue(args, name, "")
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("--%s must be a non-negative integer", name)
	}
	return value, nil
}

func automationEnabledFlag(args []string) (bool, bool, error) {
	if hasFlag(args, "--disabled") {
		return false, true, nil
	}
	for index, arg := range args {
		if strings.HasPrefix(arg, "--enabled=") {
			value, err := strconv.ParseBool(strings.TrimPrefix(arg, "--enabled="))
			if err != nil {
				return false, false, errors.New("--enabled must be true or false")
			}
			return value, true, nil
		}
		if arg == "--enabled" {
			if index+1 < len(args) {
				if value, err := strconv.ParseBool(args[index+1]); err == nil {
					return value, true, nil
				}
			}
			return true, true, nil
		}
	}
	return false, false, nil
}

func automationExecution(args []string, current map[string]any) map[string]any {
	workspaceMode := flagValue(args, "workspace-mode", fmt.Sprint(current["workspace_mode"]))
	if workspaceMode == "" || workspaceMode == "<nil>" {
		workspaceMode = "worktree"
	}
	sessionMode := fmt.Sprint(current["session_mode"])
	if sessionMode == "" || sessionMode == "<nil>" {
		sessionMode = "reuse"
	}
	if hasFlag(args, "--fresh-session") {
		sessionMode = "fresh"
	}
	if hasFlag(args, "--reuse-session") {
		sessionMode = "reuse"
	}
	execution := map[string]any{"workspace_mode": workspaceMode, "session_mode": sessionMode}
	baseRef := flagValue(args, "base-branch", fmt.Sprint(current["base_ref"]))
	if baseRef != "" && baseRef != "-" && baseRef != "<nil>" {
		execution["base_ref"] = baseRef
	}
	return execution
}

func automationSchedule(args []string, current map[string]any) (uint64, any, error) {
	everySeconds, err := automationUintFlag(args, "every-seconds", uint64Field(current, "every_seconds"))
	if err != nil {
		return 0, nil, err
	}
	schedule := flagValue(args, "schedule", "")
	if schedule != "" && schedule != "manual" && schedule != "-" {
		return 0, schedule, nil
	}
	if flagValue(args, "every-seconds", "") != "" || schedule == "-" {
		return everySeconds, nil, nil
	}
	if expression, ok := current["schedule_expression"].(string); ok && expression != "" {
		return 0, expression, nil
	}
	if everySeconds == 0 {
		return 0, nil, errors.New("--schedule or --every-seconds is required")
	}
	return everySeconds, nil, nil
}

func uint64Field(item map[string]any, name string) uint64 {
	value, _ := item[name].(float64)
	if value <= 0 {
		return 0
	}
	return uint64(value)
}

func optionalUint32Flag(args []string, name string, current any) (any, error) {
	raw := flagValue(args, name, "")
	if raw == "" {
		return current, nil
	}
	if raw == "-" {
		return nil, nil
	}
	value, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("--%s must be '-' or a non-negative integer", name)
	}
	return value, nil
}

func automationMutationCommand(args []string, current map[string]any, update bool) (map[string]any, error) {
	session := flagValue(args, "session", fmt.Sprint(current["session"]))
	if session == "" || session == "<nil>" {
		return nil, errors.New("--session is required for a desktop automation")
	}
	prompt := flagValueAny(args, "prompt", "command")
	if prompt == "" {
		prompt = fmt.Sprint(current["text"])
	}
	if prompt == "" || prompt == "<nil>" {
		return nil, errors.New("--prompt is required for a desktop automation")
	}
	everySeconds, scheduleExpression, err := automationSchedule(args, current)
	if err != nil {
		return nil, err
	}
	policy := flagValue(args, "missed-run-policy", fmt.Sprint(current["missed_run_policy"]))
	if policy == "" || policy == "<nil>" {
		policy = "skip"
	}
	if policy != "skip" && policy != "run_once" {
		return nil, errors.New("--missed-run-policy must be skip or run_once")
	}
	grace, err := optionalUint32Flag(args, "missed-run-grace-minutes", current["missed_run_grace_minutes"])
	if err != nil {
		return nil, err
	}
	name := flagValue(args, "name", fmt.Sprint(current["name"]))
	if hasFlag(args, "--clear-name") || name == "<nil>" {
		name = ""
	}
	command := map[string]any{
		"type":                     "schedule_add",
		"session":                  session,
		"text":                     prompt,
		"every_seconds":            everySeconds,
		"schedule_expression":      scheduleExpression,
		"missed_run_policy":        policy,
		"missed_run_grace_minutes": grace,
		"execution":                automationExecution(args, mapField(current, "execution")),
	}
	if name != "" {
		command["name"] = name
	}
	if update {
		command["type"] = "schedule_update"
		command["schedule_id"] = fmt.Sprint(current["id"])
	}
	if !update {
		script := flagValue(args, "precheck", "")
		if script != "" {
			remote := flagValue(args, "precheck-approval", "") == "remote"
			if !remote && !hasFlag(args, "--precheck-approved") {
				return nil, errors.New("--precheck-approved is required before storing a local precheck script")
			}
			timeout, err := automationUintFlag(args, "precheck-timeout", 30)
			if err != nil || timeout < 1 || timeout > 600 {
				return nil, errors.New("--precheck-timeout must be between 1 and 600 seconds")
			}
			hash := sha256.Sum256([]byte(strings.TrimSpace(script)))
			command["precondition"] = map[string]any{
				"script":          script,
				"timeout_seconds": timeout,
				"trusted_sha256":  hex.EncodeToString(hash[:]),
				"remote_approval": remote,
			}
		}
	}
	return command, nil
}

func mapField(item map[string]any, name string) map[string]any {
	value, _ := item[name].(map[string]any)
	if value == nil {
		return map[string]any{}
	}
	return value
}

func automationControlScheduleResult(value any) (map[string]any, error) {
	data, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("desktop automation service returned an invalid mutation result")
	}
	schedule, ok := data["schedule"].(map[string]any)
	if !ok || fmt.Sprint(schedule["id"]) == "" || fmt.Sprint(schedule["id"]) == "<nil>" {
		return nil, errors.New("desktop automation service returned no schedule")
	}
	return schedule, nil
}

func automationsThroughControl(args []string, path string) (any, error) {
	if len(args) == 0 || isHelp(args[0]) {
		return map[string]any{"commands": []string{"snapshot", "list", "show", "create", "edit", "remove", "run", "runs"}, "backend": "desktop-control"}, nil
	}
	subcommand := args[0]
	data, err := automationControlList(path)
	if err != nil {
		return nil, err
	}
	schedules := automationControlItems(data, "schedules")
	switch subcommand {
	case "snapshot":
		return data, nil
	case "list":
		return schedules, nil
	case "runs":
		runs := automationControlItems(data, "runs")
		if id := flagValue(args[1:], "id", ""); id != "" {
			filtered := make([]any, 0, len(runs))
			for _, value := range runs {
				item, _ := value.(map[string]any)
				if fmt.Sprint(item["schedule_id"]) == id || fmt.Sprint(item["slot"]) == id {
					filtered = append(filtered, item)
				}
			}
			return filtered, nil
		}
		return runs, nil
	case "show":
		return automationControlFind(schedules, flagValue(args[1:], "id", firstPath(args[1:])))
	case "create":
		if err := automationControlRequireEditableSchedules(path); err != nil {
			return nil, err
		}
		command, err := automationMutationCommand(args[1:], map[string]any{}, false)
		if err != nil {
			return nil, err
		}
		created, err := callAutomationControl(path, command)
		if err != nil {
			return nil, err
		}
		schedule, err := automationControlScheduleResult(created)
		if err != nil {
			return nil, err
		}
		if enabled, specified, parseErr := automationEnabledFlag(args[1:]); parseErr != nil {
			return nil, parseErr
		} else if specified && !enabled {
			if _, err := callAutomationControl(path, map[string]any{"type": "schedule_set_enabled", "schedule_id": fmt.Sprint(schedule["id"]), "enabled": false}); err != nil {
				return nil, err
			}
			schedule["enabled"] = false
		}
		return schedule, nil
	case "edit", "update":
		if err := automationControlRequireEditableSchedules(path); err != nil {
			return nil, err
		}
		current, err := automationControlFind(schedules, flagValue(args[1:], "id", firstPath(args[1:])))
		if err != nil {
			return nil, err
		}
		command, err := automationMutationCommand(args[1:], current, true)
		if err != nil {
			return nil, err
		}
		updated, err := callAutomationControl(path, command)
		if err != nil {
			return nil, err
		}
		schedule, err := automationControlScheduleResult(updated)
		if err != nil {
			return nil, err
		}
		if enabled, specified, parseErr := automationEnabledFlag(args[1:]); parseErr != nil {
			return nil, parseErr
		} else if specified && enabled != current["enabled"] {
			if _, err := callAutomationControl(path, map[string]any{"type": "schedule_set_enabled", "schedule_id": fmt.Sprint(schedule["id"]), "enabled": enabled}); err != nil {
				return nil, err
			}
			schedule["enabled"] = enabled
		}
		return schedule, nil
	case "remove", "delete":
		current, err := automationControlFind(schedules, flagValue(args[1:], "id", firstPath(args[1:])))
		if err != nil {
			return nil, err
		}
		return callAutomationControl(path, map[string]any{"type": "schedule_remove", "schedule_id": fmt.Sprint(current["id"])})
	case "run":
		current, err := automationControlFind(schedules, flagValue(args[1:], "id", firstPath(args[1:])))
		if err != nil {
			return nil, err
		}
		value, err := callAutomationControl(path, map[string]any{"type": "schedule_run_now", "schedule_id": fmt.Sprint(current["id"])})
		if err != nil {
			return nil, err
		}
		result, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("desktop automation service returned an invalid run result")
		}
		accepted, _ := result["accepted"].(bool)
		duplicate, _ := result["duplicate"].(bool)
		skipped, _ := result["skipped"].(bool)
		outcome, _ := result["outcome"].(string)
		if outcome == "" && accepted {
			outcome = "accepted"
		}
		result["accepted"] = accepted
		result["duplicate"] = duplicate
		result["skipped"] = skipped
		result["outcome"] = outcome
		return result, nil
	default:
		return nil, fmt.Errorf("unknown automations subcommand %q", subcommand)
	}
}
