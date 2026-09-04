package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/credentiallock"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/tools"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

func TestDecodeBenchmarkUploadReturnsAfterOneNewlineFrameWithoutEOF(t *testing.T) {
	reader, writer := io.Pipe()
	t.Cleanup(func() {
		_ = reader.Close()
		_ = writer.Close()
	})
	done := make(chan error, 1)
	go func() {
		_, _ = writer.Write([]byte("{\"run_id\":\"frame\"}\n"))
	}()
	go func() {
		_, err := decodeBenchmarkUpload(reader)
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("decode waited for EOF after receiving a complete newline-delimited frame")
	}
}

func TestBenchmarkUploadReadsContentFreeStdinAndUsesCredentialClient(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := config.Save(&config.Credentials{APIBase: "https://example.test", AccessToken: "secret", UserID: 42}); err != nil {
		t.Fatal(err)
	}
	payload := `{"owner_user_id":42,"owner_api_base":"https://example.test","run_id":"11111111-1111-4111-8111-111111111111","repository_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","task_digest":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","grader":"go test ./...","results":[{"harness":"codex","model":"gpt-5.6","score":100,"cost_usd":0.25,"duration_ms":1000},{"harness":"claude","model":"claude-sonnet","score":0,"duration_ms":2000}]}`
	previousInput := benchmarkUploadInput
	benchmarkUploadInput = strings.NewReader(payload)
	t.Cleanup(func() { benchmarkUploadInput = previousInput })
	previousSubmit := submitBenchmarkUpload
	var got api.BenchmarkRunUpload
	submitBenchmarkUpload = func(_ context.Context, client *api.Client, upload api.BenchmarkRunUpload) (*api.BenchmarkImportReceipt, error) {
		got = upload
		return &api.BenchmarkImportReceipt{RunID: upload.RunID, ImportedResults: len(upload.Results)}, nil
	}
	t.Cleanup(func() { submitBenchmarkUpload = previousSubmit })
	var out bytes.Buffer
	previousOut := cliout.Out
	cliout.Out = &out
	t.Cleanup(func() { cliout.Out = previousOut })

	if err := BenchmarkUpload([]string{"--stdin", "--format=json"}); err != nil {
		t.Fatal(err)
	}
	if got.RunID == "" || got.RepositoryDigest != strings.Repeat("a", 64) || got.TaskDigest != strings.Repeat("b", 64) || len(got.Results) != 2 {
		t.Fatalf("upload = %#v", got)
	}
	if strings.TrimSpace(out.String()) != `{"ok":true,"run_id":"11111111-1111-4111-8111-111111111111","imported_results":2}` {
		t.Fatalf("output = %q", out.String())
	}
}

func TestBenchmarkUploadRejectsAReportFromAnotherCachedAccount(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := config.Save(&config.Credentials{APIBase: "https://example.test", AccessToken: "secret", UserID: 42}); err != nil {
		t.Fatal(err)
	}
	previousInput := benchmarkUploadInput
	benchmarkUploadInput = strings.NewReader(`{"owner_user_id":7,"owner_api_base":"https://example.test","run_id":"11111111-1111-4111-8111-111111111111"}` + "\n")
	t.Cleanup(func() { benchmarkUploadInput = previousInput })
	previousSubmit := submitBenchmarkUpload
	called := false
	submitBenchmarkUpload = func(context.Context, *api.Client, api.BenchmarkRunUpload) (*api.BenchmarkImportReceipt, error) {
		called = true
		return nil, nil
	}
	t.Cleanup(func() { submitBenchmarkUpload = previousSubmit })
	var out bytes.Buffer
	previousOut := cliout.Out
	cliout.Out = &out
	t.Cleanup(func() { cliout.Out = previousOut })

	if err := BenchmarkUpload([]string{"--stdin", "--format=json"}); err == nil {
		t.Fatal("expected owner mismatch")
	}
	if called || !strings.Contains(out.String(), `"code":"unavailable"`) {
		t.Fatalf("called=%v output=%q", called, out.String())
	}
}

