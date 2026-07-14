// Package tools is the registry of third-party CLIs that `everyapi use`
// knows how to point at EveryAPI. Adding a tool here is a single map
// entry — no changes elsewhere.
//
// Each entry describes:
//   - ExecName: the binary that gets exec'd (looked up in $PATH)
//   - Env: the environment variables to set so the tool talks to the
//     EveryAPI gateway. URLs are computed at runtime by appending the
//     tool-specific suffix to the user's configured API base, so a
//     local-dev base (http://localhost:8787) works without per-tool
//     env edits.
//   - InstallHint: copy printed when ExecName isn't on PATH
//
// The env-var conventions are read straight off each tool's docs (see
// the comment on the entry). When upstream renames a variable we
// update one line here.
package tools

import (
	"fmt"
	"strings"
)

// Tool describes how to launch one third-party CLI against EveryAPI.
type Tool struct {
	Name        string
	ExecName    string
	InstallHint string

	// InstallCmd is the shell command 'everyapi use' offers to run
	// on the user's behalf when ExecName isn't on $PATH. Executed
	// via `sh -c` on Unix and `cmd /C` on Windows; an empty value
	// disables the auto-install prompt and the user falls back to
	// reading InstallHint and running the installer themselves.
	// For tools whose canonical installer is Unix-only (e.g. a
	// `curl | bash` script), InstallCmdUnixOnly should be true so
	// Windows users see the hint instead of a guaranteed-to-fail
	// shell pipeline.
	//
	// SECURITY INVARIANT: this string MUST be a compile-time literal
	// embedded in the Registry below. It is passed verbatim to `sh
	// -c` / `cmd /C` with no escaping — sourcing it from user input,
	// env vars, config files, or any network response would be RCE.
	// If you find yourself wanting to make this dynamic, design a
	// per-tool installer function instead.
	InstallCmd string
	// InstallCmdUnixOnly gates InstallCmd off on Windows when the
	// command relies on a POSIX-only pipeline (curl | bash, etc.).
	// Doubles as the "this installer is less reversible than `npm
	// install -g`" signal: prompt callers default to N when this is
	// true, so a single press of Enter never runs a remote shell
	// script on the user's machine.
	InstallCmdUnixOnly bool

	// YoloFlag is the tool-specific "skip every confirmation"
	// argument the user might want to pass — claude's
	// --dangerously-skip-permissions, codex's
	// --dangerously-bypass-approvals-and-sandbox, gemini's --yolo.
	// 'everyapi use' offers the flag via a TTY confirm prompt
	// before exec so the user can opt in without having to
	// remember the exact string. Empty for tools where no such
	// blanket-bypass flag exists.
	YoloFlag string
	// YoloLabel is the human-readable name shown in the prompt:
	// "Enable <YoloLabel>? [y/N]". Should be short and reflect
	// what the user gets — "skip permission prompts (claude)" /
	// "bypass approvals + sandbox (codex)" / "yolo mode (gemini)".
	YoloLabel string
	// YoloEnv is the env-var form of the blanket-bypass switch, for
	// tools whose "skip every confirmation" mode is toggled by an
	// environment variable rather than an argv flag — hermes reads
	// HERMES_YOLO_MODE=1 and exposes no equivalent command-line flag.
	// When set and the user opts in at the prompt, 'everyapi use'
	// puts this var = "1" in the tool's env instead of (or in
	// addition to) prepending YoloFlag. A tool may set YoloFlag,
	// YoloEnv, or both; empty when the tool has no blanket-bypass
	// switch at all.
	YoloEnv string

	// ModelOwner, when non-empty, marks a Claude Code preset that drives
	// one upstream provider's models: `everyapi use <name>` shows a model
	// picker scoped to the provider whose /v1/models `owned_by` equals
	// this value (e.g. "zhipu" for glm, "moonshot" for kimi), then
	// launches the `claude` binary pinned to the chosen id. The preset's
	// envFn sets only the base URL + token; cmd/use resolves the model
	// (picker on a TTY, `--model` flag otherwise) and injects it via
	// SetClaudeModel. Empty for the standalone agents
	// (claude/codex/gemini/hermes), which pick their own model.
	ModelOwner string

	// ModelEnv names the env var the tool's prepareFn reads to pin the
	// upstream model. Set only for tools that don't carry their own
	// vendor-default model and therefore need EveryAPI to choose one —
	// hermes (provider=custom) reads EVERYAPI_HERMES_MODEL. When
	// non-empty, 'everyapi use' offers a model picker (populated from
	// the gateway's model catalog) before launch and honors a
	// `--model <id>` flag. Empty for claude/codex/gemini, whose own
	// CLIs default the model and route it by name through the gateway.
	ModelEnv string

	// RequiredEndpoint, when non-empty, is the relay endpoint type this
	// client's wire protocol requires. `everyapi use` checks the live model
	// catalog before launch so a client cannot enter its own retry loop when
	// every available model explicitly lacks that protocol bridge.
	RequiredEndpoint string

	// envFn builds the env vars from the resolved API base + access
	// token. Returns a map[name]value to merge into os.Environ before
	// exec. Implemented as a function (not a static map) because the
	// per-tool URL suffix varies (some take a v1 prefix, some don't).
	envFn func(apiBase, token string) map[string]string

	// prepareFn is an optional pre-exec hook for tools that need
	// more than env vars — e.g. codex, whose router is pinned by
	// `~/.codex/config.toml` (model_provider) and whose auth_mode is
	// pinned by `~/.codex/auth.json` (apikey vs chatgpt). Returning
	// a non-nil env map merges those vars on top of envFn's output
	// (last write wins), letting the hook redirect CODEX_HOME at a
	// generated config dir. Nil means the tool needs no pre-exec
	// setup beyond env vars.
	prepareFn func(apiBase, token string) (map[string]string, error)

	// transparentEnvFn supplies tool-specific placeholder credentials and
	// CA wiring for the experimental process-scoped connector. It must never
	// receive or return the EveryAPI relay key. A nil function means this tool
	// has not yet been verified with transparent mode.
	transparentEnvFn func(caPath string) (set map[string]string, unset []string)

	// prepareTransparentFn is the transparent counterpart of prepareFn. It
	// writes only public routing configuration and placeholder credentials;
	// the real relay key remains inside the connector process.
	prepareTransparentFn func() (map[string]string, error)
}

