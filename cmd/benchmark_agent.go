package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
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
const maxBenchmarkUploadInputBytes = 16 * 1024

var benchmarkUploadInput io.Reader = os.Stdin

var submitBenchmarkUpload = func(ctx context.Context, client *api.Client, upload api.BenchmarkRunUpload) (*api.BenchmarkImportReceipt, error) {
	return client.ImportBenchmarkRun(ctx, upload)
}

type benchmarkUploadInputFrame struct {
	OwnerUserID  int    `json:"owner_user_id"`
	OwnerAPIBase string `json:"owner_api_base"`
	api.BenchmarkRunUpload
}

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
	// Resolve through the locked wrapper, never api.ResolveRelayKey directly: resolution can rotate an OAuth2 relay key and rewrite credentials.json, and Connect spawns this sidecar alongside `auth credential`, which holds the cross-process credential lock while it refreshes. Loading before that lock and refreshing outside it would replay an already-rotated refresh token, and the gateway's reuse detector revokes the whole refresh family plus its paired relay keys. The lock is released before the catalogue request below, which needs no credential state.
	key, err := resolveRelayKey(creds, "")
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

// BenchmarkUpload is a private EveryAPI Connect surface. The content-free
// report arrives over stdin so no repository/task metadata appears in the
// process list; the SDK signs it with the credential that remains inside this
// process and submits it to the authenticated import endpoint.
func BenchmarkUpload(args []string) error {
	flags := flag.NewFlagSet("desktop-benchmark-upload", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	fromStdin := flags.Bool("stdin", false, "read the content-free benchmark report from stdin")
	format := flags.String("format", "", "machine output format")
	if err := flags.Parse(args); err != nil {
		return reportBenchmarkUpload("invalid_benchmark", err)
	}
	if flags.NArg() != 0 || !*fromStdin || *format != "json" {
		return reportBenchmarkUpload("invalid_benchmark", errors.New("desktop-benchmark-upload requires --stdin --format=json"))
	}
	input, err := decodeBenchmarkUpload(benchmarkUploadInput)
	if err != nil {
		return reportBenchmarkUpload("invalid_benchmark", err)
	}
	creds, err := config.Load()
	if err != nil {
		code := "unavailable"
		if errors.Is(err, config.ErrNoCredentials) {
			code = "not_signed_in"
		}
		return reportBenchmarkUpload(code, err)
	}
	if input.OwnerUserID <= 0 || creds.UserID != input.OwnerUserID ||
		config.ResolveAPIBaseForBase(creds.APIBase) != input.OwnerAPIBase {
		return reportBenchmarkUpload("unavailable", errors.New("benchmark owner changed"))
	}
	receipt, err := submitBenchmarkUpload(context.Background(), api.ForCredentials(creds), input.BenchmarkRunUpload)
	if err != nil {
		return reportBenchmarkUpload(benchmarkUploadErrorCode(err), err)
	}
	return json.NewEncoder(cliout.Out).Encode(struct {
		OK              bool   `json:"ok"`
		RunID           string `json:"run_id"`
		ImportedResults int    `json:"imported_results"`
	}{OK: true, RunID: receipt.RunID, ImportedResults: receipt.ImportedResults})
}

func decodeBenchmarkUpload(reader io.Reader) (benchmarkUploadInputFrame, error) {
	encoded, err := bufio.NewReader(io.LimitReader(reader, maxBenchmarkUploadInputBytes+2)).ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return benchmarkUploadInputFrame{}, errors.New("invalid benchmark upload")
	}
	if len(encoded) == 0 || len(encoded) > maxBenchmarkUploadInputBytes+1 {
		return benchmarkUploadInputFrame{}, errors.New("invalid benchmark upload")
	}
	if encoded[len(encoded)-1] == '\n' {
		encoded = encoded[:len(encoded)-1]
	}
	if len(encoded) == 0 || len(encoded) > maxBenchmarkUploadInputBytes {
		return benchmarkUploadInputFrame{}, errors.New("invalid benchmark upload")
	}
	var upload benchmarkUploadInputFrame
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&upload); err != nil {
		return benchmarkUploadInputFrame{}, errors.New("invalid benchmark upload")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return benchmarkUploadInputFrame{}, errors.New("invalid benchmark upload")
	}
	return upload, nil
}

func benchmarkUploadErrorCode(err error) string {
	if api.IsUnauthorized(err) {
		return "not_signed_in"
	}
	var apiError *api.APIError
	if errors.As(err, &apiError) {
		switch apiError.StatusCode {
		case http.StatusBadRequest:
			return "invalid_benchmark"
		case http.StatusUnauthorized:
			return "not_signed_in"
		case http.StatusConflict:
			return "benchmark_conflict"
		}
	}
	var importError *api.BenchmarkImportError
	if errors.As(err, &importError) {
		switch importError.Code {
		case "invalid_benchmark", "invalid_signature", "benchmark_conflict", "not_signed_in":
			return importError.Code
		}
	}
	return "unavailable"
}

func reportBenchmarkUpload(code string, err error) error {
	_ = json.NewEncoder(cliout.Out).Encode(struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
	}{Code: code})
	return err
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
	if len(useArgs) > 0 && useArgs[0] == "claude" {
		claude, lookupErr := tools.Lookup("claude")
		if lookupErr != nil {
			return lookupErr
		}
		if isolationErr := benchmarkClaudeIsolationPreflight(claude); isolationErr != nil {
			return isolationErr
		}
	}
	return use(useArgs, false)
}

func benchmarkClaudeIsolationPreflight(claude *tools.Tool) error {
	path, err := tools.ResolveExec(claude)
	if err != nil {
		// The normal use preflight owns the richer not-installed error and install
		// hint. Capability checking must not replace it with a version error.
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, path, "--help")
	command.Args = []string{claude.ExecName, "--help"}
	help, err := command.Output()
	if err != nil {
		return fmt.Errorf("could not verify Claude Code benchmark isolation support; run `claude update` and try again: %w", err)
	}
	return benchmarkClaudeIsolationHelpError(help)
}

func benchmarkClaudeIsolationHelpError(help []byte) error {
	for _, required := range []string{"--bare", "--no-session-persistence"} {
		if !benchmarkClaudeHelpHasOption(help, required) {
			return fmt.Errorf("installed Claude Code does not support %s, which is required for isolated benchmarks; run `claude update` and try again", required)
		}
	}
	return nil
}

func benchmarkClaudeHelpHasOption(help []byte, option string) bool {
	for _, line := range strings.Split(string(help), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, option) {
			continue
		}
		if len(line) == len(option) {
			return true
		}
		switch line[len(option)] {
		case ' ', '\t', ',':
			return true
		}
	}
	return false
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
		// A benchmark must measure the selected harness/model pair, not the
		// operator's personal Claude setup. --bare skips hooks, LSP/plugin sync,
		// auto-memory, background prefetches, keychain reads, and CLAUDE.md
		// discovery; without it those inputs can add tens of thousands of tokens
		// to every turn and can execute unrelated commands in the worktree.
		// Persistence is disabled independently so a benchmark never becomes a
		// resumable personal session even if Claude changes --bare's defaults.
		// --add-dir explicitly restores repository-local CLAUDE.md discovery,
		// keeping project instructions available just as they are to harnesses
		// that discover their own repository instruction files.
		return append(useArgs,
			"--bare", "--no-session-persistence",
			"--add-dir", ".",
			"-p", task,
			"--output-format", "stream-json", "--verbose",
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
