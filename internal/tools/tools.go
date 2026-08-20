// Package tools is the registry of third-party CLIs that `everyapi use` knows how to point at EveryAPI. Adding a tool here is a single map entry — no changes elsewhere.
//
// Each entry describes:
//   - ExecName: the binary that gets exec'd (looked up in $PATH)
//   - Env: the environment variables to set so the tool talks to the EveryAPI gateway. URLs are computed at runtime by appending the tool-specific suffix to the user's configured API base, so a local-dev base (http://localhost:8787) works without per-tool env edits.
//   - InstallHint: copy printed when ExecName isn't on PATH
//
// The env-var conventions are read straight off each tool's docs (see the comment on the entry). When upstream renames a variable we update one line here.
package tools

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// Tool describes how to launch one third-party CLI against EveryAPI.
type Tool struct {
	Name        string
	ExecName    string
	InstallHint string
	// DefaultArgs are used only when the caller supplied no tool arguments. Keep these compile-time literals: they reach exec directly, without a shell. An explicit argument list always wins.
	DefaultArgs []string

	// Native launches a client that owns its own authentication and upstream routing. Use must not resolve or expose an EveryAPI relay key for it.
	Native bool

	// InstallCmd is the shell command 'everyapi use' offers to run on the user's behalf when ExecName isn't on $PATH. Executed via `sh -c` on Unix and `cmd /C` on Windows; an empty value disables the auto-install prompt and the user falls back to reading InstallHint and running the installer themselves. For tools whose canonical installer is Unix-only (e.g. a `curl | bash` script), InstallCmdUnixOnly should be true so Windows users see the hint instead of a guaranteed-to-fail shell pipeline.
	//
	// SECURITY INVARIANT: this string MUST be a compile-time literal embedded in the Registry below. It is passed verbatim to `sh -c` / `cmd /C` with no escaping — sourcing it from user input, env vars, config files, or any network response would be RCE. If you find yourself wanting to make this dynamic, design a per-tool installer function instead.
	InstallCmd string
	// InstallCmdWindows overrides InstallCmd on Windows when the vendor publishes a separate native installer command. The first element is the executable and the rest are passed as argv directly, never through cmd.exe. It has the same security invariant as InstallCmd and every element must remain a compile-time literal.
	InstallCmdWindows []string
	// InstallCmdUnixOnly gates InstallCmd off on Windows when the command relies on a POSIX-only pipeline (curl | bash, etc.). Doubles as the "this installer is less reversible than `npm install -g`" signal: prompt callers default to N when this is true, so a single press of Enter never runs a remote shell script on the user's machine.
	InstallCmdUnixOnly bool

	// ExtraBinDirs lists installer-specific directories to search for ExecName when $PATH misses, as $HOME-relative paths ("" segments and absolute paths are ignored). The npm-global fallback in resolveExecDirs only covers `npm install -g` tools; an installer that writes somewhere else — Antigravity's install.sh drops `agy` into ~/.local/bin — needs its own candidates, or a user whose shell adds that dir in an rc file everyapi never sources stays permanently "not installed" and loops on reinstall offers.
	//
	// Same contract as InstallCmd: compile-time literals only. These paths are joined onto the user's home dir and stat'd, never executed as shell text, but keeping them static preserves the "no user input reaches tool resolution" property.
	ExtraBinDirs []string
	// WindowsLocalAppDataBinDirs lists installer-owned directories beneath %LOCALAPPDATA%. It closes the same install-then-resolve loop as ExtraBinDirs for Windows installers that use the platform convention.
	WindowsLocalAppDataBinDirs []string

	// YoloFlag is the tool-specific "skip every confirmation" argument the user might want to pass — claude's --dangerously-skip-permissions, codex's --dangerously-bypass-approvals-and-sandbox, gemini's --yolo. 'everyapi use' offers the flag via a TTY confirm prompt before exec so the user can opt in without having to remember the exact string. Empty for tools where no such blanket-bypass flag exists.
	YoloFlag string
	// YoloLabel is the human-readable name shown in the prompt: "Enable <YoloLabel>? [y/N]". Should be short and reflect what the user gets — "skip permission prompts (claude)" / "bypass approvals + sandbox (codex)" / "yolo mode (gemini)".
	YoloLabel string
	// YoloEnv is the env-var form of the blanket-bypass switch, for tools whose "skip every confirmation" mode is toggled by an environment variable rather than an argv flag — hermes reads HERMES_YOLO_MODE=1 and exposes no equivalent command-line flag. When set and the user opts in at the prompt, 'everyapi use' puts this var = "1" in the tool's env instead of (or in addition to) prepending YoloFlag. A tool may set YoloFlag, YoloEnv, or both; empty when the tool has no blanket-bypass switch at all.
	YoloEnv string

	// FlagProbeArgs is an argv tail that makes this tool parse its command line, report the result through its exit code, and then do nothing else — codex's `exec --help` prints usage and exits 0 without starting a session, reading stdin, or firing hooks.
	//
	// It enables FlagProbe (see flagprobe.go), which runs the tool once with a candidate flag prepended to decide whether adding that flag would abort the launch. That question only has an empirical answer: the binary on $PATH may be a wrapper that prepends flags of its own, and a flag the tool declares non-repeatable then arrives twice.
	//
	// SAFETY CONTRACT: these args must be observably inert — no session, no stdin read, no network, no writes, no hooks. They run on every launch of a tool that declares them. Same compile-time-literal rule as InstallCmd and ExtraBinDirs. Empty disables probing entirely, which means every flag EveryAPI wants to add is added unexamined.
	FlagProbeArgs []string

	// ModelEnv names the env var the tool's prepareFn reads to pin the upstream model. Set only for tools that don't carry their own vendor-default model and therefore need EveryAPI to choose one — Hermes reads EVERYAPI_HERMES_MODEL, Qwen Code reads OPENAI_MODEL, Gemini, Aider, Goose, Crush, Cline, and OpenClaw use the same contract through their preparation hooks. When non-empty, 'everyapi use' offers a model picker (populated from the gateway's model catalog) before launch and honors a `--model <id>` flag. Empty for claude/codex/grok, whose own CLIs default the model and route it by name through the gateway.
	ModelEnv string

	// RequiredEndpoint, when non-empty, is the relay endpoint type this client's wire protocol requires. `everyapi use` checks the live model catalog before launch so a client cannot enter its own retry loop when every available model explicitly lacks that protocol bridge.
	RequiredEndpoint string
	// AlternativeEndpoint, when non-empty, is a second wire protocol the client can drive. Models supporting either endpoint are launchable.
	AlternativeEndpoint string

	// envFn builds the env vars from the resolved API base + access token. Returns a map[name]value to merge into os.Environ before exec. Implemented as a function (not a static map) because the per-tool URL suffix varies (some take a v1 prefix, some don't).
	envFn func(apiBase, token string) map[string]string

	// prepareFn is an optional pre-exec hook for tools that need more than env vars — e.g. codex, whose router is pinned by `~/.codex/config.toml` (model_provider) and whose auth_mode is pinned by `~/.codex/auth.json` (apikey vs chatgpt). Returning a non-nil env map merges those vars on top of envFn's output (last write wins), letting the hook redirect CODEX_HOME at a generated config dir. Nil means the tool needs no pre-exec setup beyond env vars.
	prepareFn        func(apiBase, token string) (map[string]string, error)
	prepareCatalogFn func(apiBase, token string, models []Model, bootModel string) (map[string]string, error)

	// transparentEnvFn supplies tool-specific placeholder credentials and CA wiring for the process-scoped connector. It must never receive or return the EveryAPI relay key. A nil function means this tool has not yet been verified with transparent mode.
	transparentEnvFn func(caPath string) (set map[string]string, unset []string)

	// prepareTransparentFn is the transparent counterpart of prepareFn. It writes only public routing configuration and placeholder credentials; the real relay key remains inside the connector process.
	prepareTransparentFn        func() (map[string]string, error)
	prepareTransparentCatalogFn func(models []Model, bootModel string) (map[string]string, error)
}