func (t *Tool) Env(apiBase, token string) map[string]string {
	return t.envFn(apiBase, token)
}

// InstallPromptDefault picks the press-Enter default for the
// "install <tool> now? [Y/n]" prompt. We default to Yes for routine
// package-manager installs (npm install -g …) and No for installers
// that pipe a remote shell script into bash — a single Enter
// shouldn't ever run untrusted code fetched at install time.
// InstallCmdUnixOnly happens to be exactly that signal today
// (claude's curl|bash is the only Unix-only installer in the
// Registry), so we reuse it. If that coupling breaks in the future,
// promote this to its own field.
func (t *Tool) InstallPromptDefault() bool {
	return !t.InstallCmdUnixOnly
}

// Prepare runs the tool's optional pre-exec setup (e.g. codex's
// CODEX_HOME / auth.json / config.toml). Returns env additions to
// overlay on top of Env(). nil map + nil error means "no setup
// needed". Errors abort the launch — failing to set up an isolated
// config is preferable to falling back to the user's real ~/.codex
// and silently going through ChatGPT auth.
func (t *Tool) Prepare(apiBase, token string) (map[string]string, error) {
	if t.prepareFn == nil {
		return nil, nil
	}
	return t.prepareFn(apiBase, token)
}

const transparentPlaceholderCredential = "everyapi-local-connector"

// TransparentEnv returns the complete process proxy overlay and the ambient
// variables that must be absent from the child environment. Keeping removals
// explicit is important: setting a Base URL to an empty string is still
// observably different from not setting it, and inherited real provider keys
// must not bypass or leak through the connector.
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
	// Do not inherit a corporate/plaintext proxy, a SOCKS fallback, or an
	// exclusion that could make an official model origin bypass Connector.
	unset := []string{"HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy", "EVERYAPI_RELAY_KEY"}
	specific, specificUnset := t.transparentEnvFn(caPath)
	for key, value := range specific {
		set[key] = value
	}
	unset = append(unset, specificUnset...)
	return set, unset, nil
}