func TestBenchmarkUploadRejectsUnknownOrOversizedInputBeforeNetwork(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := config.Save(&config.Credentials{AccessToken: "secret", UserID: 42}); err != nil {
		t.Fatal(err)
	}
	previousSubmit := submitBenchmarkUpload
	called := false
	submitBenchmarkUpload = func(context.Context, *api.Client, api.BenchmarkRunUpload) (*api.BenchmarkImportReceipt, error) {
		called = true
		return nil, nil
	}
	t.Cleanup(func() { submitBenchmarkUpload = previousSubmit })

	for name, payload := range map[string]string{
		"unknown field": `{"run_id":"x","task":"private"}`,
		"oversized":     strings.Repeat("x", maxBenchmarkUploadInputBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			previousInput := benchmarkUploadInput
			benchmarkUploadInput = strings.NewReader(payload)
			t.Cleanup(func() { benchmarkUploadInput = previousInput })
			var out bytes.Buffer
			previousOut := cliout.Out
			cliout.Out = &out
			t.Cleanup(func() { cliout.Out = previousOut })
			if err := BenchmarkUpload([]string{"--stdin", "--format=json"}); err == nil {
				t.Fatal("expected invalid input")
			}
			if !strings.Contains(out.String(), `"code":"invalid_benchmark"`) {
				t.Fatalf("output = %q", out.String())
			}
		})
	}
	if called {
		t.Fatal("invalid input reached the network")
	}
}

func TestBenchmarkUploadReturnsOnlyStableFailureCode(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := config.Save(&config.Credentials{AccessToken: "secret", UserID: 42}); err != nil {
		t.Fatal(err)
	}
	previousInput := benchmarkUploadInput
	benchmarkUploadInput = strings.NewReader(`{"owner_user_id":42,"owner_api_base":"https://example.test","run_id":"11111111-1111-4111-8111-111111111111"}`)
	t.Cleanup(func() { benchmarkUploadInput = previousInput })
	previousSubmit := submitBenchmarkUpload
	submitBenchmarkUpload = func(context.Context, *api.Client, api.BenchmarkRunUpload) (*api.BenchmarkImportReceipt, error) {
		return nil, errors.New("PRIVATE upstream detail")
	}
	t.Cleanup(func() { submitBenchmarkUpload = previousSubmit })
	var out bytes.Buffer
	previousOut := cliout.Out
	cliout.Out = &out
	t.Cleanup(func() { cliout.Out = previousOut })

	err := BenchmarkUpload([]string{"--stdin", "--format=json"})
	if err == nil {
		t.Fatal("expected failure")
	}
	if strings.Contains(out.String(), "PRIVATE") || !strings.Contains(out.String(), `"code":"unavailable"`) {
		t.Fatalf("output = %q", out.String())
	}
}

