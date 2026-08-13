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

	"github.com/everyapi-ai/everyapi-ai/internal/tools"
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

		// --model: pin the upstream model (hermes). Space + eq forms;
		// a valueless --model is an error (omit it to get the picker).
		{"model space value", []string{"hermes", "--model", "gpt-5.1"}, "hermes", "", false, false, nil, "gpt-5.1", false, false},
		{"model eq value", []string{"hermes", "--model=gpt-5.1"}, "hermes", "", false, false, nil, "gpt-5.1", false, false},
		{"model before tool", []string{"--model", "claude-sonnet-4-6", "hermes"}, "hermes", "", false, false, nil, "claude-sonnet-4-6", false, false},
		{"model + channel", []string{"hermes", "--channel=byteplus", "--model", "gpt-5.1"}, "hermes", "byteplus", false, false, nil, "gpt-5.1", false, false},
		{"bare model opens the picker", []string{"hermes", "--model"}, "hermes", "", false, false, nil, "", true, false},
		{"empty eq model → error", []string{"hermes", "--model="}, "", "", false, false, nil, "", false, true},
		{"bare model still lets the next flag parse", []string{"hermes", "--model", "--sanitize"}, "hermes", "", false, true, nil, "", true, false},
		{"model space value won't eat a not-yet-seen tool name", []string{"--model", "claude"}, "claude", "", false, false, nil, "", true, false},
		{"model value after tool may be a tool-named id", []string{"hermes", "--model", "claude"}, "hermes", "", false, false, nil, "claude", false, false},

		// `--` separator: end of everyapi's option parsing, everything
		// after is forwarded raw to the tool. The documented escape
		// hatch for tool flags that collide with everyapi's, e.g.
		// claude's `--dangerously-skip-permissions` and codex's
		// `--dangerously-bypass-approvals-and-sandbox`.
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
	// wantTransparent is the tri-state the parser reports: nil = the user said
	// nothing (Use then applies the per-tool default), non-nil = an explicit
	// request. "unset" and "explicitly false" are NOT interchangeable now that
	// transparent is the default — unset falls back silently on a tool with no
	// adapter, explicit-true errors there.
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

// TestParseUseArgsTransparentDoesNotEatValueTokens pins that a bare value
// literally spelled "transparent" — a routing group, a --model value, or the
// tool positional — is never mistaken for the --transparent flag. Only a
// dash-prefixed token toggles the connector; a group named "transparent" must
// still select that group (and keep the experimental MITM mode off).
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
	// Transparent is the default now, so the help must say so and must offer
	// the opt-out — a user whose tool breaks needs to find the escape hatch in
	// `everyapi use --help`, not in the source.
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

func TestUseUsageDocumentsGrok(t *testing.T) {
	if !strings.Contains(useUsage, "grok") {
		t.Fatal("use help does not list grok")
	}
	if !strings.Contains(useUsage, "everyapi use grok") {
		t.Fatal("use help does not include a Grok launch example")
	}
}

func TestUseUsageDocumentsOfficialQwenAndKimiCLIs(t *testing.T) {
	for _, name := range []string{"qwen-code", "kimi-code"} {
		if !strings.Contains(useUsage, name) {
			t.Errorf("use help does not list %s", name)
		}
	}
	if !strings.Contains(useUsage, "hermes/qwen-code/kimi-code") {
		t.Error("use help does not document model selection for the new CLIs")
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

func TestToolArgsForLaunchMakesBareCodexResumeGlobal(t *testing.T) {
	codex, err := tools.Lookup("codex")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"bare picker", []string{"resume"}, []string{"resume", "--all"}},
		{"explicit all", []string{"resume", "--all"}, []string{"resume", "--all"}},
		{"last session", []string{"resume", "--last"}, []string{"resume", "--last"}},
		{"specific session", []string{"resume", "session-id"}, []string{"resume", "session-id"}},
		{"ordinary launch", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := toolArgsForLaunch(codex, tc.args); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("toolArgsForLaunch() = %v, want %v", got, tc.want)
			}
		})
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

// TestParseUseArgsAcceptsSanitizeWithTransparent checks only that the parser
// accepts the two flags together. It deliberately does NOT stand in for the
// removed TestUseRejectsSanitizeAndTransparentTogether: that gate lived in Use,
// never in the parser, so a parser-level test passes identically with or
// without the change and pins nothing about it. The real behavioral
// replacement — that the two now compose into child -> connector -> sanitizer
// -> gateway rather than erroring — is
// TestUseWiresTheSanitizerAsTheConnectorUpstream, which drives Use end to end
// and fails if Use stops pointing the connector at the sanitizer.
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
		// Fields deliberately leaves arbitrary Unicode and control bytes inside
		// tokens while bounding argv growth for the fuzzer.
		args := strings.Fields(input)
		if len(args) > 64 {
			args = args[:64]
		}
		_, _, _, _, _, _, _, _ = parseUseArgs(args)
	})
}