// PrepareTransparent runs only setup that preserves the vendor's official
// origin. It never accepts the gateway base URL or relay credential by design.
func (t *Tool) PrepareTransparent() (map[string]string, error) {
	if t.transparentEnvFn == nil {
		return nil, fmt.Errorf("transparent mode is not supported for %s yet", t.Name)
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
			"ANTHROPIC_AUTH_TOKEN": transparentPlaceholderCredential,
			"NODE_EXTRA_CA_CERTS":  caPath,
			"ENABLE_TOOL_SEARCH":   "1",
		}, []string{
			"ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY",
			"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY",
		}
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

// joinBase concatenates the API base and a tool-specific suffix,
// avoiding double slashes. Centralized so adding a tool doesn't have
// to reinvent the join logic.
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

// claudeProviderPreset builds a Registry entry that launches the Claude
// Code binary (`claude`) routed at one upstream provider — the "drive a
// third-party model from Claude Code" shortcut (`everyapi use glm`,
// `everyapi use kimi`, …). `owner` is the provider's /v1/models
// `owned_by` value; cmd/use uses it to filter the live gateway catalog
// down to that provider's models and pop a picker, so the model is
// chosen at launch rather than hardcoded here. Each of these providers
// exposes Anthropic-compatible chat behind the gateway, so Claude Code
// runs unchanged once the base URL, relay token and model are set.
// Routing is by model name only — no group is pinned, matching plain
// `everyapi use claude`. Install / yolo behaviour is identical to the
// claude entry since the launched binary IS claude.
func claudeProviderPreset(name, owner string) *Tool {
	return &Tool{
		Name:               name,
		ExecName:           "claude",
		InstallHint:        "Install Claude Code: https://docs.claude.com/en/docs/claude-code/setup",
		InstallCmd:         "curl -fsSL https://claude.ai/install.sh | bash",
		InstallCmdUnixOnly: true,
		YoloFlag:           "--dangerously-skip-permissions",
		YoloLabel:          "skip all permission prompts (--dangerously-skip-permissions)",
		ModelOwner:         owner,
		transparentEnvFn:   transparentClaudeEnv,
		envFn: func(apiBase, token string) map[string]string {
			// Model is injected by cmd/use after the picker, see
			// SetClaudeModel — envFn carries only the routing essentials.
			return map[string]string{
				"ANTHROPIC_BASE_URL":   joinBase(apiBase, ""),
				"ANTHROPIC_AUTH_TOKEN": token,
				// Claude Code disables deferred MCP tool discovery when a
				// third-party ANTHROPIC_BASE_URL is present unless this opt-in
				// is set. Without it, every configured MCP schema is injected
				// into the first request (hundreds of thousands of tokens for
				// large Jira/Lark installations). Keep the native ToolSearch
				// behavior while routing through EveryAPI.
				"ENABLE_TOOL_SEARCH": "1",
				// Neutralise any ambient ANTHROPIC_API_KEY: Claude Code
				// prefers it over ANTHROPIC_AUTH_TOKEN, so a user who has
				// their real Anthropic key exported would otherwise send
				// it to the EveryAPI gateway (leaking the key to a third
				// party) and/or break relay auth. mergeEnv overlays this
				// empty value onto the child's env.
				"ANTHROPIC_API_KEY": "",
			}
		},
	}
}

// SetClaudeModel pins a resolved model into a Claude Code env map,
// pointing both the main model AND the background/"haiku" model at the
// same id. These providers ship a single flagship with no haiku-class
// small model, so if the background model is left at Claude Code's
// default (claude-3-5-haiku) its background tasks would call a model the
// gateway can't route and fail.
//
// ANTHROPIC_DEFAULT_HAIKU_MODEL is the current var for the background /
// `haiku` alias model; ANTHROPIC_SMALL_FAST_MODEL is its deprecated
// predecessor — we set both so old and new Claude Code versions are
// covered (each ignores the var it doesn't know).
func SetClaudeModel(env map[string]string, model string) {
	env["ANTHROPIC_MODEL"] = model
	env["ANTHROPIC_DEFAULT_HAIKU_MODEL"] = model
	env["ANTHROPIC_SMALL_FAST_MODEL"] = model
}

// Registry is the full set of supported tools, keyed by the name the
// user types (`everyapi use <name>`). Names are lowercase.
var Registry = map[string]*Tool{
	// Anthropic Claude Code: reads ANTHROPIC_BASE_URL and
	// ANTHROPIC_AUTH_TOKEN. The CLI sends the raw base URL — no
	// /v1 suffix — because Anthropic's official client appends its
	// own version path. Verified in Anthropic SDK source.
	"claude": {
		Name:               "claude",
		ExecName:           "claude",
		InstallHint:        "Install Claude Code: https://docs.claude.com/en/docs/claude-code/setup",
		InstallCmd:         "curl -fsSL https://claude.ai/install.sh | bash",
		InstallCmdUnixOnly: true,
		YoloFlag:           "--dangerously-skip-permissions",
		YoloLabel:          "skip all permission prompts (--dangerously-skip-permissions)",
		transparentEnvFn:   transparentClaudeEnv,
		envFn: func(apiBase, token string) map[string]string {
			return map[string]string{
				"ANTHROPIC_BASE_URL":   joinBase(apiBase, ""),
				"ANTHROPIC_AUTH_TOKEN": token,
				// Preserve Claude Code's deferred MCP ToolSearch behavior when
				// the CLI is pointed at the EveryAPI gateway.
				"ENABLE_TOOL_SEARCH":       "1",
				"ENABLE_PROMPT_CACHING_1H": "1",
				// See claudeProviderPreset: clear any ambient
				// ANTHROPIC_API_KEY so the user's real Anthropic key is
				// never forwarded to the gateway and can't shadow the
				// relay token.
				"ANTHROPIC_API_KEY": "",
			}
		},
	},

	// OpenAI Codex CLI: unlike claude/gemini, codex does NOT read
	// OPENAI_BASE_URL at runtime — its router is pinned to whatever
	// `model_provider` is set in ~/.codex/config.toml, and auth_mode
	// is pinned in ~/.codex/auth.json (defaults to "chatgpt" on a
	// fresh install, which then redirects to the ChatGPT login page
	// regardless of OPENAI_API_KEY). To make `everyapi use codex`
	// actually route through the gateway we redirect CODEX_HOME at
	// a generated config dir (see codex.go) where we write our own
	// auth.json (apikey mode) + config.toml (everyapi provider).
	// The OPENAI_API_KEY env var is still set as belt-and-suspenders
	// — config.toml's env_key points back at it.
	"codex": {
		Name:                 "codex",
		ExecName:             "codex",
		InstallHint:          "Install Codex CLI: https://github.com/openai/codex#installation",
		InstallCmd:           "npm install -g @openai/codex",
		YoloFlag:             "--dangerously-bypass-approvals-and-sandbox",
		YoloLabel:            "bypass approvals + sandbox (--dangerously-bypass-approvals-and-sandbox)",
		transparentEnvFn:     transparentCodexEnv,
		prepareTransparentFn: prepareCodexTransparent,
		envFn: func(_, token string) map[string]string {
			return map[string]string{"OPENAI_API_KEY": token}
		},
		prepareFn: prepareCodex,
	},

	// Google Gemini CLI: reads GEMINI_API_KEY (auth) and
	// GOOGLE_GEMINI_BASE_URL (alternate base). Gemini CLI appends the
	// native /v1beta API prefix itself, so this must be the gateway root.
	"gemini": {
		Name:                 "gemini",
		ExecName:             "gemini",
		InstallHint:          "Install Gemini CLI: https://github.com/google-gemini/gemini-cli#installation",
		InstallCmd:           "npm install -g @google/gemini-cli",
		YoloFlag:             "--yolo",
		YoloLabel:            "yolo mode — auto-approve every tool call (--yolo)",
		RequiredEndpoint:     "gemini",
		transparentEnvFn:     transparentGeminiEnv,
		prepareTransparentFn: prepareGeminiTransparent,
		envFn: func(apiBase, token string) map[string]string {
			return map[string]string{
				"GEMINI_API_KEY":         token,
				"GOOGLE_GEMINI_BASE_URL": joinBase(apiBase, ""),
			}
		},
		prepareFn: prepareGemini,
	},

	// Nous Research Hermes Agent (Python CLI, binary `hermes`). Unlike
	// claude/gemini, hermes does NOT read OPENAI_BASE_URL for its main
	// model — config.yaml is the single source of truth for the
	// endpoint, and OPENAI_API_KEY is only consulted when base_url's
	// host is openai.com (so it never attaches for an EveryAPI host).
	// So all routing goes through a generated config.yaml under an
	// isolated HERMES_HOME (see hermes.go): provider=custom pins
	// hermes at <apiBase>/v1, with the relay key inlined as
	// model.api_key. envFn therefore sets nothing — HERMES_HOME comes
	// from prepareFn. Yolo is env-based (HERMES_YOLO_MODE), not a flag.
	"hermes": {
		Name:               "hermes",
		ExecName:           "hermes",
		InstallHint:        "Install Hermes Agent: https://github.com/NousResearch/hermes-agent (or: pip install hermes-agent)",
		InstallCmd:         "curl -fsSL https://raw.githubusercontent.com/NousResearch/hermes-agent/main/scripts/install.sh | bash",
		InstallCmdUnixOnly: true,
		YoloEnv:            "HERMES_YOLO_MODE",
		YoloLabel:          "yolo mode — disable all approval prompts (HERMES_YOLO_MODE)",
		ModelEnv:           hermesModelEnv,
		envFn: func(_, _ string) map[string]string {
			// Routing is config-file driven; see prepareHermes.
			return map[string]string{}
		},
		prepareFn: prepareHermes,
	},

	// Claude Code presets that route the `claude` binary at a third-party
	// provider through EveryAPI. `everyapi use <name>` pops a picker over
	// that provider's models (filtered by the /v1/models `owned_by` value
	// below) and launches claude pinned to the chosen one. The owner
	// strings are the model BRAND the gateway reports in owned_by (derived
	// from the model id, not the channel). Older gateways reported the
	// channel-adaptor name instead ("ali" for qwen, "zhipu_4v" for glm);
	// cmd/use's legacyOwnerAliases tolerates both during rollout. Aggregator
	// channels are brand-narrow: `use byteplus` now lists only BytePlus-native
	// ids (dola-seed*/bytedance-seed*/ark-code*) — DeepSeek/Kimi/GPT-OSS served
	// via BytePlus report their own brand and are reached under those presets.
	"minimax":  claudeProviderPreset("minimax", "minimax"),
	"qwen":     claudeProviderPreset("qwen", "qwen"),
	"deepseek": claudeProviderPreset("deepseek", "deepseek"),
	"byteplus": claudeProviderPreset("byteplus", "byteplus"),
	"glm":      claudeProviderPreset("glm", "zhipu"),
	"kimi":     claudeProviderPreset("kimi", "moonshot"),
}

// Lookup returns the tool entry for `name`, or an error listing the
// supported names. cmd/use.go renders that error directly to the user.
func Lookup(name string) (*Tool, error) {
	t, ok := Registry[strings.ToLower(name)]
	if !ok {
		return nil, fmt.Errorf("unknown tool %q — supported: %s", name, strings.Join(Names(), ", "))
	}
	return t, nil
}

// Names returns the registered tool names in stable order. Used by
// the no-arg `everyapi use` interactive picker and by Lookup's error
// message.
func Names() []string {
	// Deterministic order matters for both the error message and the
	// picker UX. Hand-coded to match the ordering most likely to
	// reflect user demand: the standalone agents first (Claude, OpenAI,
	// Gemini, Hermes), then the Claude Code presets pinned to a
	// third-party flagship.
	return []string{
		"claude", "codex", "gemini", "hermes",
		"minimax", "qwen", "deepseek", "byteplus", "glm", "kimi",
	}
}