func TestBenchmarkAgentUseArgs(t *testing.T) {
	taskFile := filepath.Join(t.TempDir(), "task.txt")
	if err := os.WriteFile(taskFile, []byte("Fix the failing parser test.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		tool string
		want []string
	}{
		{
			name: "claude isolated headless JSONL",
			tool: "claude",
			want: []string{"claude", "--model", "model-a", "--", "--bare", "--no-session-persistence", "--add-dir", ".", "-p", "Fix the failing parser test.", "--output-format", "stream-json", "--verbose", "--dangerously-skip-permissions"},
		},
		{
			name: "codex exec JSONL",
			tool: "codex",
			want: []string{"codex", "--model", "model-a", "--", "exec", "--json", "--dangerously-bypass-approvals-and-sandbox", "Fix the failing parser test."},
		},
		{name: "opencode run", tool: "opencode", want: []string{"opencode", "--model", "model-a", "--", "run", "--format", "json", "--auto", "Fix the failing parser test."}},
		{name: "aider message", tool: "aider", want: []string{
			"aider", "--model", "model-a", "--", "--message", "Fix the failing parser test.",
			"--yes-always", "--no-auto-commits",
			"--input-history-file", taskFile + ".aider.input.history",
			"--chat-history-file", taskFile + ".aider.chat.history.md",
			"--llm-history-file", taskFile + ".aider.llm.history",
		}},
		{name: "goose run", tool: "goose", want: []string{"goose", "--model", "model-a", "--", "run", "--text", "Fix the failing parser test.", "--no-session", "--stats", "--output-format", "json"}},
		{name: "crush run", tool: "crush", want: []string{"crush", "--model", "model-a", "--", "run", "--quiet", "Fix the failing parser test."}},
		{name: "cline prompt", tool: "cline", want: []string{"cline", "--model", "model-a", "--", "Fix the failing parser test.", "--json", "--auto-approve", "true"}},
		{name: "openclaw local agent", tool: "openclaw", want: []string{"openclaw", "--model", "model-a", "--", "agent", "--local", "--message", "Fix the failing parser test.", "--json"}},
		{name: "continue print", tool: "continue", want: []string{"continue", "--model", "model-a", "--", "--print", "Fix the failing parser test.", "--auto", "--format", "json"}},
		{name: "kilo run", tool: "kilo", want: []string{"kilo", "--model", "model-a", "--", "run", "--format", "json", "--auto", "Fix the failing parser test."}},
		{name: "pi print", tool: "pi", want: []string{"pi", "--model", "model-a", "--", "--print", "--mode", "json", "--approve", "Fix the failing parser test."}},
		{name: "vibe prompt", tool: "vibe", want: []string{"vibe", "--model", "model-a", "--", "--prompt", "Fix the failing parser test.", "--output", "streaming", "--auto-approve", "--trust"}},
		{name: "copilot prompt", tool: "copilot", want: []string{"copilot", "--model", "model-a", "--", "--prompt", "Fix the failing parser test.", "--output-format", "json", "--allow-all"}},
		{name: "droid exec", tool: "droid", want: []string{"droid", "--model", "model-a", "--", "exec", "--output-format", "stream-json", "--skip-permissions-unsafe", "Fix the failing parser test."}},
		{name: "openhands headless", tool: "openhands", want: []string{"openhands", "--model", "model-a", "--", "--override-with-envs", "--headless", "--json", "--always-approve", "--task", "Fix the failing parser test."}},
		{name: "forge prompt", tool: "forge", want: []string{"forge", "--model", "model-a", "--", "--prompt", "Fix the failing parser test."}},
		{name: "grok single", tool: "grok", want: []string{"grok", "--model", "model-a", "--", "--single", "Fix the failing parser test.", "--output-format", "streaming-messages-json", "--always-approve"}},
		{name: "qwen prompt", tool: "qwen_code", want: []string{"qwen-code", "--model", "model-a", "--", "--prompt", "Fix the failing parser test.", "--output-format", "stream-json", "--yolo"}},
		{name: "kimi prompt", tool: "kimi_code", want: []string{"kimi-code", "--model", "model-a", "--", "--prompt", "Fix the failing parser test.", "--output-format", "stream-json", "--auto"}},
		{name: "hermes oneshot", tool: "hermes", want: []string{"hermes", "--model", "model-a", "--", "--oneshot", "Fix the failing parser test.", "--yolo", "--accept-hooks"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := benchmarkAgentUseArgs([]string{"--tool", test.tool, "--model", "model-a", "--task-file", taskFile})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("args = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestBenchmarkClaudeIsolationRequiresSupportedFlags(t *testing.T) {
	tests := []struct {
		name      string
		help      string
		missing   string
		wantError bool
	}{
		{name: "supported", help: "  --bare  Minimal mode\n  --no-session-persistence  Disable persistence\n"},
		{name: "bare missing", help: "  --no-session-persistence  Disable persistence\n", missing: "--bare", wantError: true},
		{name: "persistence missing", help: "  --bare  Minimal mode\n", missing: "--no-session-persistence", wantError: true},
		{name: "prefix is not the option", help: "  --barely  Not bare\n  --no-session-persistence-extra  Not persistence\n", missing: "--bare", wantError: true},
		{name: "description mention is not the option", help: "This old client does not support --bare or --no-session-persistence.\n", missing: "--bare", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := benchmarkClaudeIsolationHelpError([]byte(test.help))
			if test.wantError {
				if err == nil || !strings.Contains(err.Error(), test.missing) || !strings.Contains(err.Error(), "claude update") {
					t.Fatalf("error = %v, want unsupported flag and upgrade command", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v, want nil", err)
			}
		})
	}
}

func TestBenchmarkAgentUseArgsRejectsUnreviewedHarnessAndInvalidTask(t *testing.T) {
	taskFile := filepath.Join(t.TempDir(), "task.txt")
	if err := os.WriteFile(taskFile, []byte("task"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := benchmarkAgentUseArgs([]string{"--tool", "open-webui", "--model", "model-a", "--task-file", taskFile}); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("unsupported harness error = %v", err)
	}
	if _, err := benchmarkAgentUseArgs([]string{"--tool", "claude", "--model", "model-a", "--task-file", filepath.Join(t.TempDir(), "missing")}); err == nil || !strings.Contains(err.Error(), "read benchmark task") {
		t.Fatalf("missing task error = %v", err)
	}
}

func TestBenchmarkCatalogIncludesReviewedHarnessesAndCompatibleModels(t *testing.T) {
	catalog := []api.RelayModel{
		{ID: "anthropic-model", SupportedEndpointTypes: []string{"anthropic"}},
		{ID: "chat-model", SupportedEndpointTypes: []string{"openai"}},
		{ID: "responses-model", SupportedEndpointTypes: []string{"openai-response"}},
		{ID: "image-model", SupportedEndpointTypes: []string{"image-generation"}},
	}

	got := benchmarkCatalog(catalog)
	models := make(map[string][]string, len(got.Harnesses))
	for _, harness := range got.Harnesses {
		models[harness.Harness] = harness.Models
	}

	if !reflect.DeepEqual(models["claude"], []string{"anthropic-model"}) {
		t.Fatalf("claude models = %v", models["claude"])
	}
	if !reflect.DeepEqual(models["codex"], []string{"responses-model"}) {
		t.Fatalf("codex models = %v", models["codex"])
	}
	if !reflect.DeepEqual(models["opencode"], []string{"chat-model", "responses-model"}) {
		t.Fatalf("opencode models = %v", models["opencode"])
	}
	if _, exists := models["open-webui"]; exists {
		t.Fatal("server-only Open WebUI was exposed as a benchmark harness")
	}
	if _, exists := models["qwen_code"]; !exists {
		t.Fatal("desktop Qwen harness identifier was not exposed")
	}
}

func TestBenchmarkCatalogDeterministicallyBoundsLargeCompatibleCatalogs(t *testing.T) {
	catalog := make([]api.RelayModel, 300)
	for index := range catalog {
		catalog[index] = api.RelayModel{
			ID:                     fmt.Sprintf("model-%03d", index),
			SupportedEndpointTypes: []string{"openai"},
		}
	}

	got := benchmarkCatalog(catalog)
	for _, harness := range got.Harnesses {
		if harness.Harness != "aider" {
			continue
		}
		if len(harness.Models) != maxBenchmarkModelsPerHarness {
			t.Fatalf("aider model count = %d, want %d", len(harness.Models), maxBenchmarkModelsPerHarness)
		}
		if !harness.Truncated {
			t.Fatal("large aider catalog did not signal truncation")
		}
		if harness.Models[0] != "model-000" || harness.Models[len(harness.Models)-1] != "model-063" {
			t.Fatalf("bounded models are not deterministic: %v", harness.Models)
		}
		return
	}
	t.Fatal("aider benchmark catalog entry is missing")
}

func TestBenchmarkModelSelectionDoesNotBecomeTheRememberedDefault(t *testing.T) {
	settings := &config.Settings{}
	claude, err := tools.Lookup("claude")
	if err != nil {
		t.Fatal(err)
	}
	catalog := []api.RelayModel{{ID: "benchmark-model", SupportedEndpointTypes: []string{"anthropic"}}}
	selected, err := resolveRememberedModelWithPersistence(
		claude, settings, catalog, "benchmark-model", false, false, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if selected != "benchmark-model" {
		t.Fatalf("selected = %q", selected)
	}
	if remembered := settings.ToolModel("claude"); remembered != "" {
		t.Fatalf("benchmark persisted model %q", remembered)
	}
}

// EveryAPI Connect spawns desktop-benchmark-catalog beside `everyapi auth credential`, which holds the cross-process credential lock while it rotates an OAuth2 relay key. A catalogue run that reads credentials.json and resolves outside that lock replays an already-rotated refresh token, and the gateway's reuse detector then revokes the whole refresh family plus its paired relay keys.
func TestBenchmarkCatalogResolvesTheRelayKeyUnderTheCredentialLock(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	requests := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"chat-model","supported_endpoint_types":["openai"]}]}`)
	}))
	t.Cleanup(server.Close)
	stale := &config.Credentials{APIBase: server.URL, AccessToken: "access", RelayKey: "stale-key", RelayKeySystemChecked: true, UserID: 42}
	if err := config.Save(stale); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	previousOut := cliout.Out
	cliout.Out = &out
	t.Cleanup(func() { cliout.Out = previousOut })

	unlock, err := credentiallock.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	// Stands in for the concurrent locked refresher: the rotated key lands on disk after the sidecar was spawned, so only a run that loads under the lock can observe it.
	rotated := *stale
	rotated.RelayKey = "rotated-key"
	if err := config.Save(&rotated); err != nil {
		unlock()
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- BenchmarkCatalog(nil) }()

	raced := ""
	select {
	case auth := <-requests:
		raced = fmt.Sprintf("catalogue reached the gateway with %q while the credential lock was held", auth)
	case err := <-done:
		raced = fmt.Sprintf("catalogue finished (err=%v) while the credential lock was held", err)
	case <-time.After(500 * time.Millisecond):
	}
	unlock()

	select {
	case err := <-done:
		if raced == "" && err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("catalogue never finished after the credential lock was released")
	}
	if raced != "" {
		t.Fatal(raced)
	}
	select {
	case auth := <-requests:
		if auth != "Bearer rotated-key" {
			t.Fatalf("catalogue authorization = %q, want the relay key written under the lock", auth)
		}
	default:
		t.Fatal("catalogue made no gateway request")
	}
	if !strings.Contains(out.String(), `"chat-model"`) {
		t.Fatalf("catalogue output = %q", out.String())
	}
}
