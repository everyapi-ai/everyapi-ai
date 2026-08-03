package cmd

import (
	"encoding/json"
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
		name         string
		args         []string
		wantTool     string
		wantGroup    string
		wantPick     bool
		wantSanitize bool
		wantExtra    []string
		wantModel    string
		wantErr      bool
	}{
		{"bare tool", []string{"claude"}, "claude", "", false, false, nil, "", false},
		{"no args", nil, "", "", false, false, nil, "", false},
		{"tool then bare channel → picker", []string{"claude", "--channel"}, "claude", "", true, false, nil, "", false},
		{"tool then bare group → picker", []string{"claude", "--group"}, "claude", "", true, false, nil, "", false},
		{"bare channel only → picker, no tool", []string{"--channel"}, "", "", true, false, nil, "", false},
		{"space value after tool", []string{"claude", "--channel", "byteplus"}, "claude", "byteplus", false, false, nil, "", false},
		{"space value before tool", []string{"--channel", "team-a", "claude"}, "claude", "team-a", false, false, nil, "", false},
		{"group alias space value", []string{"claude", "--group", "byteplus"}, "claude", "byteplus", false, false, nil, "", false},
		{"group and channel aliases conflict", []string{"claude", "--group", "a", "--channel", "b"}, "", "", false, false, nil, "", true},
		{"eq value", []string{"claude", "--channel=byteplus"}, "claude", "byteplus", false, false, nil, "", false},
		{"empty eq → picker", []string{"claude", "--channel="}, "claude", "", true, false, nil, "", false},
		{"single dash space value", []string{"-channel", "team-a", "codex"}, "codex", "team-a", false, false, nil, "", false},
		{"value position is known tool, no tool yet → it's the tool, picker", []string{"--channel", "claude"}, "claude", "", true, false, nil, "", false},
		{"next token is a flag → picker", []string{"claude", "--channel", "--sanitize"}, "claude", "", true, true, nil, "", false},
		{"--direct is no longer a flag → error", []string{"claude", "--direct"}, "", "", false, false, nil, "", true},
		{"--sanitize opts in", []string{"claude", "--sanitize"}, "claude", "", false, true, nil, "", false},
		{"--sanitize + group", []string{"claude", "--sanitize", "--channel=byteplus"}, "claude", "byteplus", false, true, nil, "", false},
		{"--sanitize=true enables", []string{"claude", "--sanitize=true"}, "claude", "", false, true, nil, "", false},
		{"--sanitize=false disables (not the inverse)", []string{"claude", "--sanitize=false"}, "claude", "", false, false, nil, "", false},
		{"--sanitize=0 disables", []string{"claude", "--sanitize=0"}, "claude", "", false, false, nil, "", false},
		{"--sanitize=garbage → error", []string{"claude", "--sanitize=nope"}, "", "", false, false, nil, "", true},
		{"two positionals → error", []string{"claude", "extra"}, "", "", false, false, nil, "", true},
		{"unknown flag → error", []string{"claude", "--bogus"}, "", "", false, false, nil, "", true},
		{"ambiguous: group named like a tool before tool → error", []string{"--group", "codex", "claude"}, "", "", false, false, nil, "", true},
		{"preset name as group before tool is also ambiguous → error", []string{"--group", "byteplus", "claude"}, "", "", false, false, nil, "", true},
		{"eq form lets a tool-named group through", []string{"--channel=codex", "claude"}, "claude", "codex", false, false, nil, "", false},
		{"eq form lets a preset-named group through", []string{"--channel=byteplus", "claude"}, "claude", "byteplus", false, false, nil, "", false},

		// --model: pin the upstream model (hermes). Space + eq forms;
		// a valueless --model is an error (omit it to get the picker).
		{"model space value", []string{"hermes", "--model", "gpt-5.1"}, "hermes", "", false, false, nil, "gpt-5.1", false},
		{"model eq value", []string{"hermes", "--model=gpt-5.1"}, "hermes", "", false, false, nil, "gpt-5.1", false},
		{"model before tool", []string{"--model", "claude-sonnet-4-6", "hermes"}, "hermes", "", false, false, nil, "claude-sonnet-4-6", false},
		{"model + channel", []string{"hermes", "--channel=byteplus", "--model", "gpt-5.1"}, "hermes", "byteplus", false, false, nil, "gpt-5.1", false},
		{"bare model → error", []string{"hermes", "--model"}, "", "", false, false, nil, "", true},
		{"empty eq model → error", []string{"hermes", "--model="}, "", "", false, false, nil, "", true},
		{"model value missing, next is flag → error", []string{"hermes", "--model", "--sanitize"}, "", "", false, false, nil, "", true},
		{"model space value won't eat a not-yet-seen tool name → error", []string{"--model", "claude"}, "", "", false, false, nil, "", true},
		{"model value after tool may be a tool-named id", []string{"hermes", "--model", "claude"}, "hermes", "", false, false, nil, "claude", false},

		// `--` separator: end of everyapi's option parsing, everything
		// after is forwarded raw to the tool. The documented escape
		// hatch for tool flags that collide with everyapi's, e.g.
		// claude's `--dangerously-skip-permissions` and codex's
		// `--dangerously-bypass-approvals-and-sandbox`.
		{"-- forwards a single flag", []string{"claude", "--", "--dangerously-skip-permissions"}, "claude", "", false, false, []string{"--dangerously-skip-permissions"}, "", false},
		{"-- forwards multiple tokens verbatim", []string{"claude", "--", "--model", "opus", "prompt text"}, "claude", "", false, false, []string{"--model", "opus", "prompt text"}, "", false},
		{"-- combined with --channel before tool", []string{"--channel", "team-a", "claude", "--", "--dangerously-skip-permissions"}, "claude", "team-a", false, false, []string{"--dangerously-skip-permissions"}, "", false},
		{"-- combined with --channel after tool", []string{"claude", "--channel=byteplus", "--", "--dangerously-skip-permissions"}, "claude", "byteplus", false, false, []string{"--dangerously-skip-permissions"}, "", false},
		{"bare -- with no following args", []string{"claude", "--"}, "claude", "", false, false, nil, "", false},
		{"-- shields what would otherwise be a everyapi flag", []string{"claude", "--", "--group", "byteplus"}, "claude", "", false, false, []string{"--group", "byteplus"}, "", false},
		{"-- shields what would otherwise be a second positional", []string{"claude", "--", "extra"}, "claude", "", false, false, []string{"extra"}, "", false},
		{"-- shields --model too", []string{"hermes", "--", "--model", "x"}, "hermes", "", false, false, []string{"--model", "x"}, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tool, group, pick, sanitize, extra, model, err := parseUseArgs(c.args)
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
			tool, _, _, _, transparent, extra, _, err := parseUseArgsWithTransparent(c.args)
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
		tool, group, pick, _, transparent, _, _, err := parseUseArgsWithTransparent(
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
		tool, _, _, _, transparent, _, model, err := parseUseArgsWithTransparent(
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
		tool, _, _, _, transparent, _, _, err := parseUseArgsWithTransparent(
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
	tool, _, _, sanitize, transparent, _, _, err := parseUseArgsWithTransparent(
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
		_, _, _, _, _, _, _ = parseUseArgs(args)
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
					{"id": 7, "name": gotCreate.Name, "status": api.TokenStatusEnabled, "group": ""},
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
	if gotCreate.Group != "" {
		t.Fatalf("created token group = %q, want default group", gotCreate.Group)
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

// TestProviderChatModels covers the preset picker filter: scope to one
// provider's owned_by (case-insensitive), drop non-chat models
// (image/embeddings/rerank/video) the launched claude can't drive, keep
// models with no declared endpoint types (fail-open), and sort.
func TestProviderChatModels(t *testing.T) {
	catalog := []api.RelayModel{
		{ID: "MiniMax-M2.7-highspeed", OwnedBy: "minimax", SupportedEndpointTypes: []string{"anthropic", "openai"}},
		{ID: "MiniMax-M2.7", OwnedBy: "MiniMax", SupportedEndpointTypes: []string{"openai"}},                            // owner case-insensitive
		{ID: "speech-02", OwnedBy: "minimax", SupportedEndpointTypes: []string{"image-generation"}},                     // non-chat → drop
		{ID: "image-01", OwnedBy: "minimax", SupportedEndpointTypes: []string{"embeddings"}},                            // non-chat → drop
		{ID: "image-openai-bridge", OwnedBy: "minimax", SupportedEndpointTypes: []string{"image-generation", "openai"}}, // OpenAI image protocol, still not chat → drop
		{ID: "legacy-chat", OwnedBy: "minimax", SupportedEndpointTypes: nil},                                            // unknown → keep
		{ID: "glm-5.1", OwnedBy: "zhipu_4v", SupportedEndpointTypes: []string{"anthropic"}},                             // other provider → drop
	}
	got := providerChatModels(catalog, "minimax")
	want := []string{"MiniMax-M2.7", "MiniMax-M2.7-highspeed", "legacy-chat"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("providerChatModels = %v, want %v (sorted, chat-only, owner-scoped)", got, want)
	}

	// No models for an owner with only non-chat or no entries → empty,
	// which the caller turns into the actionable "no models" error.
	if got := providerChatModels(catalog, "moonshot"); len(got) != 0 {
		t.Errorf("providerChatModels(moonshot) = %v, want empty", got)
	}
	onlyImage := []api.RelayModel{{ID: "img", OwnedBy: "minimax", SupportedEndpointTypes: []string{"image-generation"}}}
	if got := providerChatModels(onlyImage, "minimax"); len(got) != 0 {
		t.Errorf("providerChatModels(only-image) = %v, want empty", got)
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

func TestValidateClaudePresetModel(t *testing.T) {
	tool, err := tools.Lookup("kimi")
	if err != nil {
		t.Fatal(err)
	}
	catalog := []api.RelayModel{
		{ID: "kimi-k2.5", OwnedBy: "moonshot", SupportedEndpointTypes: []string{"anthropic"}},
		{ID: "other-model", OwnedBy: "deepseek", SupportedEndpointTypes: []string{"anthropic"}},
	}
	if err := validateClaudePresetModel(tool, catalog, "kimi-k2.5"); err != nil {
		t.Fatalf("reachable provider model rejected: %v", err)
	}
	if err := validateClaudePresetModel(tool, catalog, "missing-model"); err == nil {
		t.Fatal("unreachable explicit model accepted")
	}
	if err := validateClaudePresetModel(tool, catalog, "other-model"); err == nil {
		t.Fatal("model owned by another preset accepted")
	}
}

// TestProviderChatModels_SanitizesTerminalEscapes proves a server-supplied
// model ID carrying an embedded ANSI/control escape sequence is stripped
// before it reaches the interactive picker (cliout.Sanitize), matching the
// convention used everywhere else in the CLI for backend-relayed strings —
// otherwise a malicious/compromised catalog entry could manipulate the
// terminal (hide/relabel entries, move the cursor) while the user is
// choosing which model to route their session through.
func TestProviderChatModels_SanitizesTerminalEscapes(t *testing.T) {
	catalog := []api.RelayModel{
		{ID: "evil\x1b[31mmodel", OwnedBy: "minimax", SupportedEndpointTypes: []string{"anthropic"}},
	}
	got := providerChatModels(catalog, "minimax")
	want := []string{"evilmodel"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("providerChatModels = %q, want %q (ANSI escape stripped)", got, want)
	}
}

// TestProviderChatModelsLegacyOwnerAlias verifies the glm/qwen presets
// match BOTH the new brand owned_by (zhipu/qwen) and the legacy
// channel-adaptor name (zhipu_4v/ali) an un-upgraded gateway still emits,
// so `use glm`/`use qwen` survive the rollout in either order.
func TestProviderChatModelsLegacyOwnerAlias(t *testing.T) {
	mixed := []api.RelayModel{
		{ID: "glm-4.7", OwnedBy: "zhipu", SupportedEndpointTypes: []string{"anthropic"}},       // new gateway
		{ID: "glm-5.1", OwnedBy: "zhipu_4v", SupportedEndpointTypes: []string{"anthropic"}},    // legacy gateway
		{ID: "qwen-max", OwnedBy: "qwen", SupportedEndpointTypes: []string{"openai"}},          // new gateway
		{ID: "qwen3-coder", OwnedBy: "ali", SupportedEndpointTypes: []string{"openai"}},        // legacy gateway
		{ID: "deepseek-chat", OwnedBy: "deepseek", SupportedEndpointTypes: []string{"openai"}}, // unrelated
	}
	if got, want := providerChatModels(mixed, "zhipu"), []string{"glm-4.7", "glm-5.1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("providerChatModels(zhipu) = %v, want %v (brand + legacy zhipu_4v)", got, want)
	}
	if got, want := providerChatModels(mixed, "qwen"), []string{"qwen-max", "qwen3-coder"}; !reflect.DeepEqual(got, want) {
		t.Errorf("providerChatModels(qwen) = %v, want %v (brand + legacy ali)", got, want)
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
