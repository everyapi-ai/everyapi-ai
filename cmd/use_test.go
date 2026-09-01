package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/tools"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

func TestClaudeInflatedResume(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "projects", "-tmp-project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"assistant","message":{"usage":{"input_tokens":6611,"cache_creation_input_tokens":249657,"cache_read_input_tokens":0}}}` + "\n"
	if err := os.WriteFile(filepath.Join(project, "91f317c8-1b35-48fc-85a4-553eac2b5085.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}

	tokens, inflated := claudeInflatedResume([]string{"--resume", "91f317c8-1b35-48fc-85a4-553eac2b5085"}, root)
	if !inflated || tokens != 249657 {
		t.Fatalf("claudeInflatedResume = (%d, %v), want (249657, true)", tokens, inflated)
	}
}

func TestClaudeInflatedResumeIgnoresFreshAndNonResume(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "projects", "-tmp-project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"assistant","message":{"usage":{"cache_creation_input_tokens":22659}}}` + "\n"
	if err := os.WriteFile(filepath.Join(project, "fresh.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{nil, {"--resume=fresh"}, {"-r", "fresh"}} {
		if tokens, inflated := claudeInflatedResume(args, root); inflated || tokens != 22659 && len(args) > 0 {
			t.Fatalf("claudeInflatedResume(%v) = (%d, %v), want non-inflated", args, tokens, inflated)
		}
	}
}

func TestResolveLaunchPreferenceUsesStoredChoiceWithoutPrompting(t *testing.T) {
	for _, stored := range []bool{false, true} {
		stored := stored
		asked, saved := false, false
		got, err := resolveLaunchPreference(&stored, true, func() (bool, error) {
			asked = true
			return !stored, nil
		}, func(bool) error {
			saved = true
			return nil
		})
		if err != nil || got != stored || asked || saved {
			t.Fatalf("resolve stored %v = (%v, %v), asked=%v saved=%v", stored, got, err, asked, saved)
		}
	}
}

func TestResolveLaunchPreferencePromptsAndPersistsFirstInteractiveChoice(t *testing.T) {
	var persisted *bool
	got, err := resolveLaunchPreference(nil, true, func() (bool, error) {
		return true, nil
	}, func(value bool) error {
		persisted = &value
		return nil
	})
	if err != nil || !got || persisted == nil || !*persisted {
		t.Fatalf("resolve first choice = (%v, %v), persisted=%v", got, err, persisted)
	}
}

func TestResolveLaunchPreferenceDefaultsOffWithoutTTY(t *testing.T) {
	asked, saved := false, false
	got, err := resolveLaunchPreference(nil, false, func() (bool, error) {
		asked = true
		return true, nil
	}, func(bool) error {
		saved = true
		return nil
	})
	if err != nil || got || asked || saved {
		t.Fatalf("non-interactive resolve = (%v, %v), asked=%v saved=%v", got, err, asked, saved)
	}
}

func TestParseUseArgs(t *testing.T) {
	cases := []struct {
		name          string
		args          []string
		wantTool      string
		wantGroup     string
		wantPick      bool
		wantSanitize  bool
		wantExtra     []string
		wantModel     string
		wantPickModel bool
		wantErr       bool
	}{
		{"bare tool", []string{"claude"}, "claude", "", false, false, nil, "", false, false},
		{"no args", nil, "", "", false, false, nil, "", false, false},
		{"tool then bare channel → picker", []string{"claude", "--channel"}, "claude", "", true, false, nil, "", false, false},
		{"tool then bare group → picker", []string{"claude", "--group"}, "claude", "", true, false, nil, "", false, false},
		{"bare channel only → picker, no tool", []string{"--channel"}, "", "", true, false, nil, "", false, false},
		{"space value after tool", []string{"claude", "--channel", "byteplus"}, "claude", "byteplus", false, false, nil, "", false, false},
		{"space value before tool", []string{"--channel", "team-a", "claude"}, "claude", "team-a", false, false, nil, "", false, false},
		{"group alias space value", []string{"claude", "--group", "byteplus"}, "claude", "byteplus", false, false, nil, "", false, false},
		{"group and channel aliases conflict", []string{"claude", "--group", "a", "--channel", "b"}, "", "", false, false, nil, "", false, true},
		{"eq value", []string{"claude", "--channel=byteplus"}, "claude", "byteplus", false, false, nil, "", false, false},
		{"empty eq → picker", []string{"claude", "--channel="}, "claude", "", true, false, nil, "", false, false},
		{"single dash space value", []string{"-channel", "team-a", "codex"}, "codex", "team-a", false, false, nil, "", false, false},
		{"value position is known tool, no tool yet → it's the tool, picker", []string{"--channel", "claude"}, "claude", "", true, false, nil, "", false, false},
		{"next token is a flag → picker", []string{"claude", "--channel", "--sanitize"}, "claude", "", true, true, nil, "", false, false},
		{"--direct is no longer a flag → error", []string{"claude", "--direct"}, "", "", false, false, nil, "", false, true},
		{"--sanitize opts in", []string{"claude", "--sanitize"}, "claude", "", false, true, nil, "", false, false},
		{"--sanitize + group", []string{"claude", "--sanitize", "--channel=byteplus"}, "claude", "byteplus", false, true, nil, "", false, false},
		{"--sanitize=true enables", []string{"claude", "--sanitize=true"}, "claude", "", false, true, nil, "", false, false},
		{"--sanitize=false disables (not the inverse)", []string{"claude", "--sanitize=false"}, "claude", "", false, false, nil, "", false, false},
		{"--sanitize=0 disables", []string{"claude", "--sanitize=0"}, "claude", "", false, false, nil, "", false, false},
		{"--sanitize=garbage → error", []string{"claude", "--sanitize=nope"}, "", "", false, false, nil, "", false, true},
		{"two positionals → error", []string{"claude", "extra"}, "", "", false, false, nil, "", false, true},
		{"unknown flag → error", []string{"claude", "--bogus"}, "", "", false, false, nil, "", false, true},
		{"ambiguous: group named like a tool before tool → error", []string{"--group", "codex", "claude"}, "", "", false, false, nil, "", false, true},
		{"provider name remains valid as a routing group", []string{"--group", "byteplus", "claude"}, "claude", "byteplus", false, false, nil, "", false, false},
		{"eq form lets a tool-named group through", []string{"--channel=codex", "claude"}, "claude", "codex", false, false, nil, "", false, false},
		{"eq form lets a preset-named group through", []string{"--channel=byteplus", "claude"}, "claude", "byteplus", false, false, nil, "", false, false},

		// --model: pin the upstream model (hermes). Space + eq forms; a valueless --model is an error (omit it to get the picker).
		{"model space value", []string{"hermes", "--model", "gpt-5.1"}, "hermes", "", false, false, nil, "gpt-5.1", false, false},
		{"model eq value", []string{"hermes", "--model=gpt-5.1"}, "hermes", "", false, false, nil, "gpt-5.1", false, false},
		{"model before tool", []string{"--model", "claude-sonnet-4-6", "hermes"}, "hermes", "", false, false, nil, "claude-sonnet-4-6", false, false},
		{"model + channel", []string{"hermes", "--channel=byteplus", "--model", "gpt-5.1"}, "hermes", "byteplus", false, false, nil, "gpt-5.1", false, false},
		{"bare model opens the picker", []string{"hermes", "--model"}, "hermes", "", false, false, nil, "", true, false},
		{"empty eq model → error", []string{"hermes", "--model="}, "", "", false, false, nil, "", false, true},
		{"bare model still lets the next flag parse", []string{"hermes", "--model", "--sanitize"}, "hermes", "", false, true, nil, "", true, false},
		{"model space value won't eat a not-yet-seen tool name", []string{"--model", "claude"}, "claude", "", false, false, nil, "", true, false},
		{"model value after tool may be a tool-named id", []string{"hermes", "--model", "claude"}, "hermes", "", false, false, nil, "claude", false, false},

		// `--` separator: end of everyapi's option parsing, everything after is forwarded raw to the tool. The documented escape hatch for tool flags that collide with everyapi's, e.g. claude's `--dangerously-skip-permissions` and codex's `--dangerously-bypass-approvals-and-sandbox`.
		{"-- forwards a single flag", []string{"claude", "--", "--dangerously-skip-permissions"}, "claude", "", false, false, []string{"--dangerously-skip-permissions"}, "", false, false},
		{"-- forwards multiple tokens verbatim", []string{"claude", "--", "--model", "opus", "prompt text"}, "claude", "", false, false, []string{"--model", "opus", "prompt text"}, "", false, false},
		{"-- combined with --channel before tool", []string{"--channel", "team-a", "claude", "--", "--dangerously-skip-permissions"}, "claude", "team-a", false, false, []string{"--dangerously-skip-permissions"}, "", false, false},
		{"-- combined with --channel after tool", []string{"claude", "--channel=byteplus", "--", "--dangerously-skip-permissions"}, "claude", "byteplus", false, false, []string{"--dangerously-skip-permissions"}, "", false, false},
		{"bare -- with no following args", []string{"claude", "--"}, "claude", "", false, false, nil, "", false, false},
		{"-- shields what would otherwise be a everyapi flag", []string{"claude", "--", "--group", "byteplus"}, "claude", "", false, false, []string{"--group", "byteplus"}, "", false, false},
		{"-- shields what would otherwise be a second positional", []string{"claude", "--", "extra"}, "claude", "", false, false, []string{"extra"}, "", false, false},
		{"-- shields --model too", []string{"hermes", "--", "--model", "x"}, "hermes", "", false, false, []string{"--model", "x"}, "", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tool, group, pick, sanitize, extra, model, pickModel, err := parseUseArgs(c.args)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, c.wantErr)
			}
			if c.wantErr {
				return
			}
			if tool != c.wantTool || group != c.wantGroup || pick != c.wantPick || sanitize != c.wantSanitize {
				t.Fatalf("parseUseArgs(%q) = (tool %q, group %q, pick %v, sanitize %v), want (%q, %q, %v, %v)",
					c.args, tool, group, pick, sanitize, c.wantTool, c.wantGroup, c.wantPick, c.wantSanitize)
			}
			if model != c.wantModel {
				t.Fatalf("parseUseArgs(%q) model = %q, want %q", c.args, model, c.wantModel)
			}
			if pickModel != c.wantPickModel {
				t.Fatalf("parseUseArgs(%q) pickModel = %v, want %v", c.args, pickModel, c.wantPickModel)
			}
			if !reflect.DeepEqual(extra, c.wantExtra) {
				t.Fatalf("parseUseArgs(%q) extra = %#v, want %#v", c.args, extra, c.wantExtra)
			}
		})
	}
}