// Model is the launch-safe subset of the relay model catalogue. Credentials never enter this value; prepare hooks may persist it for a client's native /model picker without writing the relay key alongside it.
type Model struct {
	ID                     string
	DisplayName            string
	OwnedBy                string
	SupportedEndpointTypes []string
	ContextWindow          int
	MaxOutput              int
	// SupportsThinking mirrors the gateway's verified `reasoning_effort` support for this model. False means unknown rather than refused, so a generated client config must withhold the level control instead of declaring the model level-less.
	SupportsThinking bool
}

const openCodeCredentialEnv = "EVERYAPI_RELAY_KEY"

type openCodeProviderOptions struct {
	BaseURL string `json:"baseURL"`
	APIKey  string `json:"apiKey"`
}

type openCodeModel struct {
	Name string `json:"name"`
}

type openCodeProvider struct {
	NPM     string                   `json:"npm"`
	Name    string                   `json:"name"`
	Options openCodeProviderOptions  `json:"options"`
	Models  map[string]openCodeModel `json:"models"`
}

type openCodeConfig struct {
	Schema       string                      `json:"$schema"`
	Provider     map[string]openCodeProvider `json:"provider"`
	Model        string                      `json:"model,omitempty"`
	Instructions []string                    `json:"instructions,omitempty"`
}

// prepareOpenCodeWithModels uses OpenCode's documented custom-provider contract through OPENCODE_CONFIG_CONTENT. The content carries only a fixed environment reference; the relay key itself is supplied separately to the child process and is never written to opencode.json or another config file.
func prepareOpenCodeWithModels(apiBase, _ string, models []Model, bootModel string) (map[string]string, error) {
	chatModels := make(map[string]openCodeModel, len(models))
	responseModels := make(map[string]openCodeModel, len(models))
	selected := ""
	firstConfigured := ""
	for _, model := range models {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			continue
		}
		name := strings.TrimSpace(model.DisplayName)
		if name == "" {
			name = id
		}
		providerID := "everyapi"
		configuredModels := chatModels
		if modelSupportsEndpoint(model.SupportedEndpointTypes, "openai-response") {
			providerID = "everyapi-responses"
			configuredModels = responseModels
		}
		configuredModels[id] = openCodeModel{Name: name}
		if firstConfigured == "" {
			firstConfigured = providerID + "/" + id
		}
		if id == bootModel {
			selected = providerID + "/" + id
		}
	}
	if selected == "" {
		selected = firstConfigured
	}

	providers := make(map[string]openCodeProvider, 2)
	options := openCodeProviderOptions{
		BaseURL: joinBase(apiBase, "/v1"),
		APIKey:  "{env:" + openCodeCredentialEnv + "}",
	}
	if len(chatModels) > 0 {
		providers["everyapi"] = openCodeProvider{
			NPM: "@ai-sdk/openai-compatible", Name: "EveryAPI", Options: options, Models: chatModels,
		}
	}
	if len(responseModels) > 0 {
		providers["everyapi-responses"] = openCodeProvider{
			NPM: "@ai-sdk/openai", Name: "EveryAPI Responses", Options: options, Models: responseModels,
		}
	}
	config := openCodeConfig{
		Schema:   "https://opencode.ai/config.json",
		Provider: providers,
	}
	if selected != "" {
		config.Model = selected
	}
	preparedHome := ""
	if instructions := TmuxAgentInstructions(); instructions != "" {
		home, err := newPreparedHome("opencode")
		if err != nil {
			return nil, err
		}
		preparedHome = home
		instructionsPath := filepath.Join(preparedHome, "tmux-context.md")
		if err := writeFileAtomic(instructionsPath, []byte(instructions+"\n"), 0o600); err != nil {
			removePreparedHomeAfterQuiet(preparedHome)
			return nil, err
		}
		config.Instructions = []string{instructionsPath}
	}
	body, err := json.Marshal(config)
	if err != nil {
		if preparedHome != "" {
			removePreparedHomeAfterQuiet(preparedHome)
		}
		return nil, fmt.Errorf("encode OpenCode provider config: %w", err)
	}
	env := map[string]string{"OPENCODE_CONFIG_CONTENT": string(body)}
	if preparedHome != "" {
		env[preparedHomeMarker] = preparedHome
	}
	return env, nil
}

func modelSupportsEndpoint(types []string, required string) bool {
	for _, endpoint := range types {
		if strings.EqualFold(endpoint, required) {
			return true
		}
	}
	return false
}

func (t *Tool) Env(apiBase, token string) map[string]string {
	return t.envFn(apiBase, token)
}

// InstallPromptDefault picks the press-Enter default for the "install <tool> now? [Y/n]" prompt. We default to Yes for routine package-manager installs (npm install -g …) and No for installers that pipe a remote shell script into bash — a single Enter shouldn't ever run untrusted code fetched at install time. InstallCmdUnixOnly marks the reviewed remote-script installers that need a platform-specific Windows override, so it also gives this cohort the safer default. If those concerns diverge, promote the prompt choice to its own field.
func (t *Tool) InstallPromptDefault() bool {
	return !t.InstallCmdUnixOnly
}

// Prepare runs the tool's optional pre-exec setup (e.g. codex's CODEX_HOME / auth.json / config.toml). Returns env additions to overlay on top of Env(). nil map + nil error means "no setup needed". Errors abort the launch — failing to set up an isolated config is preferable to falling back to the user's real ~/.codex and silently going through ChatGPT auth.
func (t *Tool) Prepare(apiBase, token string) (map[string]string, error) {
	return t.PrepareWithModels(apiBase, token, nil, "")
}

// PrepareWithModels runs setup with the live, relay-key-scoped model snapshot.
func (t *Tool) PrepareWithModels(apiBase, token string, models []Model, bootModel string) (map[string]string, error) {
	if t.prepareCatalogFn != nil {
		return t.prepareCatalogFn(apiBase, token, models, bootModel)
	}
	if t.prepareFn == nil {
		return nil, nil
	}
	return t.prepareFn(apiBase, token)
}

// ignoreBootModel adapts a catalog hook that has no boot-model concept. The ModelEnv tools pin their model through an env var their own prepare reads, so the selection EveryAPI records for claude/codex is meaningless to them.
func ignoreBootModel(fn func(apiBase, token string, models []Model) (map[string]string, error)) func(string, string, []Model, string) (map[string]string, error) {
	return func(apiBase, token string, models []Model, _ string) (map[string]string, error) {
		return fn(apiBase, token, models)
	}
}

const transparentPlaceholderCredential = "everyapi-local-connector"