// TestResolveToolModel covers the non-interactive branches of the
// model-precedence chain (flag > env > picker > default). The picker
// branch needs a TTY + network and is exercised manually; here we pin
// that the flag wins, a pre-set env is respected, and a tool without
// ModelEnv is a clean no-op.
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

	t.Run("no flag, no env, non-interactive resolves from live catalog", func(t *testing.T) {
		t.Setenv(env, "")
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/models" {
				http.NotFound(w, r)
				return
			}
			_, _ = io.WriteString(w, `{"data":[`+
				`{"id":"z-image","supported_endpoint_types":["image-generation"]},`+
				`{"id":"b-openai-chat","supported_endpoint_types":["openai"]},`+
				`{"id":"a-anthropic-only","supported_endpoint_types":["anthropic"]},`+
				`{"id":"aa-responses-only","supported_endpoint_types":["openai-response"]}`+
				`]}`)
		}))
		defer srv.Close()
		catalogCreds := &config.Credentials{APIBase: srv.URL}
		if err := resolveToolModel(hermes, catalogCreds, "relay-key", ""); err != nil {
			t.Fatal(err)
		}
		if got := os.Getenv(env); got != "b-openai-chat" {
			t.Errorf("%s = %q, want first model compatible with hermes' chat_completions transport", env, got)
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
	models := []string{"a-first-model", "z-hermes-history"}
	if got := preferredToolModelIndex(qwen, models); got != 0 {
		t.Errorf("Qwen initial index = %d, want first model", got)
	}
	if got := preferredToolModelIndex(hermes, models); got != 1 {
		t.Errorf("Hermes initial index = %d, want saved model", got)
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

// The gateway rejects a create that names the auto group without the tier
// grant, so an account that cannot use it must still get its first key — the
// rejected create retries once without a group.
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
		// The bug the helper exists to prevent: a routing group
		// literally named "help" must not be intercepted.
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
		{ID: "gpt-chat", SupportedEndpointTypes: []string{"openai"}},
		{ID: "gpt-5.6-terra", SupportedEndpointTypes: []string{"openai-response"}},
		{ID: "claude-only", SupportedEndpointTypes: []string{"anthropic"}},
	}
	opencode, _ := tools.Lookup("opencode")
	got := launchModelsForTool(opencode, catalog, "")
	if len(got) != 2 || got[0].ID != "gpt-5.6-terra" || got[1].ID != "gpt-chat" {
		t.Fatalf("OpenCode launch catalog = %#v", got)
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

// TestTransparentTopologyReversesCreationOrder pins the direction of the launch
// line. Hops are collected as they are built, each pointing at the previous one
// as its upstream, so traffic runs the other way and the first hop built prints
// last. Emitted in creation order the line stays plausible-looking while naming
// the chain backwards.
//
// Launches currently build at most one hop, so the multi-hop cases here are
// ahead of the code on purpose: they are what makes the invariant survive
// someone adding a second hop later.
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

// fmtBoolPtr renders the parser's tri-state readably in failure messages, so an
// "unset vs explicitly false" mismatch is obvious rather than printing as a
// bare pointer address.
func fmtBoolPtr(b *bool) string {
	if b == nil {
		return "unset"
	}
	if *b {
		return "true"
	}
	return "false"
}

// TestSortLaunchModelsPutsTheChoiceFirst pins the ordering that decides what a
// self-selecting client boots on. Plain alphabetical order made position 0 an
// accident of the id: an account holding "ark-…" models had claude boot on one
// purely because 'a' sorts before 'c'.
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

	// grok is served a filtered catalogue and boots on its head too, so it gets
	// the same native tier — without it an unrelated vendor's model sits in
	// front of grok's own.
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

// TestSortSurvivesClaudeAliasing pins the load-bearing link between the two
// halves of this feature. The chain is sortLaunchModels -> claudeCatalogModels
// -> the catalogue proxy's /v1/models response, and the remembered model only
// reaches claude at position 0 if the aliasing step preserves input order.
//
// It does today because claudeCatalogModels is a range-and-append. Nothing
// asserted it, so adding e.g. a "claude-* first" pass inside that function
// would silently break the guarantee with every other test still green.
func TestSortSurvivesClaudeAliasing(t *testing.T) {
	claude := tools.Registry["claude"]
	models := []tools.Model{
		{ID: "claude-opus-4-8"}, {ID: "ark-doubao-seed"}, {ID: "zzz-last"},
	}
	// A non-claude id is the interesting case: it is the one that gets
	// republished under a synthetic alias, so its identity survives only in
	// DisplayName.
	const chosen = "ark-doubao-seed"
	sortLaunchModels(claude, models, chosen)

	published, aliases := claudeCatalogModels(models)
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

// TestResolveRememberedModel covers the precedence and, more importantly, that
// every failure degrades to the catalogue default instead of aborting. claude
// and codex ship a built-in default and launched fine before any of this
// existed, so a catalogue blip or a non-interactive shell must not turn a
// working launch into an error.
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
		// pickModel=true asks to re-choose, but there is nobody to ask; a
		// scripted launch must keep the recorded model rather than block.
		got, err := resolveRememberedModel(claude, s, catalog, "", true, false)
		if err != nil || got != "claude-sonnet-4-6" {
			t.Fatalf("got (%q, %v)", got, err)
		}
	})

	t.Run("a save that fails does not abort the launch", func(t *testing.T) {
		// A plain FILE where the config directory should be, so SaveSettings's
		// MkdirAll fails with ENOTDIR. Deliberately not a read-only directory:
		// permission bits do not stop root, so that shape passes on a developer
		// machine and then fails — or silently proves nothing — in a container
		// CI running as root. ENOTDIR binds for every user.
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
		// Proves the save really failed, so the test is exercising the
		// degrade path rather than a chmod that silently did nothing.
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
}
