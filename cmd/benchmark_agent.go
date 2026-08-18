package cmd

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
)

// Connect caps the task at 64K characters, matching the renderer's own limit on
// the same field. UTF-8 spends up to four bytes on one character, so the byte
// budget here has to cover the widest encoding of a task Connect accepted —
// anything tighter would reject text the form never warned the user about.
const maxBenchmarkTaskBytes = 4 * 64 * 1024

// BenchmarkAgent is a private EveryAPI Connect surface. It translates one
// reviewed harness name into that harness's non-interactive invocation, then
// reuses Use for authentication, model selection, catalogue validation, and
// process-scoped provider setup. Keeping this adapter in the CLI means Connect
// never grows a second copy of the provider/harness configuration contract.
func BenchmarkAgent(args []string) error {
	useArgs, err := benchmarkAgentUseArgs(args)
	if err != nil {
		return err
	}
	return use(useArgs, false)
}

func benchmarkAgentUseArgs(args []string) ([]string, error) {
	flags := flag.NewFlagSet("desktop-benchmark-agent", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	toolName := flags.String("tool", "", "reviewed harness name")
	model := flags.String("model", "", "EveryAPI model id")
	taskFile := flags.String("task-file", "", "UTF-8 task file")
	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	if flags.NArg() != 0 {
		return nil, errors.New("desktop-benchmark-agent accepts flags only")
	}
	tool := strings.TrimSpace(*toolName)
	modelID := strings.TrimSpace(*model)
	path := strings.TrimSpace(*taskFile)
	if tool == "" || modelID == "" || path == "" {
		return nil, errors.New("desktop-benchmark-agent requires --tool, --model, and --task-file")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("read benchmark task: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxBenchmarkTaskBytes {
		return nil, fmt.Errorf("benchmark task must be a non-empty regular file no larger than %d bytes", maxBenchmarkTaskBytes)
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read benchmark task: %w", err)
	}
	task := strings.TrimSpace(string(encoded))
	if task == "" {
		return nil, errors.New("benchmark task is empty")
	}

	useArgs := []string{tool, "--model", modelID, "--"}
	switch tool {
	case "claude":
		return append(useArgs,
			"-p", task,
			"--output-format", "json",
			"--dangerously-skip-permissions",
		), nil
	case "codex":
		return append(useArgs,
			"exec", "--json",
			"--dangerously-bypass-approvals-and-sandbox",
			task,
		), nil
	default:
		return nil, fmt.Errorf("benchmark harness %q is not supported", tool)
	}
}