func TestParseUseArgsWithTransparent(t *testing.T) {
	// wantTransparent is the tri-state the parser reports: nil = the user said nothing (Use then applies the per-tool default), non-nil = an explicit request. "unset" and "explicitly false" are NOT interchangeable now that transparent is the default — unset falls back silently on a tool with no adapter, explicit-true errors there.
	cases := []struct {
		name            string
		args            []string
		wantTool        string
		wantTransparent *bool
		wantExtra       []string
		wantErr         bool
	}{
		{"bare flag", []string{"claude", "--transparent"}, "claude", boolPtr(true), nil, false},
		{"true value", []string{"--transparent=true", "codex"}, "codex", boolPtr(true), nil, false},
		{"false value", []string{"gemini", "--transparent=false"}, "gemini", boolPtr(false), nil, false},
		{"zero value", []string{"gemini", "--transparent=0"}, "gemini", boolPtr(false), nil, false},
		{"invalid value", []string{"claude", "--transparent=maybe"}, "", nil, nil, true},
		{"unset stays nil", []string{"claude"}, "claude", nil, nil, false},
		{"separator forwards same name", []string{"claude", "--", "--transparent"}, "claude", nil, []string{"--transparent"}, false},
		{"flag before separator only", []string{"claude", "--transparent", "--", "--transparent=false"}, "claude", boolPtr(true), []string{"--transparent=false"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tool, _, _, _, transparent, extra, _, _, err := parseUseArgsWithTransparent(c.args)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if c.wantErr {
				return
			}
			if tool != c.wantTool || !reflect.DeepEqual(transparent, c.wantTransparent) || !reflect.DeepEqual(extra, c.wantExtra) {
				t.Fatalf("got tool=%q transparent=%s extra=%#v; want tool=%q transparent=%s extra=%#v",
					tool, fmtBoolPtr(transparent), extra, c.wantTool, fmtBoolPtr(c.wantTransparent), c.wantExtra)
			}
		})
	}
}

func TestUseArgsWithSelectedToolInsertsBeforeToolSeparator(t *testing.T) {
	tests := []struct {
		name string
		args []string
		tool string
		want []string
	}{
		{name: "bare picker", tool: "codex", want: []string{"codex"}},
		{name: "routing flags", args: []string{"--channel", "team"}, tool: "codex", want: []string{"--channel", "team", "codex"}},
		{name: "tool arguments", args: []string{"--", "resume"}, tool: "codex", want: []string{"codex", "--", "resume"}},
		{name: "flags and tool arguments", args: []string{"--transparent=false", "--", "resume"}, tool: "codex", want: []string{"--transparent=false", "codex", "--", "resume"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := append([]string(nil), tt.args...)
			if got := useArgsWithSelectedTool(tt.args, tt.tool); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("useArgsWithSelectedTool(%#v, %q) = %#v, want %#v", tt.args, tt.tool, got, tt.want)
			}
			if !reflect.DeepEqual(tt.args, original) {
				t.Fatalf("input args mutated: got %#v, want %#v", tt.args, original)
			}
		})
	}
}

// TestParseUseArgsTransparentDoesNotEatValueTokens pins that a bare value literally spelled "transparent" — a routing group, a --model value, or the tool positional — is never mistaken for the --transparent flag. Only a dash-prefixed token toggles the connector; a group named "transparent" must still select that group (and keep the experimental MITM mode off).
func TestParseUseArgsTransparentDoesNotEatValueTokens(t *testing.T) {
	t.Run("channel value", func(t *testing.T) {
		tool, group, pick, _, transparent, _, _, _, err := parseUseArgsWithTransparent(
			[]string{"--channel", "transparent", "claude"})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if tool != "claude" || group != "transparent" || pick || transparent != nil {
			t.Fatalf("got tool=%q group=%q pick=%v transparent=%s; want claude/transparent/false/unset",
				tool, group, pick, fmtBoolPtr(transparent))
		}
	})
	t.Run("model value", func(t *testing.T) {
		tool, _, _, _, transparent, _, model, _, err := parseUseArgsWithTransparent(
			[]string{"hermes", "--model", "transparent"})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if tool != "hermes" || model != "transparent" || transparent != nil {
			t.Fatalf("got tool=%q model=%q transparent=%s; want hermes/transparent/unset",
				tool, model, fmtBoolPtr(transparent))
		}
	})
	t.Run("tool positional", func(t *testing.T) {
		tool, _, _, _, transparent, _, _, _, err := parseUseArgsWithTransparent(
			[]string{"transparent"})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if tool != "transparent" || transparent != nil {
			t.Fatalf("got tool=%q transparent=%s; want transparent/unset", tool, fmtBoolPtr(transparent))
		}
	})
}

func TestUseUsageDocumentsTransparentFlag(t *testing.T) {
	if !strings.Contains(useUsage, "--transparent") {
		t.Fatal("use help does not document --transparent")
	}
	// Transparent is the default now, so the help must say so and must offer the opt-out — a user whose tool breaks needs to find the escape hatch in `everyapi use --help`, not in the source.
	if !strings.Contains(useUsage, "--transparent=false") {
		t.Fatal("use help does not document the --transparent=false opt-out")
	}
	if !strings.Contains(useUsage, "ON BY DEFAULT") {
		t.Fatal("use help does not state that transparent mode is the default")
	}
	if strings.Contains(useUsage, "Experimental") {
		t.Fatal("use help still calls transparent mode experimental after the default flip")
	}
}

func TestUseUsageDocumentsTerminalMode(t *testing.T) {
	for _, text := range []string{"terminal_mode", "native", "tmux", "non-interactive"} {
		if !strings.Contains(useUsage, text) {
			t.Errorf("use help does not document %q", text)
		}
	}
	if !strings.Contains(useUsage, "A bare Codex resume reattaches") {
		t.Error("use help does not document tmux resume reuse and cleanup")
	}
	if !strings.Contains(useUsage, "everyapi use codex -- resume") {
		t.Error("use help does not show the exact managed Codex resume command")
	}
}

func TestUseUsageDocumentsGrok(t *testing.T) {
	if !strings.Contains(useUsage, "grok") {
		t.Fatal("use help does not list grok")
	}
	if !strings.Contains(useUsage, "everyapi use grok") {
		t.Fatal("use help does not include a Grok launch example")
	}
	if strings.Contains(useUsage, "pass model flags to it after --") {
		t.Fatal("use help still tells Grok users to bypass EveryAPI's managed picker")
	}
	if !strings.Contains(useUsage, "Third-party interactive launches without an explicit model show") {
		t.Fatal("use help does not explain the disabled-model picker")
	}
}

func TestUseUsageDocumentsOfficialQwenAndKimiCLIs(t *testing.T) {
	for _, name := range []string{"qwen-code", "kimi-code"} {
		if !strings.Contains(useUsage, name) {
			t.Errorf("use help does not list %s", name)
		}
	}
	if !strings.Contains(useUsage, "Model-selected tools skip their native picker") {
		t.Error("use help does not document model selection for routed CLIs")
	}
}

func TestUseUsageAndModelSelectionIncludeOpenCode(t *testing.T) {
	if !strings.Contains(useUsage, "everyapi use opencode") {
		t.Fatal("use help does not include an OpenCode launch example")
	}
	opencode, err := tools.Lookup("opencode")
	if err != nil {
		t.Fatal(err)
	}
	if !toolRemembersModel(opencode) {
		t.Fatal("OpenCode must remember the model written to its process-scoped config")
	}
}

func TestEveryThirdPartyCLIHasManagedModelSelection(t *testing.T) {
	for _, name := range []string{
		"aider", "cline", "codex", "continue", "copilot", "crush", "droid",
		"forge", "goose", "grok", "hermes", "kimi-code", "kilo", "llxprt",
		"openclaw", "opencode", "openhands", "pi", "qwen-code", "vibe",
	} {
		tool, err := tools.Lookup(name)
		if err != nil {
			t.Fatal(err)
		}
		if tool.ModelEnv == "" && !toolRemembersModel(tool) {
			t.Errorf("%s has neither a ModelEnv picker nor a managed boot-model picker", name)
		}
	}
}

func TestManagedBootPickerSkipsThirdPartyMetadataCommands(t *testing.T) {
	for _, name := range []string{"codex", "grok", "opencode"} {
		tool := tools.Registry[name]
		if managedBootPickerNeeded(tool, []string{"--help"}) {
			t.Errorf("%s --help unexpectedly needs a model picker", name)
		}
		if managedBootPickerNeeded(tool, []string{"--version"}) {
			t.Errorf("%s --version unexpectedly needs a model picker", name)
		}
		if !managedBootPickerNeeded(tool, nil) {
			t.Errorf("normal %s launch skipped the managed model picker", name)
		}
	}
	if !managedBootPickerNeeded(tools.Registry["claude"], []string{"--help"}) {
		t.Fatal("official Claude Code metadata behavior changed unexpectedly")
	}
}

func TestToolArgsForLaunchPinsQwenToOpenAI(t *testing.T) {
	qwen, err := tools.Lookup("qwen-code")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"no override", []string{"--help"}, []string{"--help", "--auth-type=openai"}},
		{"equals override", []string{"--auth-type=anthropic", "prompt"}, []string{"prompt", "--auth-type=openai"}},
		{"space override", []string{"--auth-type", "gemini", "prompt"}, []string{"prompt", "--auth-type=openai"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := toolArgsForLaunch(qwen, tc.args); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("toolArgsForLaunch() = %v, want %v", got, tc.want)
			}
		})
	}

	claude, err := tools.Lookup("claude")
	if err != nil {
		t.Fatal(err)
	}
	original := []string{"--model", "opus"}
	if got := toolArgsForLaunch(claude, original); !reflect.DeepEqual(got, original) {
		t.Errorf("non-Qwen args changed: %v", got)
	}
}

