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

	// Native launches a client that owns its own authentication and upstream
	// routing. Use must not resolve or expose an EveryAPI relay key for it.
	Native bool

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

	// ExtraBinDirs lists installer-specific directories to search for
	// ExecName when $PATH misses, as $HOME-relative paths ("" segments
	// and absolute paths are ignored). The npm-global fallback in
	// resolveExecDirs only covers `npm install -g` tools; an installer
	// that writes somewhere else — Antigravity's install.sh drops `agy`
	// into ~/.local/bin — needs its own candidates, or a user whose
	// shell adds that dir in an rc file everyapi never sources stays
	// permanently "not installed" and loops on reinstall offers.
	//
	// Same contract as InstallCmd: compile-time literals only. These
	// paths are joined onto the user's home dir and stat'd, never
	// executed as shell text, but keeping them static preserves the
	// "no user input reaches tool resolution" property.
	ExtraBinDirs []string

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

	// FlagProbeArgs is an argv tail that makes this tool parse its
	// command line, report the result through its exit code, and then do
	// nothing else — codex's `exec --help` prints usage and exits 0
	// without starting a session, reading stdin, or firing hooks.
	//
	// It enables FlagProbe (see flagprobe.go), which runs the tool once
	// with a candidate flag prepended to decide whether adding that flag
	// would abort the launch. That question only has an empirical answer:
	// the binary on $PATH may be a wrapper that prepends flags of its own,
	// and a flag the tool declares non-repeatable then arrives twice.
	//
	// SAFETY CONTRACT: these args must be observably inert — no session,
	// no stdin read, no network, no writes, no hooks. They run on every
	// launch of a tool that declares them. Same compile-time-literal rule
	// as InstallCmd and ExtraBinDirs. Empty disables probing entirely,
	// which means every flag EveryAPI wants to add is added unexamined.
	FlagProbeArgs []string

	// ModelEnv names the env var the tool's prepareFn reads to pin the
	// upstream model. Set only for tools that don't carry their own
	// vendor-default model and therefore need EveryAPI to choose one —
	// Hermes reads EVERYAPI_HERMES_MODEL, Qwen Code reads OPENAI_MODEL,
	// and Kimi Code reads KIMI_MODEL_NAME. When
	// non-empty, 'everyapi use' offers a model picker (populated from
	// the gateway's model catalog) before launch and honors a
	// `--model <id>` flag. Empty for claude/codex/gemini/grok, whose own
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
	prepareFn        func(apiBase, token string) (map[string]string, error)
	prepareCatalogFn func(apiBase, token string, models []Model, bootModel string) (map[string]string, error)

	// transparentEnvFn supplies tool-specific placeholder credentials and
	// CA wiring for the process-scoped connector. It must never
	// receive or return the EveryAPI relay key. A nil function means this tool
	// has not yet been verified with transparent mode.
	transparentEnvFn func(caPath string) (set map[string]string, unset []string)

	// prepareTransparentFn is the transparent counterpart of prepareFn. It
	// writes only public routing configuration and placeholder credentials;
	// the real relay key remains inside the connector process.
	prepareTransparentFn        func() (map[string]string, error)
	prepareTransparentCatalogFn func(models []Model, bootModel string) (map[string]string, error)
}

// Model is the launch-safe subset of the relay model catalogue. Credentials
// never enter this value; prepare hooks may persist it for a client's native
// /model picker without writing the relay key alongside it.
type Model struct {
	ID                     string
	DisplayName            string
	OwnedBy                string
	SupportedEndpointTypes []string
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

// ignoreBootModel adapts a catalog hook that has no boot-model concept. The
// ModelEnv tools pin their model through an env var their own prepare reads,
// so the selection EveryAPI records for claude/codex is meaningless to them.
func ignoreBootModel(fn func(apiBase, token string, models []Model) (map[string]string, error)) func(string, string, []Model, string) (map[string]string, error) {
	return func(apiBase, token string, models []Model, _ string) (map[string]string, error) {
		return fn(apiBase, token, models)
	}
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
			"ANTHROPIC_AUTH_TOKEN":                       transparentPlaceholderCredential,
			"NODE_EXTRA_CA_CERTS":                        caPath,
			"ENABLE_TOOL_SEARCH":                         "1",
			"CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY": "1",
			// Advisor is an experimental account-bound server tool. Disable it
			// when Claude runs through EveryAPI so rejected results cannot poison
			// the session and fail every subsequent prompt.
			"CLAUDE_CODE_DISABLE_ADVISOR_TOOL": "1",
		}, []string{
			"ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY",
			"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY",
		}
}

