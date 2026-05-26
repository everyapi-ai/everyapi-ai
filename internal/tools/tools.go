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

	// envFn builds the env vars from the resolved API base + access
	// token. Returns a map[name]value to merge into os.Environ before
	// exec. Implemented as a function (not a static map) because the
	// per-tool URL suffix varies (some take a v1 prefix, some don't).
	envFn func(apiBase, token string) map[string]string
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
		YoloFlag:           "--dangerously-skip-permissions",
		YoloLabel:          "skip all permission prompts (--dangerously-skip-permissions)",
		envFn: func(apiBase, token string) map[string]string {
			return map[string]string{
				"ANTHROPIC_BASE_URL":  joinBase(apiBase, ""),
				"ANTHROPIC_AUTH_TOKEN": token,
			}
		},
	},

	// OpenAI Codex CLI: reuses the OpenAI SDK env contract, so
	// OPENAI_BASE_URL + OPENAI_API_KEY. The /v1 suffix is required
	// because the OpenAI SDK does NOT append it.
	"codex": {
		Name:        "codex",
		ExecName:    "codex",
		InstallHint: "Install Codex CLI: https://github.com/openai/codex#installation",
		InstallCmd:  "npm install -g @openai/codex",
		YoloFlag:    "--dangerously-bypass-approvals-and-sandbox",
		YoloLabel:   "bypass approvals + sandbox (--dangerously-bypass-approvals-and-sandbox)",
		envFn: func(apiBase, token string) map[string]string {
			return map[string]string{
				"OPENAI_BASE_URL": joinBase(apiBase, "/v1"),
				"OPENAI_API_KEY":  token,
			}
		},
	},

	// Google Gemini CLI: reads GEMINI_API_KEY (auth) and
	// GOOGLE_GEMINI_BASE_URL (alternate base). The /v1beta suffix
	// matches Google's published path; EveryAPI's gemini relay is
	// mounted at the same path so a passthrough works.
	"gemini": {
		Name:        "gemini",
		ExecName:    "gemini",
		InstallHint: "Install Gemini CLI: https://github.com/google-gemini/gemini-cli#installation",
		InstallCmd:  "npm install -g @google/gemini-cli",
		YoloFlag:    "--yolo",
		YoloLabel:   "yolo mode — auto-approve every tool call (--yolo)",
		envFn: func(apiBase, token string) map[string]string {
			return map[string]string{
				"GEMINI_API_KEY":         token,
				"GOOGLE_GEMINI_BASE_URL": joinBase(apiBase, "/v1beta"),
			}
		},
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
	// reflect user demand (Claude first, then OpenAI, then Gemini).
	return []string{"claude", "codex", "gemini"}
}