func TestToolArgsForLaunchPreservesCodexResumeScope(t *testing.T) {
	codex, err := tools.Lookup("codex")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"bare picker", []string{"resume"}, []string{"resume"}},
		{"explicit all", []string{"resume", "--all"}, []string{"resume", "--all"}},
		{"last session", []string{"resume", "--last"}, []string{"resume", "--last"}},
		{"specific session", []string{"resume", "session-id"}, []string{"resume", "session-id"}},
		{"long profile", []string{"--profile", "personal", "prompt"}, []string{"prompt"}},
		{"long profile equals", []string{"--profile=personal", "prompt"}, []string{"prompt"}},
		{"short profile", []string{"-p", "personal", "prompt"}, []string{"prompt"}},
		{"short profile attached", []string{"-ppersonal", "prompt"}, []string{"prompt"}},
		{"short profile equals", []string{"-p=personal", "prompt"}, []string{"prompt"}},
		{"long profile after separator is prompt", []string{"--", "--profile", "personal"}, []string{"--", "--profile", "personal"}},
		{"short profile after separator is prompt", []string{"--", "-ppersonal"}, []string{"--", "-ppersonal"}},
		{"missing profile value keeps separator", []string{"--profile", "--", "-pprompt"}, []string{"--", "-pprompt"}},
		{"ordinary launch", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := toolArgsForLaunch(codex, tc.args); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("toolArgsForLaunch() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestToolArgsForLaunchUsesLibreFangStartByDefault(t *testing.T) {
	librefang, err := tools.Lookup("librefang")
	if err != nil {
		t.Fatal(err)
	}
	if got := toolArgsForLaunch(librefang, nil); !reflect.DeepEqual(got, []string{"start"}) {
		t.Fatalf("toolArgsForLaunch(librefang, nil) = %v", got)
	}
	explicit := []string{"doctor"}
	if got := toolArgsForLaunch(librefang, explicit); !reflect.DeepEqual(got, explicit) {
		t.Fatalf("explicit LibreFang args changed: %v", got)
	}
}

func TestToolArgsForLaunchReservesDroidRuntimeSettings(t *testing.T) {
	droid := &tools.Tool{Name: "droid"}
	got := toolArgsForLaunch(droid, []string{
		"--settings", "/tmp/bypass.json", "--auto", "high", "--settings=other.json", "hello",
	})
	want := []string{"--auto", "high", "hello"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("toolArgsForLaunch(droid) = %v, want %v", got, want)
	}
}

func TestToolArgsForLaunchReservesContinueLifecycleConfig(t *testing.T) {
	t.Setenv("EVERYAPI_CONTINUE_MODEL", "chat-model")
	continueTool, err := tools.Lookup("continue")
	if err != nil {
		t.Fatal(err)
	}
	extra, err := continueTool.PrepareWithModels(
		"https://api.everyapi.ai", "secret-relay-key",
		[]tools.Model{{ID: "chat-model", SupportedEndpointTypes: []string{"openai"}}}, "",
	)
	if err != nil {
		t.Fatal(err)
	}
	defer tools.TakePreparedCleanup(extra)()
	configPath := filepath.Join(extra["CONTINUE_GLOBAL_DIR"], "config.yaml")
	nativeArgs := toolArgsForLaunch(continueTool, []string{
		"--config", "/tmp/bypass.yaml", "--verbose", "--config=/tmp/second.yaml", "prompt",
	})
	got := append(tools.TakePreparedArgs(extra), nativeArgs...)
	want := []string{"--config", configPath, "--verbose", "prompt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Continue final args = %v, want lifecycle config only %v", got, want)
	}
}

func TestToolArgsForLaunchReservesClineLifecycleConfig(t *testing.T) {
	cline, err := tools.Lookup("cline")
	if err != nil {
		t.Fatal(err)
	}
	got := toolArgsForLaunch(cline, []string{
		"--data-dir", "/tmp/bypass-data", "--verbose",
		"--config=/tmp/bypass-config",
		"--provider", "openai-native", "-P", "anthropic",
		"--model=gpt-5.6-luna", "-m", "claude-sonnet-5",
		"--key", "bypass-key", "-kinline-key", "prompt",
	})
	want := []string{"--verbose", "prompt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("toolArgsForLaunch(cline) = %v, want lifecycle directories reserved as %v", got, want)
	}
}

func TestToolArgsForLaunchPinsOpenHandsEnvironmentOverride(t *testing.T) {
	openhands := &tools.Tool{Name: "openhands", DefaultArgs: []string{"--override-with-envs"}}
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{nil, []string{"--override-with-envs"}},
		{[]string{"--resume", "last"}, []string{"--override-with-envs", "--resume", "last"}},
		{[]string{"--override-with-envs", "--help"}, []string{"--override-with-envs", "--help"}},
	} {
		if got := toolArgsForLaunch(openhands, tc.args); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("toolArgsForLaunch(openhands, %v) = %v, want %v", tc.args, got, tc.want)
		}
	}
}

func TestToolArgsForLaunchReservesLLxprtProviderFlags(t *testing.T) {
	llxprt := &tools.Tool{Name: "llxprt"}
	got := toolArgsForLaunch(llxprt, []string{
		"--provider", "anthropic", "--baseurl=https://bypass.example/v1", "--model", "bypass",
		"-m", "short-bypass", "--key", "leak", "--keyfile=/tmp/leak", "--key-name", "ambient",
		"--profile-load", "bypass", "--profile={\"provider\":\"anthropic\"}",
		"--set=auth-key=leak", "--set", "base-url=https://bypass.example/v1",
		"--set=modelparam.temperature=0.2", "hello",
	})
	want := []string{"--set=modelparam.temperature=0.2", "hello"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("toolArgsForLaunch(llxprt) = %v, want %v", got, want)
	}
}

func TestNativeLaunchNoticeDoesNotMislabelLibreFangAsAntigravity(t *testing.T) {
	librefang, err := tools.Lookup("librefang")
	if err != nil {
		t.Fatal(err)
	}
	got := nativeLaunchNotice(librefang)
	if !strings.Contains(got, "LibreFang-owned authentication") {
		t.Fatalf("LibreFang notice = %q", got)
	}
	if strings.Contains(got, "Antigravity authentication") {
		t.Fatalf("LibreFang notice names another client: %q", got)
	}
}

func TestNativeLaunchEnvExposesTheCurrentEveryAPIExecutable(t *testing.T) {
	librefang, err := tools.Lookup("librefang")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env, err := nativeLaunchEnv(librefang)
	if err != nil {
		t.Fatal(err)
	}
	if got := env["EVERYAPI_CLI_PATH"]; got != want {
		t.Fatalf("EVERYAPI_CLI_PATH = %q, want current executable %q", got, want)
	}

	antigravity, err := tools.Lookup("antigravity")
	if err != nil {
		t.Fatal(err)
	}
	env, err = nativeLaunchEnv(antigravity)
	if err != nil {
		t.Fatal(err)
	}
	if got := env[tools.CLIPathEnvironment]; got != want {
		t.Fatalf("Antigravity %s = %q, want current executable %q", tools.CLIPathEnvironment, got, want)
	}
}

func TestToolAllowsAutomaticYoloRejectsKimiPromptMode(t *testing.T) {
	kimi, err := tools.Lookup("kimi-code")
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"--prompt", "hello"},
		{"--prompt=hello"},
		{"-p", "hello"},
	} {
		if toolAllowsAutomaticYolo(kimi, args) {
			t.Errorf("Kimi args %v must not receive an automatic --yolo", args)
		}
	}
	if !toolAllowsAutomaticYolo(kimi, []string{"--help"}) {
		t.Error("interactive/non-prompt Kimi launch should still honor the dangerous-mode preference")
	}
	qwen, err := tools.Lookup("qwen-code")
	if err != nil {
		t.Fatal(err)
	}
	if !toolAllowsAutomaticYolo(qwen, []string{"--prompt", "hello"}) {
		t.Error("Qwen supports --prompt with --yolo")
	}
}

func TestCodexWindowsDangerousModeArgsSuppressSandboxSetupPrompt(t *testing.T) {
	codex, err := tools.Lookup("codex")
	if err != nil {
		t.Fatal(err)
	}
	original := []string{codex.YoloFlag, codexHookTrustBypassFlag}
	got := codexWindowsDangerousModeArgs("windows", codex, original)
	want := []string{
		"-c",
		`windows.sandbox="unelevated"`,
		codex.YoloFlag,
		codexHookTrustBypassFlag,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("codexWindowsDangerousModeArgs() = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(original, []string{codex.YoloFlag, codexHookTrustBypassFlag}) {
		t.Fatalf("codexWindowsDangerousModeArgs mutated caller args: %v", original)
	}
}

func TestCodexWindowsDangerousModeArgsKeepsExplicitOverrideLast(t *testing.T) {
	codex, err := tools.Lookup("codex")
	if err != nil {
		t.Fatal(err)
	}
	explicit := `windows.sandbox="elevated"`
	got := codexWindowsDangerousModeArgs(
		"windows",
		codex,
		[]string{codex.YoloFlag, "-c", explicit},
	)
	want := []string{
		"-c",
		`windows.sandbox="unelevated"`,
		codex.YoloFlag,
		"-c",
		explicit,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("codexWindowsDangerousModeArgs() = %v, want explicit override last in %v", got, want)
	}
}

func TestCodexWindowsDangerousModeArgsLeavesOtherLaunchesAlone(t *testing.T) {
	codex, err := tools.Lookup("codex")
	if err != nil {
		t.Fatal(err)
	}
	claude, err := tools.Lookup("claude")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		goos string
		tool *tools.Tool
		args []string
	}{
		{name: "safe Windows Codex", goos: "windows", tool: codex, args: []string{"--help"}},
		{name: "Linux Codex", goos: "linux", tool: codex, args: []string{codex.YoloFlag}},
		{name: "Windows Claude", goos: "windows", tool: claude, args: []string{claude.YoloFlag}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := codexWindowsDangerousModeArgs(tt.goos, tt.tool, tt.args); !reflect.DeepEqual(got, tt.args) {
				t.Fatalf("codexWindowsDangerousModeArgs() = %v, want unchanged %v", got, tt.args)
			}
		})
	}
}

// TestParseUseArgsAcceptsSanitizeWithTransparent checks only that the parser accepts the two flags together. It deliberately does NOT stand in for the removed TestUseRejectsSanitizeAndTransparentTogether: that gate lived in Use, never in the parser, so a parser-level test passes identically with or without the change and pins nothing about it. The real behavioral replacement — that the two now compose into child -> connector -> sanitizer -> gateway rather than erroring — is TestUseWiresTheSanitizerAsTheConnectorUpstream, which drives Use end to end and fails if Use stops pointing the connector at the sanitizer.
func TestParseUseArgsAcceptsSanitizeWithTransparent(t *testing.T) {
	tool, _, _, sanitize, transparent, _, _, _, err := parseUseArgsWithTransparent(
		[]string{"claude", "--sanitize", "--transparent"})
	if err != nil {
		t.Fatalf("err = %v, want --sanitize and --transparent to coexist", err)
	}
	if tool != "claude" || !sanitize || transparent == nil || !*transparent {
		t.Fatalf("got tool=%q sanitize=%v transparent=%s; want claude/true/true",
			tool, sanitize, fmtBoolPtr(transparent))
	}
}

func FuzzParseUseArgsDoesNotPanic(f *testing.F) {
	seeds := []string{
		"claude -- --model opus",
		"--group a --channel b claude",
		"kimi --model= --sanitize=false",
		"-- --group \x00",
		"\u65e5\u672c\u8a9e --channel=\u7d44",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		// Fields deliberately leaves arbitrary Unicode and control bytes inside tokens while bounding argv growth for the fuzzer.
		args := strings.Fields(input)
		if len(args) > 64 {
			args = args[:64]
		}
		_, _, _, _, _, _, _, _ = parseUseArgs(args)
	})
}