// transparentStandaloneClaudeEnv is the transparent overlay for the plain
// `everyapi use claude` tool. It layers ENABLE_PROMPT_CACHING_1H on top of the
// shared transparentClaudeEnv so standalone Claude keeps the 1h prompt-cache
// window its injected (non-transparent) path already sets (see the "claude"
// Registry envFn).
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
		// claude.ai/install.sh hands off to `<binary> install`, which links the
		// launcher into ~/.local/bin — the same off-PATH cohort gemini hits.
		ExtraBinDirs:     []string{".local/bin"},
		YoloFlag:         "--dangerously-skip-permissions",
		YoloLabel:        "skip all permission prompts (--dangerously-skip-permissions)",
		RequiredEndpoint: "anthropic",
		transparentEnvFn: transparentStandaloneClaudeEnv,
		envFn: func(apiBase, token string) map[string]string {
			return map[string]string{
				"ANTHROPIC_BASE_URL":   joinBase(apiBase, ""),
				"ANTHROPIC_AUTH_TOKEN": token,
				// Preserve Claude Code's deferred MCP ToolSearch behavior when
				// the CLI is pointed at the EveryAPI gateway.
				"ENABLE_TOOL_SEARCH":                         "1",
				"ENABLE_PROMPT_CACHING_1H":                   "1",
				"CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY": "1",
				"CLAUDE_CODE_DISABLE_ADVISOR_TOOL":           "1",
				// Clear any ambient ANTHROPIC_API_KEY so the user's real key is
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
		Name:        "codex",
		ExecName:    "codex",
		InstallHint: "Install Codex CLI: https://github.com/openai/codex#installation",
		InstallCmd:  "npm install -g @openai/codex",
		YoloFlag:    "--dangerously-bypass-approvals-and-sandbox",
		YoloLabel:   "bypass approvals + sandbox (--dangerously-bypass-approvals-and-sandbox)",
		// `exec --help` parses the full command line, prints usage, and
		// exits — no session, no stdin, no hooks. Codex needs the probe
		// more than any other tool here: its parser declares both
		// --dangerously-bypass-hook-trust and
		// --dangerously-bypass-approvals-and-sandbox non-repeatable, so a
		// wrapper that injects either one turns EveryAPI's copy into a
		// launch-aborting parse error.
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

	// Antigravity CLI: keep `everyapi use gemini` as the familiar entry point,
	// but launch the locally authenticated `agy` client. agy owns its Google
	// OAuth credential and upstream routing, so never pass the EveryAPI relay
	// key or transparent-connector environment into the child.
	"gemini": {
		Name:     "gemini",
		ExecName: "agy",
		// Deliberately platform-neutral: this string is what Windows users
		// see (CanAutoInstall is false for them), and the docs page carries
		// the correct per-platform command. Inlining the Unix pipeline here
		// would hand PowerShell users a `curl` that resolves to
		// Invoke-WebRequest and fails.
		InstallHint: "Install the Antigravity CLI (`agy`): https://antigravity.google/docs/cli/install " +
			"— then sign in once before running `everyapi use gemini`.",
		// Antigravity's own installer, per the install docs above. It
		// writes the binary to ~/.local/bin/agy — hence ExtraBinDirs, so
		// the post-install re-check finds it even when the user's shell
		// only puts that dir on $PATH from an rc file we never source.
		//
		// Windows uses a separate .cmd/.ps1 installer and registers agy
		// under %LOCALAPPDATA%\agy\bin, so this pipeline is Unix-only.
		// That directory is env-var-derived rather than $HOME-relative, so
		// ExtraBinDirs cannot express it; Windows users whose PATH lacks it
		// still fall back to the hint above.
		InstallCmd:         "curl -fsSL https://antigravity.google/cli/install.sh | bash",
		InstallCmdUnixOnly: true,
		ExtraBinDirs:       []string{".local/bin"},
		Native:             true,
		YoloFlag:           "--dangerously-skip-permissions",
		YoloLabel:          "skip all permission prompts (--dangerously-skip-permissions)",
		envFn: func(_, _ string) map[string]string {
			return map[string]string{}
		},
	},

	// xAI Grok Build: GROK_MODELS_BASE_URL controls both inference and model
	// discovery on an OpenAI-compatible surface; XAI_API_KEY supplies its
	// bearer credential. prepareGrok isolates EveryAPI-routed configuration and
	// session history from the user's normal Grok profile.
	"grok": {
		Name:             "grok",
		ExecName:         "grok",
		InstallHint:      "Install Grok Build: https://docs.x.ai/build/overview (or: npm install -g @xai-official/grok)",
		InstallCmd:       "npm install -g @xai-official/grok",
		YoloFlag:         "--always-approve",
		YoloLabel:        "always approve tool executions (--always-approve)",
		RequiredEndpoint: "openai-response",
		envFn: func(apiBase, token string) map[string]string {
			base := joinBase(apiBase, "/v1")
			return map[string]string{
				"XAI_API_KEY":          token,
				"GROK_MODELS_BASE_URL": base,
			}
		},
		prepareFn: prepareGrok,
	},

	// Alibaba Qwen Code supports OpenAI-compatible providers through the
	// standard OPENAI_* environment variables. prepareQwenCode isolates its
	// launch state; cmd/use pins the OpenAI protocol at CLI precedence.
	"qwen-code": {
		Name:             "qwen-code",
		ExecName:         "qwen",
		InstallHint:      "Install Qwen Code: https://github.com/QwenLM/qwen-code#installation",
		InstallCmd:       "npm install -g @qwen-code/qwen-code@latest",
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

	// Moonshot Kimi Code can synthesize a temporary model entirely from the
	// KIMI_MODEL_* environment family. Use its OpenAI-compatible provider so
	// the selected EveryAPI catalogue model rides /v1/chat/completions.
	"kimi-code": {
		Name:             "kimi-code",
		ExecName:         "kimi",
		InstallHint:      "Install Kimi Code: https://github.com/MoonshotAI/kimi-code#install",
		InstallCmd:       "npm install -g @moonshot-ai/kimi-code",
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
		Name:        "hermes",
		ExecName:    "hermes",
		InstallHint: "Install Hermes Agent: https://github.com/NousResearch/hermes-agent (or: pip install hermes-agent)",
		// Pin the third-party script to an immutable commit. Updating Hermes
		// requires reviewing the new script and deliberately changing this SHA;
		// never point an auto-executed installer at main/master/HEAD.
		InstallCmd:         "curl -fsSL https://raw.githubusercontent.com/NousResearch/hermes-agent/e444d165807f489b5c1ab8e4a612c8d09c2e67a2/scripts/install.sh | bash",
		InstallCmdUnixOnly: true,
		// That script's get_command_link_dir() links the command into
		// $HOME/.local/bin for the default non-root install (/usr/local/bin
		// when run as root, which is already a conventional PATH entry).
		ExtraBinDirs:     []string{".local/bin"},
		YoloEnv:          "HERMES_YOLO_MODE",
		YoloLabel:        "yolo mode — disable all approval prompts (HERMES_YOLO_MODE)",
		ModelEnv:         hermesModelEnv,
		RequiredEndpoint: "openai",
		envFn: func(_, _ string) map[string]string {
			// Routing is config-file driven; see prepareHermes.
			return map[string]string{}
		},
		prepareFn:        prepareHermes,
		prepareCatalogFn: ignoreBootModel(prepareHermesWithModels),
	},
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
	// reflect user demand.
	return []string{
		"claude", "codex", "gemini", "grok", "qwen-code", "kimi-code", "hermes",
	}
}
