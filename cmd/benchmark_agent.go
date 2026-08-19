package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/tools"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

// Connect caps the task at 64K characters, matching the renderer's own limit on
// the same field. UTF-8 spends up to four bytes on one character, so the byte
// budget here has to cover the widest encoding of a task Connect accepted —
// anything tighter would reject text the form never warned the user about.
const maxBenchmarkTaskBytes = 4 * 64 * 1024
const maxBenchmarkModelsPerHarness = 64
const maxBenchmarkModelRunes = 256

var benchmarkHarnessNames = []string{
	"claude", "codex", "opencode", "aider", "goose", "crush", "cline", "openclaw",
	"continue", "kilo", "pi", "vibe", "copilot", "droid", "openhands", "forge",
	"grok", "qwen_code", "kimi_code", "hermes",
}

func benchmarkToolName(harness string) string {
	switch harness {
	case "qwen_code":
		return "qwen-code"
	case "kimi_code":
		return "kimi-code"
	default:
		return harness
	}
}

type benchmarkCatalogOutput struct {
	Version   int                       `json:"version"`
	Harnesses []benchmarkCatalogHarness `json:"harnesses"`
}

type benchmarkCatalogHarness struct {
	Harness   string   `json:"harness"`
	Models    []string `json:"models"`
	Truncated bool     `json:"truncated"`
}

func benchmarkCatalog(catalog []api.RelayModel) benchmarkCatalogOutput {
	harnesses := make([]benchmarkCatalogHarness, 0, len(benchmarkHarnessNames))
	for _, name := range benchmarkHarnessNames {
		tool, err := tools.Lookup(benchmarkToolName(name))
		if err != nil {
			continue
		}
		launchModels := launchModelsForTool(tool, catalog, "")
		models := make([]string, 0, min(len(launchModels), maxBenchmarkModelsPerHarness))
		truncated := false
		for _, model := range launchModels {
			if utf8.RuneCountInString(model.ID) > maxBenchmarkModelRunes ||
				strings.IndexFunc(model.ID, unicode.IsControl) >= 0 {
				continue
			}
			if len(models) == maxBenchmarkModelsPerHarness {
				truncated = true
				break
			}
			models = append(models, model.ID)
		}
		harnesses = append(harnesses, benchmarkCatalogHarness{
			Harness: name, Models: models, Truncated: truncated,
		})
	}
	return benchmarkCatalogOutput{Version: 1, Harnesses: harnesses}
}

// BenchmarkCatalog returns only reviewed harness identifiers and the live,
// relay-key-scoped model IDs each one can drive. Credentials stay inside the
// sidecar and never cross into Connect's renderer.
func BenchmarkCatalog(args []string) error {
	if len(args) != 0 {
		return errors.New("desktop-benchmark-catalog accepts no arguments")
	}
	creds, err := config.Load()
	if err != nil {
		return err
	}
	key, err := api.ResolveRelayKey(context.Background(), creds, "")
	if err != nil {
		return err
	}
	apiBase := config.ResolveAPIBaseForBase(creds.APIBase)
	catalog, err := api.New(apiBase, key).RelayModelCatalog(context.Background())
	if err != nil {
		return err
	}
	return json.NewEncoder(cliout.Out).Encode(benchmarkCatalog(catalog))
}

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

	useArgs := []string{benchmarkToolName(tool), "--model", modelID, "--"}
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
	case "opencode":
		return append(useArgs, "run", "--format", "json", "--auto", task), nil
	case "aider":
		return append(useArgs,
			"--message", task,
			"--yes-always", "--no-auto-commits",
			"--input-history-file", path+".aider.input.history",
			"--chat-history-file", path+".aider.chat.history.md",
			"--llm-history-file", path+".aider.llm.history",
		), nil
	case "goose":
		return append(useArgs, "run", "--text", task, "--no-session", "--stats", "--output-format", "json"), nil
	case "crush":
		return append(useArgs, "run", "--quiet", task), nil
	case "cline":
		return append(useArgs, task, "--json", "--auto-approve", "true"), nil
	case "openclaw":
		return append(useArgs, "agent", "--local", "--message", task, "--json"), nil
	case "continue":
		return append(useArgs, "--print", task, "--auto", "--format", "json"), nil
	case "kilo":
		return append(useArgs, "run", "--format", "json", "--auto", task), nil
	case "pi":
		return append(useArgs, "--print", "--mode", "json", "--approve", task), nil
	case "vibe":
		return append(useArgs, "--prompt", task, "--output", "streaming", "--auto-approve", "--trust"), nil
	case "copilot":
		return append(useArgs, "--prompt", task, "--output-format", "json", "--allow-all"), nil
	case "droid":
		return append(useArgs, "exec", "--output-format", "stream-json", "--skip-permissions-unsafe", task), nil
	case "openhands":
		return append(useArgs, "--override-with-envs", "--headless", "--json", "--always-approve", "--task", task), nil
	case "forge":
		return append(useArgs, "--prompt", task), nil
	case "grok":
		return append(useArgs, "--single", task, "--output-format", "streaming-messages-json", "--always-approve"), nil
	case "qwen_code":
		return append(useArgs, "--prompt", task, "--output-format", "stream-json", "--yolo"), nil
	case "kimi_code":
		return append(useArgs, "--prompt", task, "--output-format", "stream-json", "--auto"), nil
	case "hermes":
		return append(useArgs, "--oneshot", task, "--yolo", "--accept-hooks"), nil
	default:
		return nil, fmt.Errorf("benchmark harness %q is not supported", tool)
	}
}