// TestResolveToolModel covers the non-interactive branches of the model-precedence chain (flag > env > picker > default). The picker branch needs a TTY + network and is exercised manually; here we pin that the flag wins, a pre-set env is respected, and a tool without ModelEnv is a clean no-op.
func TestResolveToolModel(t *testing.T) {
	creds := &config.Credentials{APIBase: "https://api.everyapi.ai"}
	hermes, err := tools.Lookup("hermes")
	if err != nil {
		t.Fatal(err)
	}
	claude, err := tools.Lookup("claude")
	if err != nil {
		t.Fatal(err)
	}
	env := hermes.ModelEnv // "EVERYAPI_HERMES_MODEL"

	t.Run("flag wins and exports", func(t *testing.T) {
		t.Setenv(env, "") // isolate + auto-restore
		if err := resolveToolModel(hermes, creds, "relay-key", "gpt-5.1"); err != nil {
			t.Fatal(err)
		}
		if got := os.Getenv(env); got != "gpt-5.1" {
			t.Errorf("%s = %q, want %q", env, got, "gpt-5.1")
		}
	})

	t.Run("flag overrides a pre-set env", func(t *testing.T) {
		t.Setenv(env, "claude-sonnet-4-6")
		if err := resolveToolModel(hermes, creds, "relay-key", "gpt-5.1"); err != nil {
			t.Fatal(err)
		}
		if got := os.Getenv(env); got != "gpt-5.1" {
			t.Errorf("flag should beat env: %s = %q, want %q", env, got, "gpt-5.1")
		}
	})

	t.Run("pre-set env respected when no flag", func(t *testing.T) {
		t.Setenv(env, "preset-model")
		if err := resolveToolModel(hermes, creds, "relay-key", ""); err != nil {
			t.Fatal(err)
		}
		if got := os.Getenv(env); got != "preset-model" {
			t.Errorf("env override clobbered: %s = %q, want %q", env, got, "preset-model")
		}
	})

	t.Run("promotional metadata exposes only smart", func(t *testing.T) {
		t.Setenv(env, "")
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/models" {
				http.NotFound(w, r)
				return
			}
			_, _ = io.WriteString(w, `{"promotional_only":true,"required_group":"auto","data":[`+
				`{"id":"z-image","supported_endpoint_types":["image-generation"]},`+
				`{"id":"gpt-5.6-luna","supported_endpoint_types":["openai-response"],"chat_completions_bridge":true},`+
				`{"id":"smart-everyapi","supported_endpoint_types":["openai"]},`+
				`{"id":"a-anthropic-only","supported_endpoint_types":["anthropic"]},`+
				`{"id":"aa-responses-only","supported_endpoint_types":["openai-response"]}`+
				`]}`)
		}))
		defer srv.Close()
		catalogCreds := &config.Credentials{APIBase: srv.URL}
		if err := resolveToolModel(hermes, catalogCreds, "relay-key", ""); err != nil {
			t.Fatal(err)
		}
		if got := os.Getenv(env); got != "smart-everyapi" {
			t.Errorf("%s = %q, want smart-everyapi for a promotional-only catalog", env, got)
		}
	})

	t.Run("tool without ModelEnv is a no-op", func(t *testing.T) {
		t.Setenv(env, "")
		if err := resolveToolModel(claude, creds, "relay-key", ""); err != nil {
			t.Fatal(err)
		}
		if got := os.Getenv(env); got != "" {
			t.Errorf("claude should not touch %s, got %q", env, got)
		}
	})

	t.Run("non-hermes tool does not inherit hermes history", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		t.Setenv("EVERYAPI_HERMES_MODEL", "z-last-hermes-model")
		if _, err := hermes.Prepare("https://api.everyapi.ai", "relay-key"); err != nil {
			t.Fatal(err)
		}
		qwen, err := tools.Lookup("qwen-code")
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv(qwen.ModelEnv, "")
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `{"data":[`+
				`{"id":"a-first-qwen-model","supported_endpoint_types":["openai"]},`+
				`{"id":"z-last-hermes-model","supported_endpoint_types":["openai"]}`+
				`]}`)
		}))
		defer srv.Close()
		if err := resolveToolModel(qwen, &config.Credentials{APIBase: srv.URL}, "relay-key", ""); err != nil {
			t.Fatal(err)
		}
		if got := os.Getenv(qwen.ModelEnv); got != "a-first-qwen-model" {
			t.Errorf("%s = %q, want qwen's first model rather than Hermes history", qwen.ModelEnv, got)
		}
	})
}

func TestResolveToolModelFromCatalogRejectsFailureAndStaleExplicitSelection(t *testing.T) {
	t.Setenv("OPENAI_MODEL", "")
	tool, _ := tools.Lookup("qwen-code")
	catalogErr := errors.New("catalog unavailable")
	if err := resolveToolModelFromCatalog(tool, nil, catalogErr, "gpt-stale"); err == nil {
		t.Fatal("explicit model bypassed a failed live catalog")
	}
	catalog := []api.RelayModel{{ID: "qwen-live", SupportedEndpointTypes: []string{"openai"}}}
	if err := resolveToolModelFromCatalog(tool, catalog, nil, "qwen-stale"); err == nil {
		t.Fatal("model absent from the current key/group catalog was accepted")
	}
}

func TestResolveToolModelFromCatalogAcceptsAlternativeEndpoint(t *testing.T) {
	tool := &tools.Tool{
		Name:                "dual-wire",
		ExecName:            "dual-wire",
		ModelEnv:            "EVERYAPI_DUAL_WIRE_MODEL",
		RequiredEndpoint:    "openai",
		AlternativeEndpoint: "openai-response",
	}
	t.Setenv(tool.ModelEnv, "")
	catalog := []api.RelayModel{{
		ID:                     "responses-only",
		SupportedEndpointTypes: []string{"openai-response"},
	}}
	if err := resolveToolModelFromCatalog(tool, catalog, nil, "responses-only"); err != nil {
		t.Fatalf("alternative endpoint model was rejected: %v", err)
	}
	if got := os.Getenv(tool.ModelEnv); got != "responses-only" {
		t.Fatalf("%s = %q, want responses-only", tool.ModelEnv, got)
	}
}

func TestResolveToolModelFromCatalogAcceptsChatCompletionsBridge(t *testing.T) {
	tool, err := tools.Lookup("qwen-code")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(tool.ModelEnv, "")
	catalog := []api.RelayModel{{
		ID:                     "gpt-5.6-luna",
		SupportedEndpointTypes: []string{"openai-response"},
		ChatCompletionsBridge:  true,
	}}
	if !catalogSupportsToolEndpoint(catalog, tool) {
		t.Fatal("bridged Luna did not satisfy the tool endpoint preflight")
	}
	if models := launchModelsForTool(tool, catalog, ""); len(models) != 1 || models[0].ID != "gpt-5.6-luna" {
		t.Fatalf("bridged Luna launch catalog = %#v", models)
	}
	if err := resolveToolModelFromCatalog(tool, catalog, nil, "gpt-5.6-luna"); err != nil {
		t.Fatalf("bridged Luna was rejected by an OpenAI Chat Completions tool: %v", err)
	}
	if got := os.Getenv(tool.ModelEnv); got != "gpt-5.6-luna" {
		t.Fatalf("%s = %q, want gpt-5.6-luna", tool.ModelEnv, got)
	}
}

func TestPreferredToolModelIndexUsesHermesHistoryOnlyForHermes(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("EVERYAPI_HERMES_MODEL", "z-hermes-history")
	hermes, err := tools.Lookup("hermes")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hermes.Prepare("https://api.everyapi.ai", "relay-key"); err != nil {
		t.Fatal(err)
	}
	qwen, err := tools.Lookup("qwen-code")
	if err != nil {
		t.Fatal(err)
	}
	models := []string{"a-first-model", "claude-sonnet-5", "z-hermes-history"}
	if got := preferredToolModelIndex(qwen, models); got != 0 {
		t.Errorf("Qwen initial index = %d, want first available non-Claude model", got)
	}
	if got := preferredToolModelIndex(hermes, models); got != 2 {
		t.Errorf("Hermes initial index = %d, want saved model", got)
	}
	if got := preferredToolModel(qwen, []string{"a-first-model", "b-second-model"}); got != "a-first-model" {
		t.Errorf("Qwen fallback = %q, want first live-catalog model", got)
	}
}

func TestPreferredToolModelDoesNotDefaultThirdPartyClientsToClaude(t *testing.T) {
	aider, err := tools.Lookup("aider")
	if err != nil {
		t.Fatal(err)
	}
	models := []string{"MiniMax-M3", "claude-sonnet-5", "kimi-k2.7-code"}
	if got := preferredToolModel(aider, models); got != "MiniMax-M3" {
		t.Errorf("Aider default = %q, want first available non-Claude route", got)
	}
	if got := preferredToolModel(aider, []string{"MiniMax-M3", "kimi-k2.7-code"}); got != "MiniMax-M3" {
		t.Errorf("Aider fallback = %q, want first live-catalog model", got)
	}
}

func TestThirdPartyClientsKeepClaudeVisibleButUnavailable(t *testing.T) {
	tool, _ := tools.Lookup("pi")
	catalog := []api.RelayModel{
		{ID: "claude-sonnet-5", SupportedEndpointTypes: []string{"anthropic", "openai-response"}},
		{ID: "gpt-5.6-terra", SupportedEndpointTypes: []string{"openai-response"}},
	}
	if got := chatModelsForTool(catalog, tool); !reflect.DeepEqual(got, []string{"gpt-5.6-terra"}) {
		t.Fatalf("Pi selectable models = %v, want only the non-Claude route", got)
	}
	choices := modelPickerChoicesForTool(catalog, tool)
	wantChoices := []toolModelChoice{
		{id: "claude-sonnet-5", unavailable: true},
		{id: "gpt-5.6-terra"},
	}
	if !reflect.DeepEqual(choices, wantChoices) {
		t.Fatalf("Pi picker choices = %#v, want %#v", choices, wantChoices)
	}
	if _, err := pickManagedModelForTool(tool, catalog[:1], ""); !errors.Is(err, cliprompt.ErrPickUnavailable) {
		t.Fatalf("Pi all-Claude picker error = %v, want ErrPickUnavailable", err)
	}
	t.Setenv(tool.ModelEnv, "")
	if err := resolveToolModelFromCatalog(tool, catalog, nil, "claude-sonnet-5"); err == nil {
		t.Fatal("explicit Claude selection bypassed third-party unavailability")
	}
	if got := launchModelsForTool(tool, catalog, ""); len(got) != 1 || got[0].ID != "gpt-5.6-terra" {
		t.Fatalf("Pi launch catalog = %#v, want Claude excluded", got)
	}
}