// TransparentEnv returns the complete process proxy overlay and the ambient variables that must be absent from the child environment. Keeping removals explicit is important: setting a Base URL to an empty string is still observably different from not setting it, and inherited real provider keys must not bypass or leak through the connector.
func (t *Tool) TransparentEnv(proxyURL, caPath string) (map[string]string, []string, error) {
	if t.transparentEnvFn == nil {
		return nil, nil, fmt.Errorf("transparent mode is not supported for %s yet", t.Name)
	}
	if !strings.HasPrefix(proxyURL, "http://127.0.0.1:") && !strings.HasPrefix(proxyURL, "http://[::1]:") {
		return nil, nil, fmt.Errorf("transparent connector proxy must be loopback HTTP")
	}
	if strings.TrimSpace(caPath) == "" {
		return nil, nil, fmt.Errorf("transparent connector CA path is required")
	}
	set := map[string]string{
		"HTTPS_PROXY": proxyURL,
		"https_proxy": proxyURL,
	}
	// Do not inherit a corporate/plaintext proxy, a SOCKS fallback, or an exclusion that could make an official model origin bypass Connector.
	unset := []string{"HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy", "EVERYAPI_RELAY_KEY"}
	specific, specificUnset := t.transparentEnvFn(caPath)
	for key, value := range specific {
		set[key] = value
	}
	unset = append(unset, specificUnset...)
	return set, unset, nil
}

// PrepareTransparent runs only setup that preserves the vendor's official origin. It never accepts the gateway base URL or relay credential by design.
func (t *Tool) PrepareTransparent() (map[string]string, error) {
	return t.PrepareTransparentWithModels(nil, "")
}

// PrepareTransparentWithModels is the catalogue-aware transparent setup path.
func (t *Tool) PrepareTransparentWithModels(models []Model, bootModel string) (map[string]string, error) {
	if t.transparentEnvFn == nil {
		return nil, fmt.Errorf("transparent mode is not supported for %s yet", t.Name)
	}
	if t.prepareTransparentCatalogFn != nil {
		return t.prepareTransparentCatalogFn(models, bootModel)
	}
	if t.prepareTransparentFn == nil {
		return nil, nil
	}
	return t.prepareTransparentFn()
}

func (t *Tool) SupportsTransparent() bool {
	return t != nil && t.transparentEnvFn != nil
}

func transparentClaudeEnv(caPath string) (map[string]string, []string) {
	return map[string]string{
			"ANTHROPIC_BASE_URL":                         "https://api.anthropic.com",
			"ANTHROPIC_AUTH_TOKEN":                       transparentPlaceholderCredential,
			"NODE_EXTRA_CA_CERTS":                        caPath,
			"ENABLE_TOOL_SEARCH":                         "1",
			"CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY": "1",
			// Claude Code gates /v1/models behind gateway mode even when discovery is enabled. Keep the official origin here: Connector intercepts it and the child still never receives EveryAPI's gateway URL or relay key. Gateway mode also swaps the family alias table for the gateway tier's, which resolves opus to a retired id; prepareTransparentCatalogFn corrects that from the catalogue.
			"CLAUDE_CODE_USE_GATEWAY": "1",
			// Advisor is an experimental account-bound server tool. Disable it when Claude runs through EveryAPI so rejected results cannot poison the session and fail every subsequent prompt.
			"CLAUDE_CODE_DISABLE_ADVISOR_TOOL": "1",
		}, []string{
			"ANTHROPIC_API_KEY",
			"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY",
		}
}

// transparentStandaloneClaudeEnv is the transparent overlay for the plain `everyapi use claude` tool. It layers ENABLE_PROMPT_CACHING_1H on top of the shared transparentClaudeEnv so standalone Claude keeps the 1h prompt-cache window its injected (non-transparent) path already sets (see the "claude" Registry envFn).
func transparentStandaloneClaudeEnv(caPath string) (map[string]string, []string) {
	set, unset := transparentClaudeEnv(caPath)
	set["ENABLE_PROMPT_CACHING_1H"] = "1"
	return set, unset
}

func transparentCodexEnv(caPath string) (map[string]string, []string) {
	return map[string]string{
		"OPENAI_API_KEY":       transparentPlaceholderCredential,
		"CODEX_CA_CERTIFICATE": caPath,
	}, []string{"OPENAI_BASE_URL", "OPENAI_API_BASE", "CODEX_API_KEY"}
}

func transparentGeminiEnv(caPath string) (map[string]string, []string) {
	return map[string]string{
		"GEMINI_API_KEY":      transparentPlaceholderCredential,
		"NODE_EXTRA_CA_CERTS": caPath,
	}, []string{"GOOGLE_GEMINI_BASE_URL", "GOOGLE_API_KEY"}
}

// joinBase concatenates the API base and a tool-specific suffix, avoiding double slashes. Centralized so adding a tool doesn't have to reinvent the join logic.
func joinBase(base, suffix string) string {
	base = strings.TrimRight(base, "/")
	if suffix == "" {
		return base
	}
	if !strings.HasPrefix(suffix, "/") {
		suffix = "/" + suffix
	}
	return base + suffix
}