func TestForgeResponseOnlyModelReachesResponsesProvider(t *testing.T) {
	t.Setenv("EVERYAPI_FORGE_MODEL", "gpt-5.6-luna")
	tool, err := tools.Lookup("forge")
	if err != nil {
		t.Fatal(err)
	}
	catalog := []api.RelayModel{
		{ID: "chat-only", SupportedEndpointTypes: []string{"openai"}},
		{ID: "gpt-5.6-luna", SupportedEndpointTypes: []string{"openai-response"}},
	}
	models := launchModelsForTool(tool, catalog, "")
	if len(models) != 2 {
		t.Fatalf("Forge launch catalog = %#v, want chat and Responses models", models)
	}
	extra, err := tool.PrepareWithModels(
		"https://api.everyapi.ai", "secret-relay-key", models, "",
	)
	if err != nil {
		t.Fatal(err)
	}
	defer tools.TakePreparedCleanup(extra)()
	body, err := os.ReadFile(filepath.Join(extra["FORGE_CONFIG"], ".forge.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `provider_id = "openai_responses_compatible"`) ||
		!strings.Contains(string(body), `model_id = "gpt-5.6-luna"`) {
		t.Fatalf("Forge Responses session config = %s", body)
	}
	if strings.Contains(string(body), "secret-relay-key") {
		t.Fatal("Forge config persisted the relay credential")
	}
}

func TestApplyAgentContextMergesClaudeSystemPrompt(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-501/default,1,0")
	t.Setenv(tools.TerminalModeEnvironment, "tmux")
	t.Setenv(tools.TmuxSessionEnvironment, "everyapi-123-456")
	t.Setenv(tools.TmuxAttachCommandEnvironment, "tmux attach -t everyapi-123-456")
	claude, err := tools.Lookup("claude")
	if err != nil {
		t.Fatal(err)
	}
	got := applyAgentContext(claude, []string{"--append-system-prompt", "User instructions", "resume"})
	want := []string{"--append-system-prompt", "User instructions\n\n" + tools.AgentInstructions(), "resume"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Claude args = %#v, want %#v", got, want)
	}
	got = applyAgentContext(claude, []string{"--append-system-prompt=Inline instructions", "resume"})
	want = []string{"--append-system-prompt=Inline instructions\n\n" + tools.AgentInstructions(), "resume"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Claude inline-prompt args = %#v, want %#v", got, want)
	}
	got = applyAgentContext(claude, []string{"resume"})
	want = []string{"--append-system-prompt", tools.AgentInstructions(), "resume"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Claude args without user prompt = %#v, want %#v", got, want)
	}
	if got := applyAgentContext(claude, []string{"--help"}); !reflect.DeepEqual(got, []string{"--help"}) {
		t.Fatalf("Claude help args = %#v, want unchanged", got)
	}
	t.Setenv("TMUX", "")
	want = []string{"--append-system-prompt", tools.AgentInstructions(), "resume"}
	if got := applyAgentContext(claude, []string{"resume"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("Claude native args = %#v, want artifact standard %#v", got, want)
	}
}

func TestClineChatAndResponsesModelsReachMatchingProviders(t *testing.T) {
	tool, err := tools.Lookup("cline")
	if err != nil {
		t.Fatal(err)
	}
	catalog := []api.RelayModel{
		{ID: "MiniMax-M3", SupportedEndpointTypes: []string{"openai"}},
		{ID: "gpt-5.6-luna", SupportedEndpointTypes: []string{"openai-response"}},
	}
	models := launchModelsForTool(tool, catalog, "")
	if len(models) != 2 {
		t.Fatalf("Cline launch catalog = %#v, want Chat and Responses models", models)
	}
	for _, tc := range []struct {
		name             string
		selected         string
		selectedProvider string
	}{
		{name: "chat", selected: "MiniMax-M3", selectedProvider: "lmstudio"},
		{name: "responses", selected: "gpt-5.6-luna", selectedProvider: "lmstudio"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("EVERYAPI_CLINE_MODEL", tc.selected)
			extra, err := tool.PrepareWithModels(
				"https://api.everyapi.ai", "secret-relay-key", models, "",
			)
			if err != nil {
				t.Fatal(err)
			}
			defer tools.TakePreparedCleanup(extra)()
			dataDir := extra["CLINE_DATA_DIR"]
			providerPath := extra["CLINE_PROVIDER_SETTINGS_PATH"]
			if providerPath != filepath.Join(dataDir, "settings", "providers.json") {
				t.Fatalf("Cline provider path = %q, want isolated data directory %q", providerPath, dataDir)
			}
			body, err := os.ReadFile(providerPath)
			if err != nil {
				t.Fatal(err)
			}
			var settings struct {
				LastUsedProvider string `json:"lastUsedProvider"`
				Providers        map[string]struct {
					Settings struct {
						Provider string `json:"provider"`
						Model    string `json:"model"`
					} `json:"settings"`
				} `json:"providers"`
			}
			if err := json.Unmarshal(body, &settings); err != nil {
				t.Fatal(err)
			}
			if settings.LastUsedProvider != tc.selectedProvider {
				t.Fatalf("Cline last provider = %q, want %q", settings.LastUsedProvider, tc.selectedProvider)
			}
			provider := settings.Providers[tc.selectedProvider].Settings
			if provider.Provider != tc.selectedProvider || provider.Model != tc.selected {
				t.Fatalf("Cline selected provider settings = %#v", provider)
			}
			catalogBody, err := os.ReadFile(filepath.Join(dataDir, "settings", "models.json"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(catalogBody), "secret-relay-key") ||
				!strings.Contains(string(catalogBody), `"name":"EveryAPI"`) ||
				strings.Contains(string(catalogBody), `"name":"EveryAPI Responses"`) ||
				!strings.Contains(string(catalogBody), `"MiniMax-M3"`) ||
				!strings.Contains(string(catalogBody), `"gpt-5.6-luna"`) {
				t.Fatalf("Cline model catalog = %s", catalogBody)
			}
		})
	}
}

func TestClineNonGPTResponsesModelsRetainNativeProvider(t *testing.T) {
	tool, err := tools.Lookup("cline")
	if err != nil {
		t.Fatal(err)
	}
	for _, selected := range []string{"future-response-model", "codex-next", "o9-preview"} {
		t.Run(selected, func(t *testing.T) {
			t.Setenv("EVERYAPI_CLINE_MODEL", selected)
			models := launchModelsForTool(tool, []api.RelayModel{
				{ID: "MiniMax-M3", SupportedEndpointTypes: []string{"openai"}},
				{ID: selected, SupportedEndpointTypes: []string{"openai-response"}},
			}, "")
			extra, err := tool.PrepareWithModels(
				"https://api.everyapi.ai", "secret-relay-key", models, "",
			)
			if err != nil {
				t.Fatal(err)
			}
			defer tools.TakePreparedCleanup(extra)()

			providerBody, err := os.ReadFile(extra["CLINE_PROVIDER_SETTINGS_PATH"])
			if err != nil {
				t.Fatal(err)
			}
			var settings struct {
				LastUsedProvider string `json:"lastUsedProvider"`
			}
			if err := json.Unmarshal(providerBody, &settings); err != nil {
				t.Fatal(err)
			}
			if settings.LastUsedProvider != "openai-native" {
				t.Fatalf("Cline provider for %q = %q, want native Responses", selected, settings.LastUsedProvider)
			}

			catalogBody, err := os.ReadFile(filepath.Join(extra["CLINE_DATA_DIR"], "settings", "models.json"))
			if err != nil {
				t.Fatal(err)
			}
			var catalog struct {
				Providers map[string]struct {
					Provider struct {
						Protocol string `json:"protocol"`
						Client   string `json:"client"`
					} `json:"provider"`
				} `json:"providers"`
			}
			if err := json.Unmarshal(catalogBody, &catalog); err != nil {
				t.Fatal(err)
			}
			provider := catalog.Providers["openai-native"].Provider
			if provider.Protocol != "openai-responses" || provider.Client != "openai" {
				t.Fatalf("Cline fallback provider for %q = %#v", selected, provider)
			}
		})
	}
}

func TestClaudeModelClassifierCoversProviderQualifiedIDs(t *testing.T) {
	thirdParty, _ := tools.Lookup("opencode")
	official, _ := tools.Lookup("claude")
	for _, modelID := range []string{
		"claude",
		"claude-sonnet-5",
		"anthropic/claude",
		"openai/claude-sonnet-5",
		"bedrock/anthropic.claude-3-7-sonnet",
		"bedrock/us.anthropic.claude-3-5-sonnet-20241022-v2:0",
		"eu.anthropic.claude-3-7-sonnet-20250219-v1:0",
		"vertex:claude-opus-4-8",
	} {
		if !modelUnavailableForTool(thirdParty, modelID) {
			t.Errorf("%q should be unavailable to a third-party client", modelID)
		}
		if modelUnavailableForTool(official, modelID) {
			t.Errorf("%q should remain available to official Claude Code", modelID)
		}
	}
	for _, modelID := range []string{"claudel", "my-claude-model", "anthropic/other", "gpt-5.6-terra"} {
		if modelUnavailableForTool(thirdParty, modelID) {
			t.Errorf("near-match %q was incorrectly classified as Claude", modelID)
		}
	}
}

func TestNativeClaudeModelArgumentsCannotBypassThirdPartyPolicy(t *testing.T) {
	aider, _ := tools.Lookup("aider")
	for _, args := range [][]string{
		{"--model", "openai/claude-sonnet-5"},
		{"--model", "sonnet"},
		{"--model=anthropic/claude"},
		{"--weak-model", "bedrock/anthropic.claude-3-7-sonnet"},
		{"--editor-model=opus"},
	} {
		if got := nativeUnavailableModelArgument(aider, args); got == "" {
			t.Errorf("native args %v bypassed the Claude policy", args)
		}
	}
	for _, toolName := range []string{"codex", "opencode", "qwen-code"} {
		tool, _ := tools.Lookup(toolName)
		for _, args := range [][]string{
			{"-m", "bedrock/anthropic.claude-3-7-sonnet"},
			{"-m=claude"},
			{"-mopenai/claude-sonnet-5"},
		} {
			if got := nativeUnavailableModelArgument(tool, args); got == "" {
				t.Errorf("%s native args %v bypassed the Claude policy", toolName, args)
			}
		}
	}
	if got := nativeUnavailableModelArgument(aider, []string{"-m", "claude"}); got != "" {
		t.Errorf("Aider's -m message flag was misread as a model: %q", got)
	}
	pi, _ := tools.Lookup("pi")
	for _, args := range [][]string{
		{"--model", "sonnet:high"},
		{"--model", "gpt-5.6-terra"},
		{"--provider", "anthropic", "--model", "sonn"},
		{"--provider=anthropic"},
		{"--models", "gpt-5.6-terra,anthropic/*"},
		{"--models", "*claude*"},
		{"--models", "*"},
	} {
		if got := nativeUnavailableModelArgument(pi, args); got == "" {
			t.Errorf("Pi native args %v bypassed EveryAPI's exact model validation", args)
		}
	}
	if got := nativeUnavailableModelArgument(aider, []string{"--model", "gpt-5.6-terra"}); got != "" {
		t.Errorf("non-Claude native model was rejected: %q", got)
	}
	grok, _ := tools.Lookup("grok")
	for _, args := range [][]string{
		{"--model", "gpt-5.6-terra"},
		{"--model=gpt-5.6-terra"},
		{"-m", "gpt-5.6-terra"},
		{"-mgpt-5.6-terra"},
	} {
		if got := nativeUnavailableModelArgument(grok, args); got == "" {
			t.Errorf("Grok native args %v bypassed EveryAPI's exact model validation", args)
		}
	}
	claude, _ := tools.Lookup("claude")
	if got := nativeUnavailableModelArgument(claude, []string{"--model", "opus"}); got != "" {
		t.Errorf("official Claude Code argument was rejected: %q", got)
	}
}

func TestCreateDefaultRelayKeyCreatesThenCaches(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var gotCreate api.TokenCreate
	var postCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/token/":
			postCount++
			if err := json.NewDecoder(r.Body).Decode(&gotCreate); err != nil {
				t.Errorf("decode create body: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
		case r.Method == http.MethodGet && r.URL.Path == "/api/token/":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data": map[string]any{"items": []map[string]any{
					{"id": 7, "name": gotCreate.Name, "status": api.TokenStatusEnabled, "group": "auto"},
				}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/token/7/key":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data":    map[string]any{"key": "sk-everyapi-created"},
			})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	creds := &config.Credentials{APIBase: srv.URL, AccessToken: "mgmt-token", UserID: 42}
	if err := config.Save(creds); err != nil {
		t.Fatal(err)
	}
	key, err := createDefaultRelayKey(creds)
	if err != nil {
		t.Fatal(err)
	}
	if key != "sk-everyapi-created" {
		t.Fatalf("key = %q, want created key", key)
	}
	if creds.RelayKey != "sk-everyapi-created" {
		t.Fatalf("creds.RelayKey = %q, want cached key", creds.RelayKey)
	}
	if postCount != 1 {
		t.Fatalf("POST /api/token count = %d, want 1", postCount)
	}
	if gotCreate.Name == "" {
		t.Fatal("created token name is empty")
	}
	if gotCreate.Group != "auto" {
		t.Fatalf("created token group = %q, want the auto group", gotCreate.Group)
	}
	if !gotCreate.CrossGroupRetry {
		t.Fatal("created auto token should retry across groups")
	}
	if !gotCreate.UnlimitedQuota {
		t.Fatal("created token should be unlimited")
	}
	if gotCreate.ExpiredTime != api.TokenExpiresNever {
		t.Fatalf("created token expiry = %d, want never", gotCreate.ExpiredTime)
	}
	if gotCreate.ModelLimitsEnabled || gotCreate.ModelLimits != "" {
		t.Fatalf("created token should not have model limits: %#v", gotCreate)
	}
}

// The gateway rejects a create that names the auto group without the tier grant, so an account that cannot use it must still get its first key — the rejected create retries once without a group.
func TestCreateDefaultRelayKeyRetriesWithoutGroupWhenAutoRejected(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var creates []api.TokenCreate
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/token/":
			var req api.TokenCreate
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode create body: %v", err)
			}
			creates = append(creates, req)
			if req.Group == api.GroupAuto {
				// What validateTokenGroup answers without the tier grant.
				_ = json.NewEncoder(w).Encode(map[string]any{
					"success": false,
					"message": "no access to group auto",
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
		case r.Method == http.MethodGet && r.URL.Path == "/api/token/":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data": map[string]any{"items": []map[string]any{
					{"id": 9, "name": "first", "status": api.TokenStatusEnabled, "group": ""},
				}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/token/9/key":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data":    map[string]any{"key": "sk-everyapi-default-pool"},
			})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	creds := &config.Credentials{APIBase: srv.URL, AccessToken: "mgmt-token", UserID: 42}
	if err := config.Save(creds); err != nil {
		t.Fatal(err)
	}
	key, err := createDefaultRelayKey(creds)
	if err != nil {
		t.Fatal(err)
	}
	if key != "sk-everyapi-default-pool" {
		t.Fatalf("key = %q, want the created default-pool key", key)
	}
	if len(creates) != 2 {
		t.Fatalf("create attempts = %d, want the auto try plus one groupless retry", len(creates))
	}
	if creates[0].Group != api.GroupAuto || !creates[0].CrossGroupRetry {
		t.Fatalf("first attempt = %+v, want the auto group with cross-group retry", creates[0])
	}
	if creates[1].Group != "" {
		t.Fatalf("retry group = %q, want the default pool", creates[1].Group)
	}
	if creates[1].CrossGroupRetry {
		t.Fatal("a default-pool key must not ask for cross-group retry")
	}
}

func TestWantsUseHelp(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"empty", nil, false},
		{"bare tool", []string{"claude"}, false},
		{"--help alone", []string{"--help"}, true},
		{"-h alone", []string{"-h"}, true},
		{"help alone", []string{"help"}, true},
		{"help after tool is not help", []string{"claude", "help"}, false},
		{"--help after tool still triggers", []string{"claude", "--help"}, true},
		{"--help shielded by --", []string{"claude", "--", "--help"}, false},
		{"-h shielded by --", []string{"claude", "--", "-h"}, false},
		{"help shielded by --", []string{"claude", "--", "help"}, false},
		// The bug the helper exists to prevent: a routing group literally named "help" must not be intercepted.
		{"--group help (space form)", []string{"claude", "--group", "help"}, false},
		{"--channel help (space form)", []string{"claude", "--channel", "help"}, false},
		{"--group=help (eq form)", []string{"claude", "--group=help"}, false},
		{"--channel help before tool", []string{"--channel", "help", "claude"}, false},
		// But an actual --help / -h after --group still triggers.
		{"--group val then --help", []string{"--group", "byteplus", "--help"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := wantsUseHelp(c.args); got != c.want {
				t.Fatalf("wantsUseHelp(%q) = %v, want %v", c.args, got, c.want)
			}
		})
	}
}

func TestLaunchModelsForClaudeUsesAnthropicProtocol(t *testing.T) {
	catalog := []api.RelayModel{
		{ID: "claude-ok", OwnedBy: "anthropic", SupportedEndpointTypes: []string{"anthropic"}},
		{ID: "gpt-chat-only", OwnedBy: "openai", SupportedEndpointTypes: []string{"openai"}},
		{ID: "qwen-openai", OwnedBy: "qwen", SupportedEndpointTypes: []string{"openai"}},
		{ID: "qwen-anthropic", OwnedBy: "qwen", SupportedEndpointTypes: []string{"anthropic"}},
		{ID: "qwen-image", OwnedBy: "qwen", SupportedEndpointTypes: []string{"image-generation", "openai"}},
	}
	claude, _ := tools.Lookup("claude")
	if got := launchModelsForTool(claude, catalog, ""); len(got) != 2 || got[0].ID != "claude-ok" || got[1].ID != "qwen-anthropic" {
		t.Fatalf("plain Claude launch catalog = %#v", got)
	}
}

func TestLaunchModelsForOpenCodeIncludesChatAndResponsesProtocols(t *testing.T) {
	catalog := []api.RelayModel{
		{ID: "gpt-chat", SupportedEndpointTypes: []string{"openai"}, ContextWindow: 131072, MaxOutput: 16384},
		{ID: "gpt-5.6-terra", SupportedEndpointTypes: []string{"openai-response"}},
		{ID: "claude-only", SupportedEndpointTypes: []string{"anthropic"}},
	}
	opencode, _ := tools.Lookup("opencode")
	got := launchModelsForTool(opencode, catalog, "")
	if len(got) != 2 || got[0].ID != "gpt-5.6-terra" || got[1].ID != "gpt-chat" {
		t.Fatalf("OpenCode launch catalog = %#v", got)
	}
	if got[1].ContextWindow != 131072 || got[1].MaxOutput != 16384 {
		t.Fatalf("OpenCode launch token limits = %d/%d", got[1].ContextWindow, got[1].MaxOutput)
	}
}