// Registry is the full set of supported tools, keyed by the name the user types (`everyapi use <name>`). Names are lowercase.
var Registry = map[string]*Tool{
	// Anthropic Claude Code: reads ANTHROPIC_BASE_URL and ANTHROPIC_AUTH_TOKEN. The CLI sends the raw base URL — no /v1 suffix — because Anthropic's official client appends its own version path. Verified in Anthropic SDK source.
	"claude": {
		Name:        "claude",
		ExecName:    "claude",
		InstallHint: "Install Claude Code: https://docs.claude.com/en/docs/claude-code/setup",
		InstallCmd:  "curl -fsSL https://claude.ai/install.sh | bash || curl -fsSL https://dl.everyapi.ai/claude-code/install.sh | bash",
		InstallCmdWindows: []string{
			"powershell", "-ExecutionPolicy", "ByPass", "-Command",
			"irm https://dl.everyapi.ai/claude-code/install.ps1 | iex",
		},
		InstallCmdUnixOnly: true,
		// claude.ai/install.sh hands off to `<binary> install`, which links the launcher into ~/.local/bin — the same off-PATH cohort gemini hits.
		ExtraBinDirs:     []string{".local/bin"},
		YoloFlag:         "--dangerously-skip-permissions",
		YoloLabel:        "skip all permission prompts (--dangerously-skip-permissions)",
		RequiredEndpoint: "anthropic",
		transparentEnvFn: transparentStandaloneClaudeEnv,
		// Both paths pin the family aliases from the launch catalogue. CLAUDE_CODE_USE_GATEWAY below is what makes this necessary — see claudeFamilyDefaultEnv.
		prepareCatalogFn:            ignoreBootModel(prepareClaudeWithModels),
		prepareTransparentCatalogFn: prepareClaudeTransparentWithModels,
		envFn: func(apiBase, token string) map[string]string {
			return map[string]string{
				"ANTHROPIC_BASE_URL":   joinBase(apiBase, ""),
				"ANTHROPIC_AUTH_TOKEN": token,
				// Preserve Claude Code's deferred MCP ToolSearch behavior when the CLI is pointed at the EveryAPI gateway.
				"ENABLE_TOOL_SEARCH":                         "1",
				"ENABLE_PROMPT_CACHING_1H":                   "1",
				"CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY": "1",
				// Claude Code only fetches /v1/models when both discovery and gateway mode are enabled. Gateway mode also swaps its family alias table for the gateway tier's, which still resolves opus to claude-opus-4-7; prepareCatalogFn above corrects that from the catalogue.
				"CLAUDE_CODE_USE_GATEWAY":          "1",
				"CLAUDE_CODE_DISABLE_ADVISOR_TOOL": "1",
				// Clear any ambient ANTHROPIC_API_KEY so the user's real key is never forwarded to the gateway and can't shadow the relay token.
				"ANTHROPIC_API_KEY": "",
			}
		},
	},

	// OpenAI Codex CLI: unlike claude/gemini, codex does NOT read OPENAI_BASE_URL at runtime — its router is pinned to the active config/profile's `model_provider`, and auth_mode is pinned in CODEX_HOME/auth.json (defaults to "chatgpt" on a fresh install, which then redirects to the ChatGPT login page regardless of OPENAI_API_KEY). To make `everyapi use codex` actually route through the gateway we redirect CODEX_HOME to an EveryAPI-owned persistent directory and supply a generated lifecycle-bound provider profile (see codex.go). OPENAI_API_KEY carries the launch credential referenced by that profile's env_key; auth.json contains only a launch-independent placeholder.
	"codex": {
		Name:        "codex",
		ExecName:    "codex",
		InstallHint: "Install Codex CLI: https://github.com/openai/codex#installation",
		InstallCmd:  "npm install -g @openai/codex || npm install -g @openai/codex --registry=https://mirrors.cloud.tencent.com/npm/ || npm install -g @openai/codex --registry=https://registry.npmmirror.com",
		YoloFlag:    "--dangerously-bypass-approvals-and-sandbox",
		YoloLabel:   "bypass approvals + sandbox (--dangerously-bypass-approvals-and-sandbox)",
		// `exec --help` parses the full command line, prints usage, and exits — no session, no stdin, no hooks. Codex needs the probe more than any other tool here: its parser declares both --dangerously-bypass-hook-trust and --dangerously-bypass-approvals-and-sandbox non-repeatable, so a wrapper that injects either one turns EveryAPI's copy into a launch-aborting parse error.
		FlagProbeArgs:               []string{"exec", "--help"},
		RequiredEndpoint:            "openai-response",
		transparentEnvFn:            transparentCodexEnv,
		prepareTransparentFn:        prepareCodexTransparent,
		prepareTransparentCatalogFn: prepareCodexTransparentWithModels,
		envFn: func(_, token string) map[string]string {
			return map[string]string{"OPENAI_API_KEY": token}
		},
		prepareFn:        prepareCodex,
		prepareCatalogFn: prepareCodexWithModels,
	},

	// OpenCode supports custom OpenAI-compatible providers through its public config schema. OPENCODE_CONFIG_CONTENT keeps this configuration scoped to the launched process, while the apiKey field contains only an env reference so the relay key never enters JSON or a project opencode.json.
	"opencode": {
		Name:                "opencode",
		ExecName:            "opencode",
		InstallHint:         "Install OpenCode: https://opencode.ai/docs/",
		InstallCmd:          "npm install -g opencode-ai || npm install -g opencode-ai --registry=https://mirrors.cloud.tencent.com/npm/ || npm install -g opencode-ai --registry=https://registry.npmmirror.com",
		RequiredEndpoint:    "openai",
		AlternativeEndpoint: "openai-response",
		envFn: func(_, token string) map[string]string {
			return map[string]string{openCodeCredentialEnv: token}
		},
		prepareCatalogFn: prepareOpenCodeWithModels,
	},

	// Google's Gemini CLI supports API-key auth and a custom Gemini API origin through documented environment variables. prepareGemini overlays system settings so cached OAuth state cannot override the process-scoped key.
	"gemini": {
		Name:             "gemini",
		ExecName:         "gemini",
		InstallHint:      "Install Gemini CLI: https://github.com/google-gemini/gemini-cli#installation",
		InstallCmd:       "npm install -g @google/gemini-cli || npm install -g @google/gemini-cli --registry=https://mirrors.cloud.tencent.com/npm/ || npm install -g @google/gemini-cli --registry=https://registry.npmmirror.com",
		YoloFlag:         "--yolo",
		YoloLabel:        "yolo mode — auto-approve every tool call (--yolo)",
		ModelEnv:         "GEMINI_MODEL",
		RequiredEndpoint: "gemini",
		envFn: func(apiBase, token string) map[string]string {
			return map[string]string{
				"GEMINI_API_KEY":         token,
				"GOOGLE_GEMINI_BASE_URL": joinBase(apiBase, ""),
			}
		},
		prepareFn: prepareGemini,
	},

	// Antigravity CLI launches the locally authenticated `agy` client. agy owns its Google OAuth credential and upstream routing, so never pass the EveryAPI relay key or transparent-connector environment into the child.
	"antigravity": {
		Name:     "antigravity",
		ExecName: "agy",
		// Deliberately platform-neutral: this string is what Windows users see (CanAutoInstall is false for them), and the docs page carries the correct per-platform command. Inlining the Unix pipeline here would hand PowerShell users a `curl` that resolves to Invoke-WebRequest and fails.
		InstallHint: "Install the Antigravity CLI (`agy`): https://antigravity.google/docs/cli/install " +
			"— then sign in once before running `everyapi use antigravity`.",
		// Antigravity's own installer, per the install docs above. It writes the binary to ~/.local/bin/agy — hence ExtraBinDirs, so the post-install re-check finds it even when the user's shell only puts that dir on $PATH from an rc file we never source.
		//
		// The official Unix script remains first. Windows and the Unix fallback use the reviewed OSS mirror and install into EveryAPI's discovered local bin directory.
		InstallCmd:                 "curl -fsSL https://antigravity.google/cli/install.sh | bash || curl -fsSL https://dl.everyapi.ai/cli-mirrors/install.sh | bash -s -- antigravity",
		InstallCmdWindows:          []string{"powershell", "-ExecutionPolicy", "ByPass", "-Command", "& ([scriptblock]::Create((irm https://dl.everyapi.ai/cli-mirrors/install.ps1))) -Tool antigravity"},
		InstallCmdUnixOnly:         true,
		ExtraBinDirs:               []string{".local/bin"},
		WindowsLocalAppDataBinDirs: []string{"EveryAPI/bin"},
		Native:                     true,
		YoloFlag:                   "--dangerously-skip-permissions",
		YoloLabel:                  "skip all permission prompts (--dangerously-skip-permissions)",
		envFn: func(_, _ string) map[string]string {
			return map[string]string{}
		},
	},

	// Aider routes OpenAI-compatible models through LiteLLM. Aider expects the model namespace `openai/<id>` while EveryAPI's catalogue returns bare ids; prepareAider performs that process-scoped translation.
	"aider": {
		Name:        "aider",
		ExecName:    "aider",
		InstallHint: "Install Aider: https://aider.chat/docs/install.html",
		InstallCmd:  "curl -LsSf https://aider.chat/install.sh | sh || (curl -fsSL https://dl.everyapi.ai/cli-mirrors/uv/install.sh | sh && UV_PYTHON_INSTALL_MIRROR=https://dl.everyapi.ai/cli-mirrors/python UV_DEFAULT_INDEX=https://mirrors.aliyun.com/pypi/simple/ \"$HOME/.local/bin/uv\" tool install --python 3.12 aider-chat)",
		InstallCmdWindows: []string{
			"powershell", "-ExecutionPolicy", "ByPass", "-Command",
			"irm https://dl.everyapi.ai/cli-mirrors/uv/install.ps1 | iex; $env:UV_PYTHON_INSTALL_MIRROR='https://dl.everyapi.ai/cli-mirrors/python'; $env:UV_DEFAULT_INDEX='https://mirrors.aliyun.com/pypi/simple/'; & \"$env:USERPROFILE\\.local\\bin\\uv.exe\" tool install --python 3.12 aider-chat",
		},
		InstallCmdUnixOnly: true,
		ExtraBinDirs:       []string{".local/bin"},
		ModelEnv:           aiderModelEnv,
		RequiredEndpoint:   "openai",
		envFn: func(apiBase, token string) map[string]string {
			base := joinBase(apiBase, "/v1")
			return map[string]string{
				"OPENAI_API_KEY":        token,
				"OPENAI_API_BASE":       base,
				"AIDER_OPENAI_API_KEY":  token,
				"AIDER_OPENAI_API_BASE": base,
			}
		},
		prepareFn: prepareAider,
	},

	// Goose's OpenAI provider accepts a custom endpoint entirely through environment variables, keeping the user's persistent Goose config intact.
	"goose": {
		Name:        "goose",
		ExecName:    "goose",
		InstallHint: "Install Goose CLI: https://block.github.io/goose/docs/getting-started/installation/",
		InstallCmd:  "curl -fsSL https://github.com/aaif-goose/goose/releases/download/stable/download_cli.sh | CONFIGURE=false bash || curl -fsSL https://dl.everyapi.ai/cli-mirrors/install.sh | bash -s -- goose",
		InstallCmdWindows: []string{
			"powershell", "-ExecutionPolicy", "ByPass", "-Command",
			"& ([scriptblock]::Create((irm https://dl.everyapi.ai/cli-mirrors/install.ps1))) -Tool goose",
		},
		InstallCmdUnixOnly:         true,
		ExtraBinDirs:               []string{".local/bin"},
		WindowsLocalAppDataBinDirs: []string{"EveryAPI/bin"},
		ModelEnv:                   "GOOSE_MODEL",
		RequiredEndpoint:           "openai",
		envFn: func(apiBase, token string) map[string]string {
			return map[string]string{
				"GOOSE_PROVIDER":  "openai",
				"OPENAI_API_KEY":  token,
				"OPENAI_BASE_URL": joinBase(apiBase, "/v1"),
			}
		},
	},

	// Crush resolves `$ENV` references in its config. Generate a complete, process-scoped model catalogue and keep the relay credential only in the child environment.
	"crush": {
		Name:        "crush",
		ExecName:    "crush",
		InstallHint: "Install Crush: https://github.com/charmbracelet/crush#installation",
		InstallCmd:  "npm install -g @charmland/crush || npm install -g @charmland/crush --registry=https://mirrors.cloud.tencent.com/npm/ || npm install -g @charmland/crush --registry=https://registry.npmmirror.com || curl -fsSL https://dl.everyapi.ai/cli-mirrors/install.sh | bash -s -- crush",
		InstallCmdWindows: []string{
			"powershell", "-ExecutionPolicy", "ByPass", "-Command",
			"& npm install -g @charmland/crush; if ($LASTEXITCODE -ne 0) { & npm install -g @charmland/crush --registry=https://mirrors.cloud.tencent.com/npm/ }; if ($LASTEXITCODE -ne 0) { & npm install -g @charmland/crush --registry=https://registry.npmmirror.com }; if ($LASTEXITCODE -ne 0) { & ([scriptblock]::Create((irm https://dl.everyapi.ai/cli-mirrors/install.ps1))) -Tool crush }",
		},
		ExtraBinDirs:               []string{".local/bin"},
		WindowsLocalAppDataBinDirs: []string{"EveryAPI/bin"},
		ModelEnv:                   crushModelEnv,
		RequiredEndpoint:           "openai",
		envFn: func(_, token string) map[string]string {
			return map[string]string{crushCredentialEnv: token}
		},
		prepareCatalogFn: ignoreBootModel(prepareCrushWithModels),
	},

	// Cline CLI supports an explicit provider-settings path. Redirect it to a lifecycle-bound data directory so EveryAPI never mutates ~/.cline. Use its overridable LM Studio provider for OpenAI-compatible Chat Completions. The gateway bridges OpenAI/Codex Responses models through that endpoint so Cline's provider-scoped /model picker can present one EveryAPI catalogue; future non-bridgeable Responses models retain an openai-native fallback.
	"cline": {
		Name:                "cline",
		ExecName:            "clite",
		InstallHint:         "Install Cline CLI: https://github.com/cline/cline/tree/main/apps/cli",
		InstallCmd:          "npm install -g @cline/cli || npm install -g @cline/cli --registry=https://mirrors.cloud.tencent.com/npm/ || npm install -g @cline/cli --registry=https://registry.npmmirror.com",
		ModelEnv:            clineModelEnv,
		RequiredEndpoint:    "openai",
		AlternativeEndpoint: "openai-response",
		envFn: func(_, _ string) map[string]string {
			return map[string]string{}
		},
		prepareCatalogFn: ignoreBootModel(prepareClineWithModels),
	},

	// OpenClaw's local TUI embeds the agent runtime, so it does not need a separately managed gateway process. A generated config registers the live EveryAPI model catalogue and refers to the relay key through SecretRef.
	"openclaw": {
		Name:             "openclaw",
		ExecName:         "openclaw",
		InstallHint:      "Install OpenClaw: https://docs.openclaw.ai/install",
		InstallCmd:       "npm install -g openclaw@latest || npm install -g openclaw@latest --registry=https://mirrors.cloud.tencent.com/npm/ || npm install -g openclaw@latest --registry=https://registry.npmmirror.com",
		DefaultArgs:      []string{"tui", "--local"},
		ModelEnv:         openClawModelEnv,
		RequiredEndpoint: "openai",
		envFn: func(_, token string) map[string]string {
			return map[string]string{openClawCredentialEnv: token}
		},
		prepareCatalogFn: ignoreBootModel(prepareOpenClawWithModels),
	},

	// Continue accepts an explicit local assistant YAML and resolves local secrets from process.env. Keep both its config and session state in the lifecycle-bound CONTINUE_GLOBAL_DIR.
	"continue": {
		Name:             "continue",
		ExecName:         "cn",
		InstallHint:      "Install Continue CLI: https://docs.continue.dev/guides/cli",
		InstallCmd:       "npm install -g @continuedev/cli || npm install -g @continuedev/cli --registry=https://mirrors.cloud.tencent.com/npm/ || npm install -g @continuedev/cli --registry=https://registry.npmmirror.com",
		ModelEnv:         continueModelEnv,
		RequiredEndpoint: "openai",
		envFn: func(_, token string) map[string]string {
			return map[string]string{openClawCredentialEnv: token}
		},
		prepareCatalogFn: ignoreBootModel(prepareContinueWithModels),
	},

	// Open WebUI documents both the `open-webui serve` launcher and these semicolon-delimited OpenAI connection environment variables. Keep the gateway credential process-scoped and let the sidecar supervise the server.
	"open-webui": {
		Name:        "open-webui",
		ExecName:    "open-webui",
		InstallHint: "Install Open WebUI: https://docs.openwebui.com/getting-started/quick-start/",
		InstallCmd:  "(curl -LsSf https://astral.sh/uv/install.sh | sh && \"$HOME/.local/bin/uv\" tool install --python 3.11 open-webui) || (curl -fsSL https://dl.everyapi.ai/cli-mirrors/uv/install.sh | sh && UV_PYTHON_INSTALL_MIRROR=https://dl.everyapi.ai/cli-mirrors/python UV_DEFAULT_INDEX=https://mirrors.aliyun.com/pypi/simple/ \"$HOME/.local/bin/uv\" tool install --python 3.11 open-webui)",
		InstallCmdWindows: []string{
			"powershell", "-ExecutionPolicy", "ByPass", "-Command",
			"irm https://dl.everyapi.ai/cli-mirrors/uv/install.ps1 | iex; $env:UV_PYTHON_INSTALL_MIRROR='https://dl.everyapi.ai/cli-mirrors/python'; $env:UV_DEFAULT_INDEX='https://mirrors.aliyun.com/pypi/simple/'; & \"$env:USERPROFILE\\.local\\bin\\uv.exe\" tool install --python 3.11 open-webui",
		},
		InstallCmdUnixOnly: true,
		ExtraBinDirs:       []string{".local/bin"},
		DefaultArgs:        []string{"serve"},
		RequiredEndpoint:   "openai",
		envFn: func(apiBase, token string) map[string]string {
			return map[string]string{
				"OPENAI_API_BASE_URLS":     joinBase(apiBase, "/v1"),
				"OPENAI_API_KEYS":          token,
				"ENABLE_PERSISTENT_CONFIG": "false",
			}
		},
		prepareFn: prepareOpenWebUI,
	},

	// DeepSeek Harness publishes the `dsh` binary from its official npm package. The preparation hook owns only the EveryAPI provider entry and credential; `dsh web` serves the official UI on its default loopback address.
	"deepseek-harness": {
		Name:             "deepseek-harness",
		ExecName:         "dsh",
		InstallHint:      "Install DeepSeek Harness: https://github.com/deepseek-ai/deepseek-harness#run",
		InstallCmd:       "npm install -g @deepseek-ai/dsh || npm install -g @deepseek-ai/dsh --registry=https://mirrors.cloud.tencent.com/npm/ || npm install -g @deepseek-ai/dsh --registry=https://registry.npmmirror.com",
		DefaultArgs:      []string{"web"},
		RequiredEndpoint: "openai",
		envFn: func(_, _ string) map[string]string {
			return map[string]string{}
		},
		prepareCatalogFn: ignoreBootModel(prepareDeepSeekHarnessWithModels),
	},

	// Kilo CLI is an OpenCode fork with its own trusted KILO_CONFIG_CONTENT surface. Reuse the reviewed OpenCode provider shape while preventing project configuration from overriding the launch.
	"kilo": {
		Name:                "kilo",
		ExecName:            "kilo",
		InstallHint:         "Install Kilo Code CLI: https://kilo.ai/docs/code-with-ai/platforms/cli",
		InstallCmd:          "npm install -g @kilocode/cli || npm install -g @kilocode/cli --registry=https://mirrors.cloud.tencent.com/npm/ || npm install -g @kilocode/cli --registry=https://registry.npmmirror.com",
		ModelEnv:            kiloModelEnv,
		RequiredEndpoint:    "openai",
		AlternativeEndpoint: "openai-response",
		envFn: func(_, token string) map[string]string {
			return map[string]string{openClawCredentialEnv: token}
		},
		prepareCatalogFn: ignoreBootModel(prepareKiloWithModels),
	},

	// Pi documents an overrideable agent directory and environment-backed credentials in models.json. Its isolated settings pin the selected model.
	"pi": {
		Name:                "pi",
		ExecName:            "pi",
		InstallHint:         "Install Pi: https://pi.dev/docs/quickstart",
		InstallCmd:          "npm install -g --ignore-scripts @earendil-works/pi-coding-agent || npm install -g --ignore-scripts @earendil-works/pi-coding-agent --registry=https://mirrors.cloud.tencent.com/npm/ || npm install -g --ignore-scripts @earendil-works/pi-coding-agent --registry=https://registry.npmmirror.com",
		ModelEnv:            piModelEnv,
		RequiredEndpoint:    "openai",
		AlternativeEndpoint: "openai-response",
		envFn: func(_, token string) map[string]string {
			return map[string]string{openClawCredentialEnv: token}
		},
		prepareCatalogFn: ignoreBootModel(preparePiWithModels),
	},

	// Pi Web is the official browser UI over the same pi agent directory, so its configuration has to be durable: sessions, project trust, the selected model, and the Models panel's own edits all live there. Registering the provider in the real models.json keeps every one of those, and the entry references the relay key through the environment rather than storing it.
	"pi-web": {
		Name:                "pi-web",
		ExecName:            "pi-web",
		InstallHint:         "Install Pi Web: https://github.com/agegr/pi-web#quick-start",
		InstallCmd:          "npm install -g @agegr/pi-web@latest || npm install -g @agegr/pi-web@latest --registry=https://mirrors.cloud.tencent.com/npm/ || npm install -g @agegr/pi-web@latest --registry=https://registry.npmmirror.com",
		RequiredEndpoint:    "openai",
		AlternativeEndpoint: "openai-response",
		envFn: func(_, token string) map[string]string {
			return map[string]string{openClawCredentialEnv: token}
		},
		prepareCatalogFn: ignoreBootModel(preparePiWebWithModels),
	},

	// Vibe's VIBE_HOME is an official profile boundary. A generated TOML file registers EveryAPI as a generic OpenAI-compatible provider and references the process environment for its credential.
	"vibe": {
		Name:        "vibe",
		ExecName:    "vibe",
		InstallHint: "Install Mistral Vibe: https://github.com/mistralai/mistral-vibe#installation",
		InstallCmd:  "curl -LsSf https://mistral.ai/vibe/install.sh | bash || (curl -fsSL https://dl.everyapi.ai/cli-mirrors/uv/install.sh | sh && UV_PYTHON_INSTALL_MIRROR=https://dl.everyapi.ai/cli-mirrors/python UV_DEFAULT_INDEX=https://mirrors.aliyun.com/pypi/simple/ \"$HOME/.local/bin/uv\" tool install --python 3.12 mistral-vibe)",
		InstallCmdWindows: []string{
			"powershell", "-ExecutionPolicy", "ByPass", "-Command",
			"irm https://dl.everyapi.ai/cli-mirrors/uv/install.ps1 | iex; $env:UV_PYTHON_INSTALL_MIRROR='https://dl.everyapi.ai/cli-mirrors/python'; $env:UV_DEFAULT_INDEX='https://mirrors.aliyun.com/pypi/simple/'; & \"$env:USERPROFILE\\.local\\bin\\uv.exe\" tool install --python 3.12 mistral-vibe",
		},
		InstallCmdUnixOnly: true,
		ExtraBinDirs:       []string{".local/bin"},
		ModelEnv:           vibeModelEnv,
		RequiredEndpoint:   "openai",
		envFn: func(_, token string) map[string]string {
			return map[string]string{openClawCredentialEnv: token}
		},
		prepareCatalogFn: ignoreBootModel(prepareVibeWithModels),
	},

	// GitHub Copilot CLI exposes an official BYOK environment contract. Keep provider selection process-scoped and choose chat/completions versus Responses from the selected model's live EveryAPI capabilities.
	"copilot": {
		Name:                "copilot",
		ExecName:            "copilot",
		InstallHint:         "Install GitHub Copilot CLI: https://docs.github.com/copilot/how-tos/set-up/install-copilot-cli",
		InstallCmd:          "npm install -g @github/copilot || npm install -g @github/copilot --registry=https://mirrors.cloud.tencent.com/npm/ || npm install -g @github/copilot --registry=https://registry.npmmirror.com",
		ModelEnv:            copilotModelEnv,
		RequiredEndpoint:    "openai",
		AlternativeEndpoint: "openai-response",
		envFn: func(apiBase, token string) map[string]string {
			return map[string]string{
				"COPILOT_PROVIDER_BASE_URL":     joinBase(apiBase, "/v1"),
				"COPILOT_PROVIDER_TYPE":         "openai",
				"COPILOT_PROVIDER_API_KEY":      token,
				"COPILOT_PROVIDER_BEARER_TOKEN": "",
				"COPILOT_PROVIDER_HEADERS":      "",
			}
		},
		prepareCatalogFn: ignoreBootModel(prepareCopilotWithModels),
	},

	// Factory Droid merges an explicit --settings file only for the current process. Generate one isolated custom model and refer to the credential through Droid's documented ${ENV_VAR} expansion.
	"droid": {
		Name:                "droid",
		ExecName:            "droid",
		InstallHint:         "Install Factory Droid: https://docs.factory.ai/cli/getting-started/quickstart",
		InstallCmd:          "npm install -g droid || npm install -g droid --registry=https://mirrors.cloud.tencent.com/npm/ || npm install -g droid --registry=https://registry.npmmirror.com",
		ModelEnv:            droidModelEnv,
		RequiredEndpoint:    "openai",
		AlternativeEndpoint: "openai-response",
		envFn: func(_, token string) map[string]string {
			return map[string]string{openClawCredentialEnv: token}
		},
		prepareCatalogFn: ignoreBootModel(prepareDroidWithModels),
	},

	// OpenHands CLI supports process-only LLM_* overrides when the explicit --override-with-envs switch is present. No persistent user settings are read or mutated for this launch.
	"openhands": {
		Name:        "openhands",
		ExecName:    "openhands",
		InstallHint: "Install OpenHands CLI: https://github.com/OpenHands/OpenHands-CLI#installation",
		InstallCmd:  "curl -fsSL https://install.openhands.dev/install.sh | sh || curl -fsSL https://dl.everyapi.ai/cli-mirrors/install.sh | bash -s -- openhands",
		InstallCmdWindows: []string{
			"powershell", "-ExecutionPolicy", "ByPass", "-Command",
			"irm https://dl.everyapi.ai/cli-mirrors/uv/install.ps1 | iex; $env:UV_PYTHON_INSTALL_MIRROR='https://dl.everyapi.ai/cli-mirrors/python'; $env:UV_DEFAULT_INDEX='https://mirrors.aliyun.com/pypi/simple/'; & \"$env:USERPROFILE\\.local\\bin\\uv.exe\" tool install openhands --python 3.12",
		},
		InstallCmdUnixOnly: true,
		ExtraBinDirs:       []string{".local/bin"},
		DefaultArgs:        []string{"--override-with-envs"},
		ModelEnv:           openHandsModelEnv,
		RequiredEndpoint:   "openai",
		envFn: func(apiBase, token string) map[string]string {
			return map[string]string{
				"LLM_API_KEY":  token,
				"LLM_BASE_URL": joinBase(apiBase, "/v1"),
			}
		},
		prepareCatalogFn: ignoreBootModel(prepareOpenHandsWithModels),
	},

	// ForgeCode's Chat Completions and Responses-compatible providers read their endpoint and credential from the process environment. A temporary FORGE_CONFIG prevents its credential migration from writing the relay key into the user's profile.
	"forge": {
		Name:        "forge",
		ExecName:    "forge",
		InstallHint: "Install ForgeCode: https://github.com/antinomyhq/forge#installation",
		InstallCmd:  "curl -fsSL https://forgecode.dev/cli | sh || curl -fsSL https://dl.everyapi.ai/cli-mirrors/install.sh | bash -s -- forge",
		InstallCmdWindows: []string{
			"powershell", "-ExecutionPolicy", "ByPass", "-Command",
			"& ([scriptblock]::Create((irm https://dl.everyapi.ai/cli-mirrors/install.ps1))) -Tool forge",
		},
		InstallCmdUnixOnly:         true,
		ExtraBinDirs:               []string{".local/bin"},
		WindowsLocalAppDataBinDirs: []string{"Programs/Forge", "EveryAPI/bin"},
		ModelEnv:                   forgeModelEnv,
		RequiredEndpoint:           "openai",
		AlternativeEndpoint:        "openai-response",
		envFn: func(apiBase, token string) map[string]string {
			return map[string]string{
				"OPENAI_API_KEY": token,
				"OPENAI_URL":     joinBase(apiBase, "/v1"),
			}
		},
		prepareCatalogFn: ignoreBootModel(prepareForgeWithModels),
	},

	// LLxprt accepts an explicit provider, Base URL, and model at CLI precedence. Its application roots are isolated by the preparation hook.
	"llxprt": {
		Name:             "llxprt",
		ExecName:         "llxprt",
		InstallHint:      "Install LLxprt Code: https://github.com/vybestack/llxprt-code#installation",
		InstallCmd:       "npm install -g @vybestack/llxprt-code || npm install -g @vybestack/llxprt-code --registry=https://mirrors.cloud.tencent.com/npm/ || npm install -g @vybestack/llxprt-code --registry=https://registry.npmmirror.com",
		ModelEnv:         llxprtModelEnv,
		RequiredEndpoint: "openai",
		envFn: func(_, token string) map[string]string {
			return map[string]string{"OPENAI_API_KEY": token}
		},
		prepareCatalogFn: ignoreBootModel(prepareLLxprtWithModels),
	},

	// xAI Grok Build: GROK_MODELS_BASE_URL discovers the live catalogue and XAI_API_KEY supplies its bearer credential. prepareGrok uses a fresh auth path per launch so a cached xAI browser session cannot override that key.
	"grok": {
		Name:             "grok",
		ExecName:         "grok",
		InstallHint:      "Install Grok Build: https://docs.x.ai/build/overview (or: npm install -g @xai-official/grok)",
		InstallCmd:       "npm install -g @xai-official/grok || npm install -g @xai-official/grok --registry=https://mirrors.cloud.tencent.com/npm/ || npm install -g @xai-official/grok --registry=https://registry.npmmirror.com",
		YoloFlag:         "--always-approve",
		YoloLabel:        "always approve tool executions (--always-approve)",
		RequiredEndpoint: "openai",
		envFn: func(apiBase, token string) map[string]string {
			base := joinBase(apiBase, "/v1")
			return map[string]string{
				"XAI_API_KEY":          token,
				"GROK_MODELS_BASE_URL": base,
			}
		},
		prepareFn:        prepareGrok,
		prepareCatalogFn: ignoreBootModel(prepareGrokWithModels),
	},

	// Alibaba Qwen Code supports OpenAI-compatible providers through the standard OPENAI_* environment variables. prepareQwenCode isolates its launch state; cmd/use pins the OpenAI protocol at CLI precedence.
	"qwen-code": {
		Name:             "qwen-code",
		ExecName:         "qwen",
		InstallHint:      "Install Qwen Code: https://github.com/QwenLM/qwen-code#installation",
		InstallCmd:       "npm install -g @qwen-code/qwen-code@latest || npm install -g @qwen-code/qwen-code@latest --registry=https://mirrors.cloud.tencent.com/npm/ || npm install -g @qwen-code/qwen-code@latest --registry=https://registry.npmmirror.com",
		YoloFlag:         "--yolo",
		YoloLabel:        "yolo mode — auto-approve every tool call (--yolo)",
		ModelEnv:         "OPENAI_MODEL",
		RequiredEndpoint: "openai",
		envFn: func(apiBase, token string) map[string]string {
			return map[string]string{
				"OPENAI_API_KEY":  token,
				"OPENAI_BASE_URL": joinBase(apiBase, "/v1"),
			}
		},
		prepareFn:        prepareQwenCode,
		prepareCatalogFn: ignoreBootModel(prepareQwenCodeWithModels),
	},

	// Moonshot Kimi Code can synthesize a temporary model entirely from the KIMI_MODEL_* environment family. Use its OpenAI-compatible provider so the selected EveryAPI catalogue model rides /v1/chat/completions.
	"kimi-code": {
		Name:             "kimi-code",
		ExecName:         "kimi",
		InstallHint:      "Install Kimi Code: https://github.com/MoonshotAI/kimi-code#install",
		InstallCmd:       "npm install -g @moonshot-ai/kimi-code || npm install -g @moonshot-ai/kimi-code --registry=https://mirrors.cloud.tencent.com/npm/ || npm install -g @moonshot-ai/kimi-code --registry=https://registry.npmmirror.com",
		YoloFlag:         "--yolo",
		YoloLabel:        "yolo mode — auto-approve every tool call (--yolo)",
		ModelEnv:         "KIMI_MODEL_NAME",
		RequiredEndpoint: "openai",
		envFn: func(apiBase, token string) map[string]string {
			return map[string]string{
				"KIMI_MODEL_API_KEY":       token,
				"KIMI_MODEL_BASE_URL":      joinBase(apiBase, "/v1"),
				"KIMI_MODEL_PROVIDER_TYPE": "openai",
			}
		},
		prepareFn:        prepareKimiCode,
		prepareCatalogFn: ignoreBootModel(prepareKimiCodeWithModels),
	},

	// Nous Research Hermes Agent (Python CLI, binary `hermes`). Unlike claude/gemini, hermes does NOT read OPENAI_BASE_URL for its main model — config.yaml is the single source of truth for the endpoint, and OPENAI_API_KEY is only consulted when base_url's host is openai.com (so it never attaches for an EveryAPI host). So all routing goes through a generated config.yaml under an isolated HERMES_HOME (see hermes.go): provider=custom pins hermes at <apiBase>/v1, with the relay key inlined as model.api_key. envFn therefore sets nothing — HERMES_HOME comes from prepareFn. Yolo is env-based (HERMES_YOLO_MODE), not a flag.
	"hermes": {
		Name:        "hermes",
		ExecName:    "hermes",
		InstallHint: "Install Hermes Agent: https://github.com/NousResearch/hermes-agent (or: pip install hermes-agent)",
		// Pin the third-party script to an immutable commit. Updating Hermes requires reviewing the new script and deliberately changing this SHA; never point an auto-executed installer at main/master/HEAD.
		InstallCmd: "curl -fsSL https://raw.githubusercontent.com/NousResearch/hermes-agent/e444d165807f489b5c1ab8e4a612c8d09c2e67a2/scripts/install.sh | bash -s -- --non-interactive --skip-setup || curl -fsSL https://dl.everyapi.ai/cli-mirrors/hermes/install.sh | bash -s -- --non-interactive --skip-setup",
		InstallCmdWindows: []string{
			"powershell", "-ExecutionPolicy", "ByPass", "-Command",
			"& ([scriptblock]::Create((irm https://dl.everyapi.ai/cli-mirrors/hermes/install.ps1))) -NonInteractive",
		},
		InstallCmdUnixOnly: true,
		// That script's get_command_link_dir() links the command into $HOME/.local/bin for the default non-root install (/usr/local/bin when run as root, which is already a conventional PATH entry).
		ExtraBinDirs:               []string{".local/bin"},
		WindowsLocalAppDataBinDirs: []string{"hermes/hermes-agent/bin"},
		YoloEnv:                    "HERMES_YOLO_MODE",
		YoloLabel:                  "yolo mode — disable all approval prompts (HERMES_YOLO_MODE)",
		ModelEnv:                   hermesModelEnv,
		RequiredEndpoint:           "openai",
		envFn: func(_, _ string) map[string]string {
			// Routing is config-file driven; see prepareHermes.
			return map[string]string{}
		},
		prepareFn:        prepareHermes,
		prepareCatalogFn: ignoreBootModel(prepareHermesWithModels),
	},

	// LibreFang ships a first-party EveryAPI credential-process integration. It resolves the current relay key per request and owns its provider state, so launch it natively without copying a credential into the environment.
	"librefang": {
		Name:        "librefang",
		ExecName:    "librefang",
		InstallHint: "Install LibreFang: https://github.com/librefang/librefang#quick-start",
		InstallCmd:  "curl -fsSL https://librefang.ai/install.sh | LIBREFANG_AUTO_START=0 sh || curl -fsSL https://dl.everyapi.ai/cli-mirrors/install.sh | bash -s -- librefang",
		InstallCmdWindows: []string{
			"powershell", "-ExecutionPolicy", "ByPass", "-Command",
			"& ([scriptblock]::Create((irm https://dl.everyapi.ai/cli-mirrors/install.ps1))) -Tool librefang",
		},
		InstallCmdUnixOnly:         true,
		ExtraBinDirs:               []string{".librefang/bin", ".local/bin"},
		WindowsLocalAppDataBinDirs: []string{"EveryAPI/bin"},
		// LibreFang owns its daemon lifecycle through `start`/`status`/`stop`, so launch it the way it documents: `start` detaches, prints the API and dashboard URLs, and hands the terminal back. Forcing `--foreground` to keep a supervising sidecar alive would pin an unattended log stream to the user's terminal and make Ctrl+C or closing the window kill the daemon. Connect reads the detached daemon process directly instead of inferring a session from the sidecar.
		DefaultArgs: []string{"start"},
		Native:      true,
		envFn: func(_, _ string) map[string]string {
			return map[string]string{}
		},
	},
}

// Lookup returns the tool entry for `name`, or an error listing the supported names. cmd/use.go renders that error directly to the user.
func Lookup(name string) (*Tool, error) {
	t, ok := Registry[strings.ToLower(name)]
	if !ok {
		return nil, fmt.Errorf("unknown tool %q — supported: %s", name, strings.Join(Names(), ", "))
	}
	return t, nil
}

// Names returns the registered tool names in stable order. Used by the no-arg `everyapi use` interactive picker and by Lookup's error message.
func Names() []string {
	// Deterministic order matters for both the error message and the picker UX. Hand-coded to match the ordering most likely to reflect user demand.
	return []string{
		"claude", "codex", "opencode", "gemini", "antigravity", "aider", "goose", "crush", "cline", "openclaw", "continue", "kilo", "pi", "pi-web", "vibe", "copilot", "droid", "openhands", "forge", "llxprt", "grok", "qwen-code", "kimi-code", "hermes", "librefang", "open-webui", "deepseek-harness",
	}
}