func TestCatalogSupportsEndpoint(t *testing.T) {
	cases := []struct {
		name     string
		catalog  []api.RelayModel
		endpoint string
		want     bool
	}{
		{"matching model", []api.RelayModel{{ID: "gemini-pro", SupportedEndpointTypes: []string{"gemini"}}}, "gemini", true},
		{"case insensitive", []api.RelayModel{{ID: "gemini-pro", SupportedEndpointTypes: []string{"Gemini"}}}, "gemini", true},
		{"chat completions bridge", []api.RelayModel{{ID: "gpt-5.6-luna", SupportedEndpointTypes: []string{"openai-response"}, ChatCompletionsBridge: true}}, "openai", true},
		{"explicitly incompatible", []api.RelayModel{{ID: "claude", SupportedEndpointTypes: []string{"anthropic", "openai"}}}, "gemini", false},
		{"missing metadata fails open", []api.RelayModel{{ID: "legacy"}}, "gemini", true},
		{"explicit empty metadata fails closed", []api.RelayModel{{ID: "unroutable", SupportedEndpointTypes: []string{}}}, "gemini", false},
		{"empty catalog", nil, "gemini", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := catalogSupportsEndpoint(tc.catalog, tc.endpoint); got != tc.want {
				t.Errorf("catalogSupportsEndpoint() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCatalogSupportsToolEndpointRejectsMediaOnlyModels(t *testing.T) {
	grok, err := tools.Lookup("grok")
	if err != nil {
		t.Fatal(err)
	}
	catalog := []api.RelayModel{{
		ID:                     "image-only",
		SupportedEndpointTypes: []string{"openai", "image-generation"},
	}}
	if catalogSupportsToolEndpoint(catalog, grok) {
		t.Fatal("media-only OpenAI model must not satisfy a routed text client")
	}
}

func TestChatCapabilityDistinguishesMissingFromExplicitEmptyMetadata(t *testing.T) {
	if !chatCapable(nil) {
		t.Fatal("missing metadata from an older gateway must remain compatible")
	}
	if chatCapable([]string{}) {
		t.Fatal("an explicit empty endpoint list means this key has no callable protocol")
	}
	if !supportsEndpoint(nil, "openai") {
		t.Fatal("missing metadata from an older gateway must remain compatible")
	}
	if supportsEndpoint([]string{}, "openai") {
		t.Fatal("an explicit empty endpoint list must not satisfy a required protocol")
	}
}

func TestToolInvocationNeedsEndpoint(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}, {"--version"}, {"-v"}} {
		if toolInvocationNeedsEndpoint(args) {
			t.Errorf("%v should not require a model endpoint", args)
		}
	}
	for _, args := range [][]string{nil, {"--prompt", "hi"}, {"--model", "gemini-pro"}} {
		if !toolInvocationNeedsEndpoint(args) {
			t.Errorf("%v should require a model endpoint", args)
		}
	}
}

// TestTransparentTopologyReversesCreationOrder pins the direction of the launch line. Hops are collected as they are built, each pointing at the previous one as its upstream, so traffic runs the other way and the first hop built prints last. Emitted in creation order the line stays plausible-looking while naming the chain backwards.
//
// Launches currently build at most one hop, so the multi-hop cases here are ahead of the code on purpose: they are what makes the invariant survive someone adding a second hop later.
func TestTransparentTopologyReversesCreationOrder(t *testing.T) {
	const (
		connector = "http://127.0.0.1:1000"
		first     = "http://127.0.0.1:2000"
		second    = "http://127.0.0.1:3000"
		gateway   = "https://api.everyapi.ai"
	)
	for _, tc := range []struct {
		name string
		hops []string
		want string
	}{
		{"no hops", nil, connector + " → " + gateway},
		{"single hop", []string{first}, connector + " → " + first + " → " + gateway},
		{
			"second hop prints before the first",
			[]string{first, second},
			connector + " → " + second + " → " + first + " → " + gateway,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := transparentTopology(connector, tc.hops, gateway); got != tc.want {
				t.Fatalf("topology = %q, want %q", got, tc.want)
			}
		})
	}
}

func boolPtr(b bool) *bool { return &b }

// fmtBoolPtr renders the parser's tri-state readably in failure messages, so an "unset vs explicitly false" mismatch is obvious rather than printing as a bare pointer address.
func fmtBoolPtr(b *bool) string {
	if b == nil {
		return "unset"
	}
	if *b {
		return "true"
	}
	return "false"
}

// TestSortLaunchModelsPutsTheChoiceFirst pins the ordering that decides what a self-selecting client boots on. Plain alphabetical order made position 0 an accident of the id: an account holding "ark-…" models had claude boot on one purely because 'a' sorts before 'c'.
func TestSortLaunchModelsPutsTheChoiceFirst(t *testing.T) {
	claude := tools.Registry["claude"]
	codex := tools.Registry["codex"]
	ids := func(models []tools.Model) []string {
		out := make([]string, 0, len(models))
		for _, m := range models {
			out = append(out, m.ID)
		}
		return out
	}
	catalog := func() []tools.Model {
		return []tools.Model{
			{ID: "ark-doubao-seed"}, {ID: "claude-sonnet-4-6"},
			{ID: "zzz-last"}, {ID: "claude-opus-4-8"},
		}
	}

	models := catalog()
	sortLaunchModels(claude, models, "")
	// No choice yet: claude's own ids come first, alphabetical within each tier.
	if got := ids(models); !reflect.DeepEqual(got, []string{
		"claude-opus-4-8", "claude-sonnet-4-6", "ark-doubao-seed", "zzz-last"}) {
		t.Fatalf("claude order without a choice = %v", got)
	}

	models = catalog()
	sortLaunchModels(claude, models, "ark-doubao-seed")
	if got := ids(models); got[0] != "ark-doubao-seed" {
		t.Fatalf("a remembered model must sort first, got %v", got)
	}

	// grok is served a filtered catalogue and boots on its head too, so it gets the same native tier — without it an unrelated vendor's model sits in front of grok's own.
	models = append(catalog(), tools.Model{ID: "grok-4"})
	sortLaunchModels(tools.Registry["grok"], models, "")
	if got := ids(models); got[0] != "grok-4" {
		t.Fatalf("grok order = %v, want its own model first", got)
	}

	// codex has no native-prefix tier — only the choice is privileged.
	models = catalog()
	sortLaunchModels(codex, models, "zzz-last")
	if got := ids(models); !reflect.DeepEqual(got, []string{
		"zzz-last", "ark-doubao-seed", "claude-opus-4-8", "claude-sonnet-4-6"}) {
		t.Fatalf("codex order = %v", got)
	}
}

// TestSortSurvivesClaudeAliasing pins the load-bearing link between the two halves of this feature. The chain is sortLaunchModels -> claudeCatalogModels -> the catalogue proxy's /v1/models response, and the remembered model only reaches claude at position 0 if the aliasing step preserves input order.
//
// It does today because claudeCatalogModels is a range-and-append. Nothing asserted it, so adding e.g. a "claude-* first" pass inside that function would silently break the guarantee with every other test still green.
func TestSortSurvivesClaudeAliasing(t *testing.T) {
	claude := tools.Registry["claude"]
	models := []tools.Model{
		{ID: "claude-opus-4-8"}, {ID: "ark-doubao-seed"}, {ID: "zzz-last"},
	}
	// A non-claude id is the interesting case: it is the one that gets republished under a synthetic alias, so its identity survives only in DisplayName.
	const chosen = "ark-doubao-seed"
	sortLaunchModels(claude, models, chosen)

	published, aliases, _ := claudeCatalogModels(models)
	if len(published) == 0 {
		t.Fatal("no models published")
	}
	if published[0].DisplayName != chosen {
		t.Fatalf("head of the published catalogue = %q (id %q), want the chosen model %q",
			published[0].DisplayName, published[0].ID, chosen)
	}
	if aliases[published[0].ID] != chosen {
		t.Fatalf("alias %q does not map back to %q", published[0].ID, chosen)
	}
}

// TestResolveRememberedModel covers the precedence and, more importantly, that every failure degrades to the catalogue default instead of aborting. claude and codex ship a built-in default and launched fine before any of this existed, so a catalogue blip or a non-interactive shell must not turn a working launch into an error.
func TestResolveRememberedModel(t *testing.T) {
	claude := tools.Registry["claude"]
	catalog := []api.RelayModel{
		{ID: "claude-opus-4-8", SupportedEndpointTypes: []string{"anthropic"}},
		{ID: "claude-sonnet-4-6", SupportedEndpointTypes: []string{"anthropic"}},
	}

	t.Run("explicit flag wins and is persisted", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		s := &config.Settings{}
		got, err := resolveRememberedModel(claude, s, catalog, "claude-sonnet-4-6", false, true)
		if err != nil || got != "claude-sonnet-4-6" {
			t.Fatalf("got (%q, %v)", got, err)
		}
		reloaded, err := config.LoadSettings()
		if err != nil {
			t.Fatal(err)
		}
		if reloaded.ToolModel("claude") != "claude-sonnet-4-6" {
			t.Fatalf("selection was not persisted: %#v", reloaded.ToolModels)
		}
	})

	t.Run("a flag naming an unavailable model is rejected", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		if _, err := resolveRememberedModel(claude, &config.Settings{}, catalog, "gpt-5", false, true); err == nil {
			t.Fatal("want an error naming the unavailable model")
		}
	})

	t.Run("a remembered model is reused without prompting", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		s := &config.Settings{ToolModels: map[string]string{"claude": "claude-sonnet-4-6"}}
		// interactive=true, yet no picker runs: the test would block if it did.
		got, err := resolveRememberedModel(claude, s, catalog, "", false, true)
		if err != nil || got != "claude-sonnet-4-6" {
			t.Fatalf("got (%q, %v)", got, err)
		}
	})

	t.Run("a remembered model the account lost is dropped", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		s := &config.Settings{ToolModels: map[string]string{"claude": "gone-from-the-account"}}
		got, err := resolveRememberedModel(claude, s, catalog, "", false, false)
		if err != nil || got != "" {
			t.Fatalf("got (%q, %v), want the catalogue default", got, err)
		}
		if remembered := s.ToolModel("claude"); remembered != "gone-from-the-account" {
			t.Fatalf("official Claude memory = %q, want fail-soft setting preserved", remembered)
		}
	})

	t.Run("non-interactive never prompts", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		got, err := resolveRememberedModel(claude, &config.Settings{}, catalog, "", false, false)
		if err != nil || got != "" {
			t.Fatalf("got (%q, %v), want a silent fall-through to the catalogue default", got, err)
		}
	})

	t.Run("a bare --model still cannot prompt non-interactively", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		s := &config.Settings{ToolModels: map[string]string{"claude": "claude-sonnet-4-6"}}
		// pickModel=true asks to re-choose, but there is nobody to ask; a scripted launch must keep the recorded model rather than block.
		got, err := resolveRememberedModel(claude, s, catalog, "", true, false)
		if err != nil || got != "claude-sonnet-4-6" {
			t.Fatalf("got (%q, %v)", got, err)
		}
	})

	t.Run("a save that fails does not abort the launch", func(t *testing.T) {
		// A plain FILE where the config directory should be, so SaveSettings's MkdirAll fails with ENOTDIR. Deliberately not a read-only directory: permission bits do not stop root, so that shape passes on a developer machine and then fails — or silently proves nothing — in a container CI running as root. ENOTDIR binds for every user.
		root := filepath.Join(t.TempDir(), "config-home-is-a-file")
		if err := os.WriteFile(root, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("XDG_CONFIG_HOME", root)

		got, err := resolveRememberedModel(claude, &config.Settings{}, catalog, "claude-opus-4-8", false, true)
		if err != nil {
			t.Fatalf("an unwritable settings file must not stop the launch: %v", err)
		}
		if got != "claude-opus-4-8" {
			t.Fatalf("got %q, want the selection to still apply to this launch", got)
		}
		// Proves the save really failed, so the test is exercising the degrade path rather than a chmod that silently did nothing.
		reloaded, loadErr := config.LoadSettings()
		if loadErr == nil && reloaded.ToolModel("claude") != "" {
			t.Fatal("settings were writable after all; this case never reached the failure path")
		}
	})

	t.Run("an empty catalogue is not fatal", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		got, err := resolveRememberedModel(claude, &config.Settings{}, nil, "", true, true)
		if err != nil || got != "" {
			t.Fatalf("got (%q, %v), want no error", got, err)
		}
	})

	t.Run("third-party explicit Claude is rejected even when it is the only route", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		opencode := tools.Registry["opencode"]
		claudeOnly := []api.RelayModel{{
			ID:                     "anthropic/claude",
			SupportedEndpointTypes: []string{"openai"},
		}}
		if _, err := resolveRememberedModel(
			opencode, &config.Settings{}, claudeOnly, "anthropic/claude", false, false,
		); err == nil {
			t.Fatal("explicit Claude selection was accepted after filtering emptied the catalog")
		}
	})

	t.Run("third-party explicit absent model is rejected when only Claude is routed", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		opencode := tools.Registry["opencode"]
		claudeOnly := []api.RelayModel{{
			ID:                     "claude-sonnet-5",
			SupportedEndpointTypes: []string{"openai"},
		}}
		if _, err := resolveRememberedModel(
			opencode, &config.Settings{}, claudeOnly, "gpt-5.6-terra", false, false,
		); err == nil {
			t.Fatal("model absent from an all-Claude catalog was accepted")
		}
	})

	t.Run("third-party remembered Claude is cleared even when it is the only route", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		opencode := tools.Registry["opencode"]
		s := &config.Settings{ToolModels: map[string]string{"opencode": "claude-sonnet-5"}}
		claudeOnly := []api.RelayModel{{
			ID:                     "claude-sonnet-5",
			SupportedEndpointTypes: []string{"openai"},
		}}
		got, err := resolveRememberedModel(opencode, s, claudeOnly, "", false, false)
		if err == nil || got != "" {
			t.Fatalf("got (%q, %v), want the disabled remembered model cleared and launch rejected", got, err)
		}
		if got := s.ToolModel("opencode"); got != "" {
			t.Fatalf("remembered Claude model still persisted in memory: %q", got)
		}
		reloaded, err := config.LoadSettings()
		if err != nil {
			t.Fatal(err)
		}
		if got := reloaded.ToolModel("opencode"); got != "" {
			t.Fatalf("remembered Claude model still persisted on disk: %q", got)
		}
	})

	t.Run("third-party stale non-Claude memory cannot bypass an all-Claude catalog", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		opencode := tools.Registry["opencode"]
		s := &config.Settings{ToolModels: map[string]string{"opencode": "gpt-5.6-terra"}}
		claudeOnly := []api.RelayModel{{
			ID:                     "claude-sonnet-5",
			SupportedEndpointTypes: []string{"openai"},
		}}
		if got, err := resolveRememberedModel(opencode, s, claudeOnly, "", false, false); err == nil || got != "" {
			t.Fatalf("got (%q, %v), want stale remembered model cleared and launch rejected", got, err)
		}
		if got := s.ToolModel("opencode"); got != "" {
			t.Fatalf("stale remembered model still persisted: %q", got)
		}
	})

	t.Run("OpenCode picker choices include Claude as unavailable", func(t *testing.T) {
		opencode := tools.Registry["opencode"]
		choices := modelPickerChoicesForTool([]api.RelayModel{
			{ID: "claude-sonnet-5", SupportedEndpointTypes: []string{"openai"}},
			{ID: "gpt-5.6-terra", SupportedEndpointTypes: []string{"openai-response"}},
		}, opencode)
		want := []toolModelChoice{
			{id: "claude-sonnet-5", unavailable: true},
			{id: "gpt-5.6-terra"},
		}
		if !reflect.DeepEqual(choices, want) {
			t.Fatalf("OpenCode choices = %#v, want %#v", choices, want)
		}
	})

	t.Run("third-party remembered model still opens the disabled picker", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		opencode := tools.Registry["opencode"]
		s := &config.Settings{ToolModels: map[string]string{"opencode": "gpt-5.6-terra"}}
		catalog := []api.RelayModel{
			{ID: "claude-sonnet-5", SupportedEndpointTypes: []string{"openai"}},
			{ID: "gpt-5.6-luna", SupportedEndpointTypes: []string{"openai-response"}},
			{ID: "gpt-5.6-terra", SupportedEndpointTypes: []string{"openai-response"}},
		}

		originalStdin := os.Stdin
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		os.Stdin = reader
		t.Cleanup(func() {
			os.Stdin = originalStdin
			_ = reader.Close()
		})
		if _, err := writer.WriteString("gpt-5.6-luna\n"); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}

		got, err := resolveRememberedModel(opencode, s, catalog, "", false, true)
		if err != nil {
			t.Fatal(err)
		}
		if got != "gpt-5.6-luna" {
			t.Fatalf("selected model = %q, want the new picker choice", got)
		}
	})

	t.Run("third-party picker cancellation stops launch", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		opencode := tools.Registry["opencode"]
		s := &config.Settings{ToolModels: map[string]string{"opencode": "gpt-5.6-terra"}}
		catalog := []api.RelayModel{{
			ID:                     "gpt-5.6-terra",
			SupportedEndpointTypes: []string{"openai-response"},
		}}

		originalStdin := os.Stdin
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		os.Stdin = reader
		t.Cleanup(func() {
			os.Stdin = originalStdin
			_ = reader.Close()
		})
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}

		if _, err := resolveRememberedModel(opencode, s, catalog, "", false, true); err == nil {
			t.Fatal("cancelled mandatory picker still allowed OpenCode to launch")
		}
	})

	t.Run("third-party all-Claude catalog is shown then rejected", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		opencode := tools.Registry["opencode"]
		catalog := []api.RelayModel{
			{ID: "claude-opus-5", SupportedEndpointTypes: []string{"openai"}},
			{ID: "claude-sonnet-5", SupportedEndpointTypes: []string{"openai-response"}},
		}
		if _, err := resolveRememberedModel(opencode, &config.Settings{}, catalog, "", false, true); !errors.Is(err, cliprompt.ErrPickUnavailable) {
			t.Fatalf("all-Claude OpenCode catalog error = %v, want ErrPickUnavailable", err)
		}
	})
}

func TestManagedBootModelArgs(t *testing.T) {
	grok := tools.Registry["grok"]
	if got, want := managedBootModelArgs(grok, []string{"chat"}, "gpt-5.6-terra", true), []string{"--model", "gpt-5.6-terra", "chat"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("managedBootModelArgs(grok) = %v, want %v", got, want)
	}
	opencode := tools.Registry["opencode"]
	if got, want := managedBootModelArgs(opencode, []string{"run"}, "gpt-5.6-terra", true), []string{"run"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("managedBootModelArgs(opencode) = %v, want %v", got, want)
	}
}

// Claude Code boots on its own built-in default unless told otherwise, so a remembered selection that only reorders the served catalogue leaves the first request naming a model the relay key may not be able to route.
func TestManagedBootModelArgsClaude(t *testing.T) {
	claude := tools.Registry["claude"]

	t.Run("pins the remembered model", func(t *testing.T) {
		got := managedBootModelArgs(claude, nil, "deepseek-v4-flash", true)
		want := []string{"--model", "deepseek-v4-flash"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("managedBootModelArgs(claude) = %v, want %v", got, want)
		}
	})

	t.Run("marks an Opus boot model for 1M context", func(t *testing.T) {
		// The context window a launch runs at is decided here: Claude Code reads the 1M beta off the model string it boots on, so an unmarked Opus id pins the session to 200K. Non-Opus families must stay unmarked — their upstreams hard-reject the flag.
		for boot, want := range map[string]string{
			"claude-opus-5":     "claude-opus-5[1m]",
			"claude-opus-4-8":   "claude-opus-4-8[1m]",
			"claude-sonnet-5":   "claude-sonnet-5",
			"claude-haiku-4-5":  "claude-haiku-4-5",
			"deepseek-v4-flash": "deepseek-v4-flash",
		} {
			got := managedBootModelArgs(claude, nil, boot, true)
			if !reflect.DeepEqual(got, []string{"--model", want}) {
				t.Errorf("managedBootModelArgs(claude, %q) = %v, want [--model %s]", boot, got, want)
			}
		}
	})

	t.Run("claude_long_context=false leaves every boot model unmarked", func(t *testing.T) {
		// The escape hatch has to reach the argv, because that is the only place the marker is applied. A relay key whose Anthropic channel is not enabled for the beta rejects every request in the session, so "off" must produce exactly the pre-change argument.
		for _, boot := range []string{"claude-opus-5", "claude-opus-4-8", "claude-sonnet-5", "deepseek-v4-flash"} {
			got := managedBootModelArgs(claude, nil, boot, false)
			if !reflect.DeepEqual(got, []string{"--model", boot}) {
				t.Errorf("managedBootModelArgs(claude, %q, longContext=false) = %v, want [--model %s]", boot, got, boot)
			}
		}
	})

	t.Run("passes the real upstream id, not the catalogue alias", func(t *testing.T) {
		// claudeCatalogModels republishes non-claude ids under a synthetic alias; the alias is only resolvable while the catalogue transform is hosted, so the boot argument must stay the real id.
		_, aliases, _ := claudeCatalogModels([]tools.Model{{ID: "deepseek-v4-flash"}})
		got := managedBootModelArgs(claude, nil, "deepseek-v4-flash", true)
		for _, arg := range got {
			if _, isAlias := aliases[arg]; isAlias {
				t.Fatalf("boot args %v carry a synthetic alias; want the real upstream id", got)
			}
		}
	})

	t.Run("keeps a caller-supplied --model authoritative", func(t *testing.T) {
		for _, args := range [][]string{
			{"--model", "claude-haiku-4-5"},
			{"--model=claude-haiku-4-5"},
		} {
			got := managedBootModelArgs(claude, args, "deepseek-v4-flash", true)
			if !reflect.DeepEqual(got, args) {
				t.Fatalf("managedBootModelArgs(claude, %v) = %v, want it unchanged", args, got)
			}
		}
	})

	t.Run("leaves metadata-only invocations alone", func(t *testing.T) {
		for _, args := range [][]string{{"--help"}, {"-h"}, {"help"}, {"--version"}, {"-v"}, {"version"}} {
			got := managedBootModelArgs(claude, args, "deepseek-v4-flash", true)
			if !reflect.DeepEqual(got, args) {
				t.Fatalf("managedBootModelArgs(claude, %v) = %v, want it unchanged", args, got)
			}
		}
	})

	t.Run("no selection means no argument", func(t *testing.T) {
		if got := managedBootModelArgs(claude, []string{"chat"}, "", true); !reflect.DeepEqual(got, []string{"chat"}) {
			t.Fatalf("managedBootModelArgs(claude) with no boot model = %v, want unchanged", got)
		}
	})
}

// usageToolNames parses the tool names out of the ARGUMENTS block of `everyapi use --help`.
//
// It reads the block rather than substring-matching each name, because substring matching cannot fail: "pi" is inside both "copilot" and "pi-web", so a naive check would pass on a help text that never mentions pi at all.
func usageToolNames(t *testing.T) map[string]bool {
	t.Helper()
	_, after, found := strings.Cut(useUsage, "ARGUMENTS\n")
	if !found {
		t.Fatal("useUsage has no ARGUMENTS block")
	}
	block, _, found := strings.Cut(after, "Omit to open an interactive picker")
	if !found {
		t.Fatal("the ARGUMENTS block has no terminator")
	}
	names := map[string]bool{}
	for _, field := range strings.Fields(strings.ReplaceAll(block, "|", " ")) {
		if field == "<tool>" {
			continue
		}
		names[field] = true
	}
	if len(names) == 0 {
		t.Fatal("parsed no tool names out of the ARGUMENTS block")
	}
	return names
}

// TestUseUsageListsEveryRegisteredTool pins the help text to the registry.
//
// The list in useUsage is a hand-maintained literal, so every tool added to the registry since it was written had to be copied here by hand — and deepseek-harness was not, staying invisible in `everyapi use --help` for as long as it shipped. Registering a tool is meant to be a single map entry; this makes the help text part of what that entry is checked against instead of a second place to remember.
func TestUseUsageListsEveryRegisteredTool(t *testing.T) {
	t.Parallel()
	listed := usageToolNames(t)
	for _, name := range tools.Names() {
		if !listed[name] {
			t.Errorf("tool %q is registered but missing from `everyapi use --help`", name)
		}
	}
}

// TestUseUsageListsNoUnregisteredTool is the other direction: a tool removed or renamed in the registry must not linger in the help as an option that errors when the user types it.
func TestUseUsageListsNoUnregisteredTool(t *testing.T) {
	t.Parallel()
	registered := map[string]bool{}
	for _, name := range tools.Names() {
		registered[name] = true
	}
	for name := range usageToolNames(t) {
		if !registered[name] {
			t.Errorf("`everyapi use --help` offers %q, which is not a registered tool", name)
		}
	}
}
