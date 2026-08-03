package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/internal/tools"
	"github.com/everyapi-ai/everyapi-sdk/api"
	"github.com/everyapi-ai/everyapi-sdk/config"
	"github.com/everyapi-ai/everyapi-sdk/sanitizer"
)

// useUsage is split so the prose can embed literal backticks (e.g.
// "`-- <flag>`") without colliding with the raw-string delimiter.
const useUsage = `everyapi use — launch a third-party CLI routed through EveryAPI

USAGE
  everyapi use [<tool>] [--group <name> | --channel <name>] [--model <id>] [--sanitize] [--transparent[=false]] [-- tool args...]

ARGUMENTS
  <tool>                 claude | codex | gemini | hermes
                         minimax | qwen | deepseek | byteplus | glm | kimi
                         The second row launches Claude Code against that
                         provider (routed via EveryAPI) and pops a picker over
                         the provider's models — or pass --model <id> to skip it.
                         Omit to open an interactive picker over installed tools.

FLAGS
  --group <name>         Relay via the key bound to that routing group.
  --channel <name>       Alias of --group.
  (bare --group/--channel, no value) → interactive picker over your
                         enabled keys' routing groups.
  --model <id>           hermes only: pin the upstream model, skipping the
                         picker. Omit on a TTY to choose from your model
                         catalog. claude/codex/gemini set their own model —
                         pass model flags to them after -- instead.
  --sanitize             Opt in to the local sanitizer proxy (masks detected secrets before they reach the gateway). Off by default — the mask/restore step corrupts coding-agent sessions; for non-agentic SDK traffic use the standalone 'everyapi proxy' instead.
  --transparent[=false]  Transparent mode keeps the tool on its vendor's
                         official API origin and relays registered model routes
                         through a process-scoped local TLS connector, so the
                         EveryAPI relay key never reaches the child's env or
                         config. ON BY DEFAULT for claude/codex and the Claude
                         presets. The gemini entry launches native agy instead.
                         Pass --transparent=false to fall back
                         to injecting the gateway Base URL + relay key.
                         hermes is EveryAPI-native (no vendor origin to keep)
                         and always uses the injected path.
  --                     End of everyapi's option parsing; remaining args are
                         forwarded verbatim to the tool's argv.

Safety preferences: on the first interactive launch, everyapi asks whether to
enable dangerous mode (and, for Codex, whether to bypass hook trust review).
Your choices are saved in settings.json and reused without prompting. The
prompt defaults to Yes, but no dangerous option is enabled before you confirm.

EXAMPLES
  everyapi use claude                  (transparent by default)
  everyapi use claude --transparent=false
  everyapi use codex --channel byteplus
  everyapi use claude -- --model opus
  everyapi use glm                     (Claude Code; pick a GLM model)
  everyapi use kimi --model kimi-k2.5  (Claude Code on Kimi, skip the picker)
  everyapi use hermes                  (pick a model interactively)
  everyapi use hermes --model gpt-5.1  (skip the picker)
`

// Use is the buyer onboarding bridge: verify credentials, configure
// the tool's env vars to point at EveryAPI, exec into the tool.
//
// Usage:
//
//	everyapi use claude
//	everyapi use codex
//	everyapi use gemini
//	everyapi use            (no arg → interactive picker over installed tools)
//	everyapi use claude --group byteplus   (relay through the key bound to
//	                              the "byteplus" group instead of the
//	                              newest enabled key; --channel is an
//	                              alias for --group)
//	everyapi use claude --channel  (bare --group/--channel, no value →
//	                              interactive picker over the routing
//	                              groups your enabled keys are bound to)
//	everyapi use claude --sanitize (opt in to the local sanitizer proxy,
//	                              which is off by default; it chains behind
//	                              the transparent connector)
//	everyapi use claude --transparent=false
//	                              (opt out of transparent mode, injecting the
//	                              gateway Base URL + relay key instead)
//	everyapi use claude -- --dangerously-skip-permissions
//	                              (everything after `--` is forwarded
//	                              verbatim to the tool's argv)
//
// Flags may appear before or after the tool name; a value attached
// with `=` (`--channel=byteplus`) is always explicit. Space form
// (`--channel team-a`) consumes the next token as the value unless
// it's another flag or a known tool name — so `everyapi use claude
// --channel` opens the picker while `--channel team-a claude` is
// explicit. A group literally named like a tool (claude/codex/gemini/
// hermes/minimax/qwen/deepseek/byteplus/glm/kimi) needs the `=` form
// when it appears before the tool positional. A bare `--` ends
// everyapi's option parsing; everything after is
// forwarded raw to the tool — use it for tool flags like claude's
// `--dangerously-skip-permissions` or codex's `--dangerously-bypass-*`.
func Use(args []string) error {
	if wantsUseHelp(args) {
		cliout.Println(useUsage)
		return nil
	}

	toolName, group, pickGroup, sanitize, transparentFlag, extraArgs, model, err := parseUseArgsWithTransparent(args)
	if err != nil {
		return err
	}

	creds, err := config.Load()
	if errors.Is(err, config.ErrNoCredentials) {
		return errors.New(i18n.T("auth.not_logged_in"))
	}
	if err != nil {
		return err
	}
	// The gateway to dial: settings.gateway_region is applied here (not just
	// at login) so `everyapi settings set gateway_region cn/global` takes
	// effect without a re-login. creds.APIBase stays the login value —
	// login is its only author — so the RelayKey cache Save below never
	// rewrites the stored api_base. A self-hosted --api-base survives
	// because ResolveAPIBaseForBase returns a non-official creds base as-is.
	gw := config.ResolveAPIBaseForBase(creds.APIBase)

	// OAuth2 (relay-key) logins carry no management credential, so the
	// per-group relay-key lookup (uncached ListTokens path) 401s and the
	// downstream handler mistranslates that into "session expired —
	// re-login", which re-login cannot fix. Refuse --group/--channel up
	// front with an actionable message. The default-group path works off
	// the cached relay key, so only guard when a group was explicitly
	// requested (flag value or the interactive picker).
	if creds.OAuthClientID != "" && (group != "" || pickGroup) {
		return errors.New(i18n.T("use.relay_key_mode_group"))
	}

	if toolName == "" {
		toolName, err = interactivePicker()
		if err != nil {
			return err
		}
	}

	if pickGroup {
		group, err = pickGroupInteractive(creds)
		if err != nil {
			return err
		}
	}

	t, err := tools.Lookup(toolName)
	if err != nil {
		return err
	}
	// Transparent mode is the default wherever a tool has an adapter for it:
	// the tool keeps talking to its vendor's official origin and the relay key
	// never reaches the child's env or config. A tool without an adapter has no
	// third-party origin to preserve (hermes is EveryAPI-native and routes at
	// <apiBase>/v1 by design), so it silently keeps the injected path —
	// defaulting must not break it. An explicit --transparent on such a tool
	// still fails: the user asked for something that cannot be delivered.
	transparent := t.SupportsTransparent()
	if transparentFlag != nil {
		if *transparentFlag && !t.SupportsTransparent() {
			return fmt.Errorf("transparent mode is not supported for %s", t.Name)
		}
		transparent = *transparentFlag
	}
	// Same principle as an unsupported tool, applied to an unsupported network.
	// When ALL_PROXY is the user's only proxy variable, neither side honors it:
	// http.ProxyFromEnvironment ignores ALL_PROXY so the connector dials direct,
	// and TransparentEnv strips it from the child. On a network where direct
	// egress is firewalled that hangs every request. Defaulting must not break a
	// setup that works today, so fall back to the injected path, where the child
	// reads ALL_PROXY itself exactly as before. An explicit --transparent still
	// fails loudly: silently doing something other than what was asked is worse.
	if transparent {
		if envVar := allProxyOnlyEgressVar(); envVar != "" {
			if transparentFlag != nil && *transparentFlag {
				return fmt.Errorf(
					"--transparent cannot use the proxy configured in %s: the local\n"+
						"connector resolves proxies the way Go does, which ignores %s. Set\n"+
						"HTTPS_PROXY instead, or drop --transparent to launch through the\n"+
						"gateway Base URL.", envVar, envVar)
			}
			cliout.Printf(
				"Note: %s is your only proxy setting, which the transparent connector does\n"+
					"not read — launching %s on the injected path instead.\n", envVar, t.ExecName)
			transparent = false
		}
	}

	// --model applies to tools EveryAPI picks a model for: hermes (ModelEnv)
	// and the Claude Code provider presets (ModelOwner, where --model skips
	// the picker). plain claude/codex/gemini default the model in their own
	// CLI — pass tool model flags after `--` for those.
	if model != "" && t.ModelEnv == "" && t.ModelOwner == "" {
		return fmt.Errorf(i18n.T("use.model_unsupported"), t.ExecName, t.ExecName)
	}

	// Preflight: if the tool's binary isn't on PATH, offer to run
	// its installer. This runs BEFORE the relay-key probe + sanitizer
	// boot so a buyer who's never installed the agent doesn't pay
	// for two network round-trips just to hit "not installed" at the
	// very end. Non-interactive callers (CI, piped stdin) get the
	// original ErrToolNotFound — we're not going to start writing
	// to npm's global prefix from a script that didn't ask for it.
	if err := ensureToolInstalled(t); err != nil {
		return err
	}
	if t.Native {
		if sanitize {
			return fmt.Errorf("--sanitize is not supported for native %s", t.ExecName)
		}
		return launchNativeTool(t, extraArgs)
	}

	// The device-auth access token can't relay (it's a management
	// credential); resolve the account's relay API key instead.
	relayKey, err := resolveRelayKey(creds, group)
	if err != nil {
		if errors.Is(err, errNoRelayKey) && group == "" {
			relayKey, err = createDefaultRelayKeyInteractive(creds)
		}
	}
	if err != nil {
		if errors.Is(err, errNoRelayKeyForGroup) {
			return fmt.Errorf(
				"no enabled relay API key in group %q on your account. Create an\n"+
					"API key assigned to that group in the EveryAPI dashboard (%s),\n"+
					"then run 'everyapi auth login' again — or drop --group/--channel to use\n"+
					"the default key.",
				group, api.WebOriginFromBase(gw))
		}
		if errors.Is(err, errNoRelayKey) {
			return fmt.Errorf(i18n.T("use.no_relay_key_long"), api.WebOriginFromBase(gw))
		}
		// A 401 here means the management token expired while resolving the
		// relay key (the uncached path calls ListTokens with it). Surface
		// the actionable "re-login" line instead of the raw "look up relay
		// API key: 401 …" wrap, which doesn't tell the user what to do.
		if api.IsUnauthorized(err) {
			return errors.New(i18n.T("auth.session_expired"))
		}
		return err
	}

	// Confirm the relay key actually works before exec'ing the tool.
	// /v1/models runs the same TokenAuth/ValidateUserToken as the
	// real traffic, so a 401 here means the tool would just loop on
	// "401 Invalid token" (invalid / expired / disabled / out of
	// quota key) with no hint why. Bail with an actionable message.
	// Only a definitive 401 is fatal: non-401 probe errors (network,
	// 5xx, the transient SystemPerformanceCheck gate) are left to the
	// tool's own retry — false-blocking a working setup on a flaky
	// probe is worse than letting it through.
	//
	// Provider presets always validate against the live /v1/models catalog
	// below, including an explicit --model, so a separate probe would be a
	// redundant round-trip.
	if t.ModelOwner == "" {
		client := api.New(gw, relayKey)
		var perr error
		if t.RequiredEndpoint != "" && toolInvocationNeedsEndpoint(extraArgs) {
			var catalog []api.RelayModel
			catalog, perr = client.RelayModelCatalog(cliout.WithCtx())
			if perr == nil && !catalogSupportsEndpoint(catalog, t.RequiredEndpoint) {
				return fmt.Errorf("no models available through the %s endpoint for this relay key; choose a compatible EveryAPI client or add a channel that supports this endpoint", t.RequiredEndpoint)
			}
		} else {
			perr = client.ProbeRelayToken(cliout.WithCtx())
		}
		if perr != nil && api.IsUnauthorized(perr) {
			invalidateCachedKeyOnReject(creds, group)
			return relayKeyRejectedErr(t.ExecName, gw)
		}
	}

	// Resolve the upstream model for tools EveryAPI picks for (hermes).
	// Sets t.ModelEnv in this process so the tool's prepareFn reads it
	// when generating its config. Runs after the relay-key probe so we
	// don't prompt for a model only to bail on a dead key, and uses the
	// relay key (not the management token) so the catalog is scoped to
	// what this key/group can actually reach. No-op for
	// claude/codex/gemini (ModelEnv == "").
	if err := resolveToolModel(t, creds, relayKey, model); err != nil {
		return err
	}

	// Claude Code provider presets (`everyapi use glm/kimi/…`) resolve
	// their model here: a picker scoped to the provider's catalog on a
	// TTY, or the --model flag. Done before the sanitizer spawns so we
	// fail fast (bad pick / empty catalog) without leaving a detached
	// proxy behind. The chosen id is injected into the tool env below.
	presetModel := ""
	if t.ModelOwner != "" {
		presetModel, err = resolveClaudePresetModel(t, creds, relayKey, model, group)
		if err != nil {
			return err
		}
	}

	// Claude resume preflight must run before proxy selection: a polluted
	// transcript is cloned under a fresh ID before the recovered process is
	// launched.
	claudeDir := ""
	var recovery *claudeSessionRecovery
	if t.ExecName == "claude" {
		claudeDir = os.Getenv("CLAUDE_CONFIG_DIR")
		if claudeDir == "" {
			if home, homeErr := os.UserHomeDir(); homeErr == nil {
				claudeDir = filepath.Join(home, ".claude")
			}
		}
		extraArgs, recovery = prepareClaudeSessionRecovery(extraArgs, claudeDir, newClaudeSessionID)
		// A launch abandoned after this point (guard startup failure, a
		// yolo-prompt cancel, a Prepare error) must not leave the freshly
		// minted clone behind as a phantom resumable session. The happy
		// path hands off via tools.Exec, which os.Exec/os.Exits and never
		// runs defers, so the clone survives exactly when the tool runs.
		defer recovery.discard()
		switch {
		case recovery == nil:
		case recovery.GuardOnly:
			cliout.Printf(i18n.T("use.claude_polluted_resume_selfrecovered")+"\n",
				recovery.NewSessionID,
				recovery.Pollution.FirstTimestamp,
			)
		case recovery.Pollution != nil:
			cliout.Printf(i18n.T("use.claude_polluted_resume_recovered")+"\n",
				recovery.OriginalSessionID,
				recovery.Pollution.FirstTimestamp,
				recovery.Pollution.AffectedMessages,
				recovery.NewSessionID,
			)
		default:
			cliout.Printf(i18n.T("use.claude_resume_redirected")+"\n",
				recovery.OriginalSessionID,
				recovery.NewSessionID,
			)
		}
		if tokens, inflated := claudeInflatedResume(extraArgs, claudeDir); inflated {
			cliout.Printf(i18n.T("use.claude_inflated_resume")+"\n", tokens)
		}
	}

	// Sanitizer integration. Off by default — opt in with --sanitize.
	// When on, the proxy intercepts the tool's traffic, masks sensitive
	// substrings before they reach the gateway, and restores them on the
	// way back. It stays off by default because the mask/restore step
	// corrupts coding-agent sessions: the model writes placeholders into
	// code/files that escape the HTTP round-trip and leak into the
	// transcript. 'everyapi proxy' is the home for non-agentic SDK traffic.
	//
	// The proxy runs IN THIS PROCESS (a goroutine on an ephemeral
	// loopback port), and `everyapi use` stays alive as the tool's
	// parent (see tools.Exec). So the proxy's lifetime is exactly the
	// tool's lifetime — no detached daemon, no pid file, no shared
	// instance, and therefore none of the cross-session orphaning that
	// a shared 127.0.0.1:8888 proxy suffered (kill the session that
	// spawned it and every other session got connection-refused).
	apiBaseForEnv := gw
	connectorUpstream := gw
	guardClaudeRecovery := recovery != nil
	if sanitize || guardClaudeRecovery {
		proxyAddr, stop, perr := startInProcessSanitizer(gw, sanitize, guardClaudeRecovery)
		if perr != nil {
			if guardClaudeRecovery {
				return fmt.Errorf("start Claude recovery response guard: %w", perr)
			}
			cliout.Printf(i18n.T("use.sanitizer_warn"), perr)
			cliout.Printf(i18n.T("use.fallback_direct"), gw)
			cliout.Printf("%s", i18n.T("use.fallback_hint"))
		} else {
			// Both launch paths route relayed traffic through the sanitizer, so
			// its mask/restore and the Claude recovery response guard apply
			// regardless of transparent mode. The injected path points the tool
			// straight at it; the transparent path keeps the tool on the vendor
			// origin and makes the connector relay THROUGH it
			// (child -> connector -> sanitizer -> gateway). Chaining rather than
			// porting the guard into the connector keeps one implementation of
			// the SSE transform instead of two that drift.
			apiBaseForEnv = proxyAddr
			connectorUpstream = proxyAddr
			// Tear the in-process proxy down if Use returns BEFORE handing
			// off to tools.Exec — a yolo-prompt cancel/EOF, a Prepare
			// error, etc. (defers are function-scoped, so this fires on
			// any such return). On the happy path tools.Exec os.Exits,
			// which skips defers, so the proxy lives for the tool's life.
			defer stop()
		}
	}

	var (
		env                map[string]string
		unsetEnv           []string
		transparentSession *transparentConnectorSession
	)
	if transparent {
		// connectorUpstream may be the sanitizer hop; gw is always the
		// real gateway, which is what the connector's loop guard must validate.
		launch, launchErr := startTransparentLaunch(t, connectorUpstream, gw, relayKey)
		if launchErr != nil {
			return fmt.Errorf("start transparent mode for %s: %w", t.ExecName, launchErr)
		}
		transparentSession = launch.session
		env = launch.env
		unsetEnv = launch.unsetEnv
		// Covers prompt cancellation and prepare failures. On a successful
		// launch ExecWithOptions also runs stop before propagating the child's
		// exit code; stop is idempotent.
		defer transparentSession.stop()
	} else {
		env = t.Env(apiBaseForEnv, relayKey)
	}

	// Pin the provider preset's chosen model. Injected into the env map
	// (not os.Setenv) so it overrides any ANTHROPIC_MODEL the user has
	// exported globally — mergeEnv lets the map win over os.Environ.
	if presetModel != "" {
		tools.SetClaudeModel(env, presetModel)
	}

	settings, err := config.LoadSettings()
	if err != nil {
		return err
	}
	interactive := cliprompt.IsInteractive()
	if t.Name == "codex" && !containsFlag(extraArgs, codexHookTrustBypassFlag) {
		enable, prefErr := resolveLaunchPreference(
			settings.CodexHookTrustBypass,
			interactive,
			func() (bool, error) {
				return cliprompt.YesNo(
					bufio.NewReader(os.Stdin),
					i18n.T("use.hook_trust_prompt"),
					true,
				)
			},
			func(value bool) error {
				settings.CodexHookTrustBypass = boolPointer(value)
				return config.SaveSettings(settings)
			},
		)
		if prefErr != nil {
			return prefErr
		}
		if enable {
			extraArgs = append([]string{codexHookTrustBypassFlag}, extraArgs...)
		}
	}

	// Dangerous-mode prompt. Each tool exposes a single "skip
	// every confirmation" switch — an argv flag (Tool.YoloFlag,
	// claude/codex/gemini) or an env var (Tool.YoloEnv, hermes'
	// HERMES_YOLO_MODE). If the user hasn't already passed the flag
	// via `-- <flags>`, use the persisted choice or ask once on a TTY.
	// The prompt defaults to YES, but the mode stays disabled until the
	// user confirms and the choice is saved.
	yoloAlreadyPassed := t.YoloFlag != "" && containsFlag(extraArgs, t.YoloFlag)
	if (t.YoloFlag != "" || t.YoloEnv != "") && !yoloAlreadyPassed {
		enable, perr := resolveLaunchPreference(
			settings.DangerousMode,
			interactive,
			func() (bool, error) {
				return cliprompt.YesNo(
					bufio.NewReader(os.Stdin),
					fmt.Sprintf(i18n.T("use.yolo_prompt"), t.YoloLabel),
					true,
				)
			},
			func(value bool) error {
				settings.DangerousMode = boolPointer(value)
				return config.SaveSettings(settings)
			},
		)
		if perr != nil {
			return perr
		}
		if enable {
			if t.YoloFlag != "" {
				// Prepend so a user-passed flag after `--` still
				// wins on conflict (last-flag wins in Go's flag and
				// in claude/codex/gemini's argv parsing alike).
				extraArgs = append([]string{t.YoloFlag}, extraArgs...)
			}
			if t.YoloEnv != "" {
				// Set before Prepare()'s overlay, which only adds
				// HERMES_HOME and won't clobber this.
				env[t.YoloEnv] = "1"
			}
		} else if t.YoloEnv != "" {
			// Declined. An env-var tool (hermes) may already have the yolo
			// var exported in the parent environment; mergeEnv passes every
			// ambient var through unless we override it, so without this the
			// pre-exported HERMES_YOLO_MODE=1 would reach the child and yolo
			// would stay ON — the opposite of the user's choice. Force it
			// empty (off under any value-based parse: == "1", != "", ParseBool).
			// Flag tools need no counterpart — their bypass is only ever
			// added to argv on yes.
			env[t.YoloEnv] = ""
		}
	}

	// Per-tool pre-exec setup — codex writes an isolated CODEX_HOME and
	// Gemini writes an auth-mode settings overlay. Transparent mode uses
	// separate hooks that retain the official provider/origin. Runs
	// AFTER the yolo prompt so a user who Escs out doesn't pay for
	// a file write that's about to be orphaned. Returned env
	// overlays t.Env so the hook can pin CODEX_HOME.
	var extraEnv map[string]string
	if transparent {
		extraEnv, err = t.PrepareTransparent()
	} else {
		extraEnv, err = t.Prepare(apiBaseForEnv, relayKey)
	}
	if err != nil {
		return fmt.Errorf("prepare %s: %w", t.ExecName, err)
	}
	for k, v := range extraEnv {
		env[k] = v
	}

	// Surface the resolved base URL so an aspiring debugger knows
	// where the requests are heading. One line, just before we hand the
	// terminal over to the tool.
	if transparent {
		// Print the real topology. connectorUpstream is the sanitizer when the
		// chain is engaged, and hardcoding gw here advertised a
		// direct hop that no longer existed.
		if connectorUpstream != gw {
			cliout.Printf("Launching %s through transparent connector %s → %s → %s\n",
				t.ExecName, transparentSession.proxyURL, connectorUpstream, gw)
		} else {
			cliout.Printf("Launching %s through transparent connector %s → %s\n",
				t.ExecName, transparentSession.proxyURL, gw)
		}
	} else if apiBaseForEnv != gw {
		cliout.Printf(i18n.T("use.launching_via")+"\n", t.ExecName, apiBaseForEnv, gw)
	} else {
		cliout.Printf(i18n.T("use.launching")+"\n", t.ExecName, gw)
	}
	// Discard any terminal control-sequence reply (e.g. the OSC 11
	// background-color report a huh picker triggered) still buffered on
	// stdin, so it doesn't leak into the launched tool as phantom input.
	cliprompt.DrainStdin()
	if transparent {
		return tools.ExecWithOptions(t, tools.ExecOptions{
			Env:      env,
			UnsetEnv: unsetEnv,
			Args:     extraArgs,
			Cleanup:  transparentSession.stop,
		})
	}
	return tools.Exec(t, env, extraArgs)
}

func launchNativeTool(t *tools.Tool, args []string) error {
	if t.YoloFlag != "" && !containsFlag(args, t.YoloFlag) {
		settings, err := config.LoadSettings()
		if err != nil {
			return err
		}
		enable, err := resolveLaunchPreference(
			settings.DangerousMode,
			cliprompt.IsInteractive(),
			func() (bool, error) {
				return cliprompt.YesNo(
					bufio.NewReader(os.Stdin),
					fmt.Sprintf(i18n.T("use.yolo_prompt"), t.YoloLabel),
					true,
				)
			},
			func(value bool) error {
				settings.DangerousMode = boolPointer(value)
				return config.SaveSettings(settings)
			},
		)
		if err != nil {
			return err
		}
		if enable {
			args = append([]string{t.YoloFlag}, args...)
		}
	}
	cliout.Printf("Launching native %s (Antigravity authentication)\n", t.ExecName)
	cliprompt.DrainStdin()
	return tools.Exec(t, map[string]string{}, args)
}

// createDefaultRelayKeyInteractive is the first-run repair path for
// device-auth users who can manage their account but have not created a
// relay API key yet. Scripts keep the explicit error; an interactive shell can
// mint a default key and continue the launch in the same command.
func createDefaultRelayKeyInteractive(creds *config.Credentials) (string, error) {
	if !cliprompt.IsInteractive() {
		return "", errNoRelayKey
	}
	ok, err := cliprompt.YesNo(
		bufio.NewReader(os.Stdin),
		i18n.T("use.create_relay_key_prompt"),
		true,
	)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errors.New(i18n.T("common.canceled"))
	}
	return createDefaultRelayKey(creds)
}

func createDefaultRelayKey(creds *config.Credentials) (string, error) {
	name := "everyapi-cli-" + time.Now().Format("20060102-150405")
	client := api.ForCredentials(creds)
	req := api.TokenCreate{
		Name:           name,
		ExpiredTime:    api.TokenExpiresNever,
		UnlimitedQuota: true,
	}
	if err := client.CreateToken(cliout.WithCtx(), req); err != nil {
		if api.IsUnauthorized(err) {
			return "", errors.New(i18n.T("auth.session_expired"))
		}
		return "", fmt.Errorf(i18n.T("use.create_relay_key_failed"), err)
	}
	cliout.Printf(i18n.T("use.create_relay_key_created")+"\n", name)
	key, err := resolveRelayKey(creds, "")
	if err != nil {
		if api.IsUnauthorized(err) {
			return "", errors.New(i18n.T("auth.session_expired"))
		}
		return "", fmt.Errorf(i18n.T("use.create_relay_key_resolve_failed"), err)
	}
	return key, nil
}

const codexHookTrustBypassFlag = "--dangerously-bypass-hook-trust"

func boolPointer(value bool) *bool { return &value }

// resolveLaunchPreference distinguishes an absent preference from an explicit
// false. First interactive use asks and persists; scripts default safely off.
func resolveLaunchPreference(
	stored *bool,
	interactive bool,
	ask func() (bool, error),
	persist func(bool) error,
) (bool, error) {
	if stored != nil {
		return *stored, nil
	}
	if !interactive {
		return false, nil
	}
	value, err := ask()
	if err != nil {
		return false, err
	}
	if err := persist(value); err != nil {
		return false, err
	}
	return value, nil
}

// containsFlag reports whether the user already passed `flag` in
// `--`-trailing args. Matches both `--foo` and `--foo=value` forms.
// Cheap linear scan — extraArgs is at most a handful of tokens.
func containsFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag || strings.HasPrefix(a, flag+"=") {
			return true
		}
	}
	return false
}

const claudeInflatedSessionThreshold = 100_000

// claudeInflatedResume returns the first cache-creation size recorded for an
// explicitly resumed Claude session. Claude stores sessions below
// <config>/projects/<encoded-cwd>/<uuid>.jsonl; searching by UUID also handles
// sessions created from a different working directory. Read failures are
// deliberately non-fatal: this is a diagnostic warning, never a launch gate.
func claudeInflatedResume(args []string, claudeDir string) (int64, bool) {
	sessionID := ""
	for i, arg := range args {
		switch {
		case arg == "--resume" || arg == "-r":
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				sessionID = args[i+1]
			}
		case strings.HasPrefix(arg, "--resume="):
			sessionID = strings.TrimPrefix(arg, "--resume=")
		}
	}
	if sessionID == "" || strings.ContainsAny(sessionID, `/\\`) || claudeDir == "" {
		return 0, false
	}
	paths, err := filepath.Glob(filepath.Join(claudeDir, "projects", "*", sessionID+".jsonl"))
	if err != nil || len(paths) == 0 {
		return 0, false
	}
	f, err := os.Open(paths[0])
	if err != nil {
		return 0, false
	}
	defer f.Close()

	type record struct {
		Message struct {
			Usage struct {
				CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
			} `json:"usage"`
		} `json:"message"`
	}
	scanner := bufio.NewScanner(f)
	// Tool results can make individual transcript records much larger than the
	// Scanner default, even though the first usage record is usually tiny.
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var rec record
		if json.Unmarshal(scanner.Bytes(), &rec) != nil {
			continue
		}
		if tokens := rec.Message.Usage.CacheCreationInputTokens; tokens > 0 {
			return tokens, tokens >= claudeInflatedSessionThreshold
		}
	}
	return 0, false
}

// wantsUseHelp reports whether argv asks for `everyapi use` help.
//
// Stops at `--` so `everyapi use claude -- --help` still forwards
// --help to the underlying tool. Skips the value token after a
// space-form --group/--channel so a routing group literally named
// "help" isn't hijacked as a usage request. Bare `help` only counts
// as a help command before the tool positional appears — after that
// it's just a stray arg and `parseUseArgs` will surface the real
// "two positionals" error.
func wantsUseHelp(args []string) bool {
	toolSeen := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return false
		}
		if a == "--help" || a == "-h" {
			return true
		}
		if a == "--group" || a == "-group" || a == "--channel" || a == "-channel" || a == "--model" || a == "-model" {
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
			}
			continue
		}
		if a == "help" && !toolSeen {
			return true
		}
		if !strings.HasPrefix(a, "-") {
			toolSeen = true
		}
	}
	return false
}

// startInProcessSanitizer launches the privacy sanitizer proxy as a
// goroutine in THIS process and returns the loopback URL the tool should
// use as its API base. Because the proxy shares our process and
// `everyapi use` stays alive as the tool's parent (see tools.Exec), the
// proxy lives for exactly the tool's lifetime: when the tool exits,
// tools.Exec calls os.Exit and the goroutine — and its listener — go
// with it. No detached daemon, no pid file, no shared instance, so the
// whole cross-session orphaning class is gone.
//
// Returns an error (caller then falls back to launching directly against
// the gateway) if the loopback listener can't be bound, the detector
// config won't load, the proxy can't be constructed, or it doesn't come
// up healthy in time — a launch with no privacy filter beats no launch
// at all. Every failure path releases the listener and log handle so a
// fall-back-to-direct session leaks neither.
//
// On success it also returns a stop func the caller MUST arrange to run
// if it abandons the launch (see the call site's defer) — that's what
// releases the goroutine, the bound port, and the log fd on a
// fall-back-to-direct or an early return before tools.Exec.
func startInProcessSanitizer(upstream string, sanitizeRequests, guardClaudeRecovery bool) (string, func(), error) {
	// Bind the loopback listener OURSELVES and hold it. Owning the port
	// end-to-end (rather than picking a free port, closing it, and letting
	// the server re-bind) closes the TOCTOU window: no other process can
	// grab the port in between, so the readiness probe below can only ever
	// reach our own proxy — never a foreign sanitizer that would silently
	// route this session's traffic (and relay key) through another
	// account.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, fmt.Errorf("bind loopback listener: %w", err)
	}
	listen := ln.Addr().String()

	detectors := []sanitizer.Detector{}
	if sanitizeRequests {
		fc, configErr := sanitizer.LoadFileConfig()
		if configErr != nil {
			_ = ln.Close()
			return "", nil, fmt.Errorf("load sanitizer config: %w", configErr)
		}
		detectors = fc.BuildDetectors()
	}
	logger, closeLog := sanitizerLogger()
	// Construct synchronously so a bad config (e.g. a non-loopback /
	// malformed upstream) surfaces to the caller as a real error instead
	// of vanishing into the background goroutine's log file.
	srv, err := sanitizer.New(sanitizer.Config{
		Listen:                    listen,
		UpstreamBase:              upstream,
		Detectors:                 detectors,
		Logger:                    logger,
		GuardClaudeToolCorruption: guardClaudeRecovery,
	})
	if err != nil {
		_ = ln.Close()
		closeLog()
		return "", nil, fmt.Errorf("construct sanitizer: %w", err)
	}

	// Serve on the listener we own, for the life of this process.
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = srv.Serve(context.Background(), ln) // owns ln; closes it on return
	}()
	// stop unwinds everything: close the listener to make Serve return,
	// wait for the goroutine, then close the log fd. Idempotent enough for
	// our use (a second ln.Close is a harmless error). The happy path
	// never calls it — tools.Exec os.Exits and the goroutine/listener/fd
	// are reclaimed by process exit.
	stop := func() {
		_ = ln.Close()
		<-served
		closeLog()
	}

	// Hand the URL over only once the proxy is actually serving, so the
	// tool doesn't race its first request into a connection-refused.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if sanitizerHealthy(listen) {
			return "http://" + listen, stop, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Didn't come up in time: tear down (otherwise a fall-back-to-direct
	// session would leak the goroutine, the bound port, and the fd for its
	// whole, possibly hours-long, duration).
	stop()
	return "", nil, fmt.Errorf("sanitizer did not become healthy on %s within 2s", listen)
}

// sanitizerLogger returns a logger writing to
// ~/.config/everyapi/sanitizer.log (appended) — the same place the
// standalone `everyapi proxy` writes, so proxy events stay in one spot
// for the diagnose tooling — plus a closer for the underlying file. It
// MUST NOT log to stderr: that stream is shared with the interactive
// tool's TUI and would corrupt the display. Falls back to discarding
// (and a no-op closer) on any error.
//
// The caller closes the file on a startup-failure path; on success the
// handle is owned by the long-lived proxy goroutine and reclaimed when
// the process exits.
func sanitizerLogger() (*log.Logger, func()) {
	discard := func() (*log.Logger, func()) { return log.New(io.Discard, "", 0), func() {} }
	// EnsureLogPath resolves ~/.config/everyapi and creates it if this is
	// the first command to need it — otherwise a fresh install (where the
	// dir doesn't exist yet) drops every proxy log line on the floor.
	path, err := config.EnsureLogPath("sanitizer.log")
	if err != nil {
		return discard()
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return discard()
	}
	return log.New(f, "", log.LstdFlags), func() { _ = f.Close() }
}

// sanitizerHealthy is a 250ms probe to /__sanitizer/health. Returns
// true only on a 200 response — anything else (network error, 404
// from some unrelated service occupying the port, 5xx) is treated as
// "not our proxy".
func sanitizerHealthy(listen string) bool {
	client := &http.Client{Timeout: 250 * time.Millisecond}
	resp, err := client.Get("http://" + listen + "/__sanitizer/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return false
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16))
	return strings.TrimSpace(string(body)) == "ok"
}

// parseUseArgs splits `everyapi use` argv into the tool name, the
// optional routing-group selector, and any args meant for the
// underlying tool. Hand-rolled instead of stdlib flag because flag
// stops at the first positional (so `everyapi use claude --channel`
// would never see the flag) and can't express "flag present but
// valueless" (the picker trigger).
//
// --group / --channel (and single-dash forms) are aliases. `=value` is
// an explicit value; an empty `=` or a bare flag opens the picker.
// Space form consumes the next token as the value unless it's another
// flag or a known tool name we still need.
//
// A bare `--` is the standard Unix end-of-options marker: every
// remaining token is appended to extraArgs verbatim and forwarded
// to the tool. Without `--`, unknown flags are an error — that's the
// typo-catching surface we don't want to give up just to spare users
// two characters when they want to pass `--dangerously-skip-permissions`.
func parseUseArgs(args []string) (toolName, group string, pickGroup, sanitize bool, extraArgs []string, model string, err error) {
	knownTool := func(s string) bool { _, e := tools.Lookup(s); return e == nil }

	var positional []string
	groupSeen := false
	groupHasVal := false
	groupFlagName := ""

	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			// End of everyapi's option parsing — forward the rest raw.
			if i+1 < len(args) {
				extraArgs = append(extraArgs, args[i+1:]...)
			}
			break
		}
		if !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
			continue
		}
		name := strings.TrimLeft(a, "-")
		val := ""
		hasEq := false
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			val, name, hasEq = name[eq+1:], name[:eq], true
		}
		switch name {
		case "sanitize":
			// Opt in to the local sanitizer proxy (masks detected
			// secrets before they reach the gateway). Off by default —
			// the mask/restore step leaks placeholders into coding-agent
			// sessions and corrupts them; non-agentic SDK traffic should
			// use the standalone 'everyapi proxy' instead.
			//
			// Honor an attached value so the standard `-flag=false`
			// bool convention works (`--sanitize=false` must DISABLE,
			// not silently enable). A bare `--sanitize` opts in.
			if hasEq {
				b, err := strconv.ParseBool(val)
				if err != nil {
					return "", "", false, false, nil, "", fmt.Errorf(i18n.T("use.sanitize_bad_value"), val)
				}
				sanitize = b
				continue
			}
			sanitize = true
		case "model":
			// Pin the upstream model for model-selectable tools
			// (hermes), skipping the interactive picker. Validated
			// against the tool's capability after Lookup. `=value`
			// or space form; a bare/empty --model is an error since
			// "no value" already has a meaning (the picker) reached
			// by simply omitting the flag. The space form won't eat a
			// not-yet-seen tool name (`--model hermes`) — that token
			// is the tool, leaving --model dangling (the error path).
			if hasEq {
				if val == "" {
					return "", "", false, false, nil, "", errors.New(i18n.T("use.model_needs_value"))
				}
				model = val
				continue
			}
			if i+1 < len(args) {
				nx := args[i+1]
				if !strings.HasPrefix(nx, "-") && !(knownTool(nx) && len(positional) == 0) {
					model = nx
					i++
					continue
				}
			}
			return "", "", false, false, nil, "", errors.New(i18n.T("use.model_needs_value"))
		case "group", "channel":
			if groupFlagName != "" && groupFlagName != name {
				return "", "", false, false, nil, "", errors.New("--group and --channel are aliases; use only one")
			}
			groupFlagName = name
			groupSeen = true
			if hasEq {
				if val != "" {
					group, groupHasVal = val, true
				}
				continue
			}
			if i+1 < len(args) {
				nx := args[i+1]
				if !strings.HasPrefix(nx, "-") && !(knownTool(nx) && len(positional) == 0) {
					group, groupHasVal = nx, true
					i++
				}
			}
		default:
			return "", "", false, false, nil, "", fmt.Errorf(i18n.T("use.unknown_flag"), a, a)
		}
	}

	if len(positional) > 1 {
		return "", "", false, false, nil, "", errors.New(i18n.T("use.usage"))
	}
	if len(positional) == 1 {
		toolName = positional[0]
	}
	pickGroup = groupSeen && !groupHasVal
	return toolName, group, pickGroup, sanitize, extraArgs, model, nil
}

// parseUseArgsWithTransparent layers the transparent flag onto the stable
// parser without changing its long-standing return contract.
// Tokens after `--` are never inspected, so a tool may receive a flag with the
// same name verbatim. Like Go boolean flags, the supported forms are a bare
// flag and an attached =true/false value; repeated flags use the last value.
//
// transparent is tri-state: nil means the user said nothing, so Use applies the
// per-tool default. A non-nil value is an explicit request, which Use honors
// even when that means erroring on a tool with no transparent adapter. The
// distinction matters now that transparent is the default — "unset" must fall
// back silently where the mode does not apply, while an explicit --transparent
// on the same tool must still fail loudly.
func parseUseArgsWithTransparent(args []string) (toolName, group string, pickGroup, sanitize bool, transparent *bool, extraArgs []string, model string, err error) {
	filtered := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			filtered = append(filtered, args[i:]...)
			break
		}
		// Only a token that begins with a dash can be the --transparent
		// flag. A bare value — a positional tool name, or the space-form
		// value of --group/--channel/--model — must pass through verbatim,
		// even when it literally reads "transparent"; strings.TrimLeft
		// returns such a token unchanged, so matching it here would eat a
		// routing group named "transparent" (and silently flip on the
		// connector) or swallow a --model value.
		if strings.HasPrefix(a, "-") {
			name := strings.TrimLeft(a, "-")
			if name == "transparent" {
				enabled := true
				transparent = &enabled
				continue
			}
			if strings.HasPrefix(name, "transparent=") {
				value := strings.TrimPrefix(name, "transparent=")
				parsed, parseErr := strconv.ParseBool(value)
				if parseErr != nil {
					return "", "", false, false, nil, nil, "", fmt.Errorf("--transparent=%q is not a valid true/false value", value)
				}
				transparent = &parsed
				continue
			}
		}
		filtered = append(filtered, a)
	}
	toolName, group, pickGroup, sanitize, extraArgs, model, err = parseUseArgs(filtered)
	return
}

// resolveToolModel pins the upstream model for tools EveryAPI selects a
// model for (those with Tool.ModelEnv set — hermes today). It exports
// the resolved id into t.ModelEnv in THIS process so the tool's
// prepareFn reads it when generating config. Precedence:
//
//  1. --model <id> (explicit flag) → use it, no prompt.
//  2. t.ModelEnv already set in the environment → respect it, no prompt.
//  3. interactive TTY → model picker over the live gateway catalog.
//  4. non-interactive with nothing set → deterministically select the
//     first chat-capable model in that same live catalog.
//
// A no-op for claude/codex/gemini, whose CLIs default the model
// themselves and route it by name through the gateway.
func resolveToolModel(t *tools.Tool, creds *config.Credentials, relayKey, modelFlag string) error {
	if t.ModelEnv == "" {
		return nil
	}
	if modelFlag != "" {
		return os.Setenv(t.ModelEnv, modelFlag)
	}
	if os.Getenv(t.ModelEnv) != "" {
		return nil // explicit env override; respect it
	}
	if !cliprompt.IsInteractive() {
		models, err := toolChatModels(t, creds, relayKey)
		if err != nil {
			return err
		}
		return os.Setenv(t.ModelEnv, preferredToolModel(models))
	}
	chosen, err := pickModelInteractive(t, creds, relayKey)
	if err != nil {
		return err
	}
	if chosen != "" {
		return os.Setenv(t.ModelEnv, chosen)
	}
	return nil
}

// toolChatModels returns the chat-capable model ids the relay key can
// actually route to. A missing catalog is fatal because ModelEnv tools
// have no vendor-side model default; callers can always bypass catalog
// discovery explicitly with --model or the tool's model environment.
func toolChatModels(t *tools.Tool, creds *config.Credentials, relayKey string) ([]string, error) {
	gw := config.ResolveAPIBaseForBase(creds.APIBase)
	catalog, err := api.New(gw, relayKey).RelayModelCatalog(cliout.WithCtx())
	if err != nil {
		return nil, fmt.Errorf("could not resolve a model for %s from the live catalog (%w); pass --model <id> to select one explicitly", t.ExecName, err)
	}
	models := chatModels(catalog, t.RequiredEndpoint)
	if len(models) == 0 {
		return nil, fmt.Errorf("no chat-capable models are reachable for %s with this key/group; add a compatible channel or pass --model <id>", t.ExecName)
	}
	return models, nil
}

func preferredToolModel(models []string) string {
	if last := tools.LastHermesModel(); last != "" {
		for _, model := range models {
			if model == last {
				return last
			}
		}
	}
	return models[0]
}

// relayKeyRejectedErr is the actionable message shown when EveryAPI
// 401s a relay key. Shared by the pre-launch probe and the preset
// catalog fetch (both hit /v1/models with the same TokenAuth), so a
// dead key gives identical "check / top up / refresh" guidance wherever
// it's first noticed.
func relayKeyRejectedErr(execName, apiBase string) error {
	wallet := api.WebOriginFromBase(apiBase) + "/wallet"
	return fmt.Errorf(
		"EveryAPI rejected the relay API key — not launching %s, it would just\n"+
			"loop on 401. The key is invalid, expired, disabled, or out of quota.\n"+
			"  check:    everyapi auth status\n"+
			"  top up:   %s\n"+
			"  refresh:  everyapi auth login",
		execName, wallet)
}

// invalidateCachedKeyOnReject clears the cached default-group relay key
// after the pre-launch relay check came back a definitive 401, so the
// next `everyapi use` re-resolves to a live sibling token instead of
// re-handing-out the dead cached key (the bug this guards: a token
// disabled/revoked/expired server-side leaves the default-group cache
// pointing at a key the gateway now rejects, with nothing to teach it
// otherwise).
//
// Gated on group == "": only the default-group path caches its key. A
// --group/--channel key is resolved fresh and never cached, so its
// rejection must NOT wipe the unrelated (possibly still-valid) default
// cache. The on-disk clear is best-effort — the user is being told to
// re-login/refresh anyway (which rewrites credentials.json) — so a Save
// failure is surfaced as a warning rather than masking the real
// "key rejected" error we're about to return.
func invalidateCachedKeyOnReject(creds *config.Credentials, group string) {
	if group != "" {
		return
	}
	unlock, lockErr := acquireCredentialLock()
	if lockErr != nil {
		fmt.Fprintln(os.Stderr, "warning: could not lock credentials.json:", lockErr)
		return
	}
	defer unlock()
	latest, loadErr := config.Load()
	if loadErr != nil || latest.RelayKey != creds.RelayKey {
		return
	}
	creds = latest
	if err := api.InvalidateCachedRelayKey(creds); err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not clear the rejected relay key from credentials.json:", err)
	}
}

// resolveClaudePresetModel picks the upstream model for a Claude Code
// provider preset (Tool.ModelOwner set — glm/kimi/minimax/…). Precedence:
//
//  1. --model <id> (explicit flag) → use it, no picker.
//  2. interactive TTY → picker over the provider's models, filtered from
//     the live gateway catalog by the model's `owned_by`.
//  3. non-interactive with no --model → error: a script must name the
//     model so we never silently pick on the user's behalf.
//
// Unlike resolveToolModel (hermes), an empty/failed catalog is FATAL
// here: the whole point of `everyapi use glm` is to run a glm model, so
// silently falling back to Claude Code's default Anthropic model would
// be a worse surprise than a clear "no glm models reachable" error.
func resolveClaudePresetModel(t *tools.Tool, creds *config.Credentials, relayKey, modelFlag, group string) (string, error) {
	gw := config.ResolveAPIBaseForBase(creds.APIBase)
	if !cliprompt.IsInteractive() {
		if modelFlag == "" {
			return "", fmt.Errorf(i18n.T("use.preset_needs_model"), t.Name)
		}
	}
	catalog, err := api.New(gw, relayKey).RelayModelCatalog(cliout.WithCtx())
	if err != nil {
		// This fetch doubles as the relay-key probe for presets (Use
		// skips the standalone probe when we'll reach here), so a 401
		// gets the same actionable "dead key" guidance — and, when the
		// rejected key was the default-group cache, clears it so the
		// next run re-resolves to a live token instead of re-handing-out
		// the dead one.
		if api.IsUnauthorized(err) {
			invalidateCachedKeyOnReject(creds, group)
			return "", relayKeyRejectedErr(t.ExecName, gw)
		}
		return "", fmt.Errorf(i18n.T("use.preset_catalog_failed"), t.Name, err)
	}
	if modelFlag != "" {
		if err := validateClaudePresetModel(t, catalog, modelFlag); err != nil {
			return "", err
		}
		return modelFlag, nil
	}
	ids := providerChatModels(catalog, t.ModelOwner)
	if len(ids) == 0 {
		return "", fmt.Errorf(i18n.T("use.preset_no_models"),
			t.Name, api.WebOriginFromBase(gw))
	}
	idx, err := cliprompt.Pick(fmt.Sprintf(i18n.T("use.model_picker"), t.Name), ids)
	if err != nil {
		return "", err
	}
	return ids[idx], nil
}

func validateClaudePresetModel(t *tools.Tool, catalog []api.RelayModel, model string) error {
	for _, id := range providerChatModels(catalog, t.ModelOwner) {
		if id == model {
			return nil
		}
	}
	return fmt.Errorf("model %q is not currently reachable for %s through this relay key", model, t.Name)
}

// chatCapableEndpoints are the GET /v1/models `supported_endpoint_types`
// a Claude Code preset can actually drive — text chat over the
// OpenAI/Anthropic/Gemini wire. Image, embeddings, rerank and video
// models share a provider's owned_by (MiniMax's speech-02/image-01, for
// one) but Claude Code can't use them, so the preset picker hides them.
var chatCapableEndpoints = map[string]bool{
	"openai":                  true,
	"openai-response":         true,
	"openai-response-compact": true,
	"anthropic":               true,
	"gemini":                  true,
}

// Some dedicated media endpoints also advertise "openai" because their wire
// shape belongs to the OpenAI API family (for example gpt-image via
// /v1/images). The dedicated endpoint is the stronger signal: these models
// cannot be sent to /chat/completions even though "openai" is also present.
var nonChatEndpoints = map[string]bool{
	"audio-speech":     true,
	"embeddings":       true,
	"image-generation": true,
	"jina-rerank":      true,
	"openai-video":     true,
}

// legacyOwnerAliases tolerates gateways that haven't shipped the
// owned_by de-channelization. A preset owner is the model BRAND the
// gateway now reports (e.g. "zhipu", "qwen"); an older gateway instead
// reports the channel-adaptor name ("zhipu_4v", "ali"). Accepting both
// lets `use glm`/`use qwen` work whichever the gateway emits, so the CLI
// can roll out ahead of the backend. Remove once every gateway reports
// brands.
var legacyOwnerAliases = map[string][]string{
	"zhipu": {"zhipu_4v"},
	"qwen":  {"ali"},
}

// ownerMatches reports whether a model's owned_by belongs to the preset
// owner — the brand slug itself or any legacy channel-name alias.
func ownerMatches(ownedBy, owner string) bool {
	if strings.EqualFold(ownedBy, owner) {
		return true
	}
	for _, alias := range legacyOwnerAliases[owner] {
		if strings.EqualFold(ownedBy, alias) {
			return true
		}
	}
	return false
}

// providerChatModels filters a relay catalog down to the chat-capable
// model ids owned by `owner` (brand slug, case-insensitive; legacy
// channel names tolerated via ownerMatches), sorted. A model that
// declares no endpoint types is kept (fail-open) so missing metadata
// never hides an otherwise-valid chat model — only a model that
// explicitly serves ONLY non-chat endpoints is dropped.
func providerChatModels(catalog []api.RelayModel, owner string) []string {
	var ids []string
	for _, m := range catalog {
		if !ownerMatches(m.OwnedBy, owner) {
			continue
		}
		if !chatCapable(m.SupportedEndpointTypes) {
			continue
		}
		ids = append(ids, cliout.Sanitize(m.ID))
	}
	sort.Strings(ids)
	return ids
}

// chatModels returns all unique chat-capable ids in lexical order. When a
// tool declares a required wire endpoint, models that only expose a different
// chat protocol are excluded; missing metadata still fails open for older
// gateways.
func chatModels(catalog []api.RelayModel, requiredEndpoint string) []string {
	seen := make(map[string]struct{}, len(catalog))
	ids := make([]string, 0, len(catalog))
	for _, model := range catalog {
		if !chatCapable(model.SupportedEndpointTypes) {
			continue
		}
		if requiredEndpoint != "" && !supportsEndpoint(model.SupportedEndpointTypes, requiredEndpoint) {
			continue
		}
		id := cliout.Sanitize(model.ID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func supportsEndpoint(types []string, required string) bool {
	if types == nil {
		return true
	}
	for _, endpoint := range types {
		if strings.EqualFold(endpoint, required) {
			return true
		}
	}
	return false
}

// chatCapable reports whether a model serving these endpoint types can
// be driven as a Claude Code chat model. A nil slice means an older gateway
// omitted the field and remains fail-open; a non-nil empty slice is an
// explicit statement that this key has no callable protocol for the model.
func chatCapable(types []string) bool {
	if types == nil {
		return true
	}
	for _, endpoint := range types {
		if nonChatEndpoints[strings.ToLower(endpoint)] {
			return false
		}
	}
	for _, endpoint := range types {
		if chatCapableEndpoints[strings.ToLower(endpoint)] {
			return true
		}
	}
	return false
}

// catalogSupportsEndpoint reports whether at least one routable model can
// serve the wire protocol required by a tool. Missing endpoint metadata fails
// open for compatibility with older gateways; an empty catalog or a catalog
// where every model explicitly declares only other endpoints fails closed.
func catalogSupportsEndpoint(catalog []api.RelayModel, endpoint string) bool {
	for _, model := range catalog {
		if model.SupportedEndpointTypes == nil {
			return true
		}
		for _, supported := range model.SupportedEndpointTypes {
			if strings.EqualFold(supported, endpoint) {
				return true
			}
		}
	}
	return false
}

func toolInvocationNeedsEndpoint(args []string) bool {
	if len(args) == 0 {
		return true
	}
	switch args[0] {
	case "--help", "-h", "help", "--version", "-v", "version":
		return false
	default:
		return true
	}
}

// pickModelInteractive lists the chat-capable models the relay key can route to
// (GET /v1/models with the relay key — group-scoped, so the picker only
// offers models the launched tool will really reach) and asks the user
// to pick one. The cursor defaults to the model pinned on the last
// launch when it's still offered. A catalog-fetch failure or an empty
// catalog is fatal because ModelEnv tools have no hidden built-in model
// fallback; --model remains the explicit offline/script escape hatch.
func pickModelInteractive(t *tools.Tool, creds *config.Credentials, relayKey string) (string, error) {
	models, err := toolChatModels(t, creds, relayKey)
	if err != nil {
		return "", err
	}
	// Default the cursor to last launch's model when it's still in the
	// catalog. LastHermesModel is hermes-specific, which is fine while
	// hermes is the only ModelEnv tool; generalize if that changes.
	initial := 0
	if last := tools.LastHermesModel(); last != "" {
		for i, m := range models {
			if m == last {
				initial = i
				break
			}
		}
	}
	idx, err := cliprompt.PickWithSelected(
		fmt.Sprintf(i18n.T("use.model_picker"), t.ExecName),
		models, initial)
	if err != nil {
		return "", err
	}
	return models[idx], nil
}

// pickGroupInteractive lists the distinct routing groups the account's
// ENABLED relay tokens are bound to and asks the user to pick one. The
// buyer CLI has no channel-listing endpoint (that's admin-only), so
// "available channels" is necessarily expressed as the groups the user
// already holds a key for. The empty group (default tokens) shows as
// "(default — newest enabled key)" and selecting it returns "" — the
// normal newest-enabled-key path.
func pickGroupInteractive(creds *config.Credentials) (string, error) {
	client := api.ForCredentials(creds)
	tokens, err := client.ListTokens(cliout.WithCtx())
	if err != nil {
		return "", fmt.Errorf("list tokens for the group picker: %w", err)
	}
	seen := map[string]bool{}
	var groups []string
	for i := range tokens {
		if tokens[i].Status != api.TokenStatusEnabled {
			continue
		}
		if g := tokens[i].Group; !seen[g] {
			seen[g] = true
			groups = append(groups, g)
		}
	}
	if len(groups) == 0 {
		return "", errors.New(i18n.T("use.no_relay_keys"))
	}
	labels := make([]string, len(groups))
	for i, g := range groups {
		if g == "" {
			labels[i] = "(default — newest enabled key)"
		} else {
			labels[i] = cliout.Sanitize(g)
		}
	}
	idx, err := cliprompt.Pick(i18n.T("use.group_picker"), labels)
	if err != nil {
		return "", err
	}
	return groups[idx], nil
}

// ensureToolInstalled is the preflight gate between Lookup and the
// network probes. If the tool is already on PATH it's a no-op. If
// it's missing and the session is non-interactive (CI / piped
// stdin), or the tool has no usable auto-installer for this
// platform, the caller sees the original ErrToolNotFound — same
// behavior as before this helper existed. If it's missing AND we
// have a TTY AND CanAutoInstall returns true, we surface a yes/no
// confirm. Default is YES for routine package-manager installs and
// NO for installers that pipe a remote shell script into bash —
// pressing Enter shouldn't ever run untrusted code fetched at
// install time. On Yes we stream the installer's output to the
// terminal so npm / curl progress reaches the user live, then
// return nil so the caller proceeds to launch.
//
// `ErrInstalledButNotOnPath` from RunInstall is translated here so
// the user gets a localized "installed but not on PATH — open a new
// shell" message instead of the raw English error type.
func ensureToolInstalled(t *tools.Tool) error {
	if tools.IsInstalled(t) {
		return nil
	}
	if !cliprompt.IsInteractive() || !tools.CanAutoInstall(t) {
		return &tools.ErrToolNotFound{Tool: t}
	}
	// The auto-installer shells out to a package manager (npm) through a
	// non-interactive shell that doesn't source the user's rc files. If
	// that command isn't resolvable on PATH — the classic case being a
	// version-manager npm exposed only as a shell function — offering the
	// install just yields a cryptic "npm: command not found". Catch it
	// here and tell the user exactly what to install first.
	if missing := tools.InstallerMissing(t); missing != "" {
		return fmt.Errorf(i18n.T("use.installer_missing"), t.ExecName, missing, t.InstallHint)
	}
	cliout.Printf(i18n.T("use.tool_not_installed")+"\n", t.ExecName)
	cliout.Printf("  %s\n", t.InstallCmd)
	ok, err := cliprompt.YesNo(
		bufio.NewReader(os.Stdin),
		fmt.Sprintf(i18n.T("use.install_prompt"), t.Name),
		t.InstallPromptDefault(),
	)
	if err != nil {
		// Esc / Ctrl-C from the confirm propagates as cancellation
		// so the launcher loop catches it. ensureToolInstalled is
		// only reached after IsInteractive() — a TTY won't EOF on
		// read — so we don't carve out an EOF special case here.
		return err
	}
	if !ok {
		return &tools.ErrToolNotFound{Tool: t}
	}
	cliout.Printf(i18n.T("use.installing")+"\n", t.Name)
	if err := tools.RunInstall(t); err != nil {
		var notOnPath *tools.ErrInstalledButNotOnPath
		if errors.As(err, &notOnPath) {
			if len(notOnPath.Dirs) > 0 {
				return fmt.Errorf(i18n.T("use.installed_not_on_path_dirs"),
					notOnPath.Tool.ExecName, strings.Join(notOnPath.Dirs, ", "))
			}
			return fmt.Errorf(i18n.T("use.installed_not_on_path"), notOnPath.Tool.ExecName)
		}
		return err
	}
	cliout.Printf(i18n.T("use.installed")+"\n", t.Name)
	return nil
}

// interactivePicker is the no-arg fallback. Renders the registered
// tools as an arrow-navigable list when run on a TTY; falls back to
// a numbered prompt otherwise (CI / piped input).
func interactivePicker() (string, error) {
	names := tools.Names()
	idx, err := cliprompt.Pick(i18n.T("use.tool_picker"), names)
	if err != nil {
		return "", err
	}
	return names[idx], nil
}

// allProxyOnlyEgressVar names the environment variable that leaves this process
// with an egress route neither the connector nor the child will honor under
// transparent mode, or "" when transparent mode can still reach the network. It
// exists because the connector and the child resolve proxies differently, and
// transparent mode silently strands anyone in the gap.
//
// The connector dials only https under transparent mode: its relay leg dials the
// https gateway (roundTrip forces the upstream scheme) and its pass-through
// CONNECT tunnels https. Go's http.ProxyFromEnvironment is per-scheme and reads
// HTTPS_PROXY — not HTTP_PROXY — for an https target, and never reads ALL_PROXY
// (both verified empirically). So from the connector's vantage only HTTPS_PROXY
// is usable, and HTTP_PROXY and ALL_PROXY are both inert. What each means:
//
//   - HTTPS_PROXY (any scheme, incl. socks5) is honored by the connector's relay
//     leg — net/http dials socks5/socks5h proxy URLs natively — so it rescues
//     the launch and is not reported.
//   - HTTP_PROXY is inert for the connector's https legs, so it must not count as
//     connector-usable: it cannot rescue a launch (so it must not suppress an
//     ALL_PROXY that would), and reporting it would needlessly divert an
//     otherwise-fine transparent launch onto the injected path, writing the
//     relay key into the child. So a lone HTTP_PROXY returns "" (stay
//     transparent): the common non-firewalled launch works with no key leak.
//     Known narrow limitation: on a network where direct egress is firewalled
//     and HTTP_PROXY is the only variable, the connector cannot reach the
//     gateway — set HTTPS_PROXY, the correct variable for https egress. (Whether
//     the injected path would have fared better there is child-dependent: a
//     catch-all client like gaxios reads HTTP_PROXY for https, a per-scheme one
//     like reqwest does not — so falling back is not a reliable rescue, and it
//     always leaks the key.)
//   - ALL_PROXY is the real gap: http.ProxyFromEnvironment never reads it so the
//     connector dials direct, while TransparentEnv strips it from the child. But
//     ALL_PROXY is a catch-all by definition — a user who set it means it for all
//     traffic — and ALL_PROXY-only leaves the connector with no usable proxy at
//     all, so fall back to the injected path where the child reads ALL_PROXY
//     itself. That is the author's existing bet; the point here is only that an
//     inert HTTP_PROXY must not pre-empt it.
//
// The earlier version listed HTTP_PROXY alongside HTTPS_PROXY as "a proxy the
// connector reads". It reads neither for an https dial, and treating HTTP_PROXY
// as connector-usable let an HTTP_PROXY set beside an ALL_PROXY short-circuit
// the ALL_PROXY fallback: the launch stayed transparent, the connector dialed
// direct, and a user who had a working catch-all proxy hung instead of falling
// back to the injected path that would have used it.
func allProxyOnlyEgressVar() string {
	// Only HTTPS_PROXY rescues transparent mode (the sole traffic is https), and
	// it does so whatever its scheme — an earlier version refused a socks5
	// HTTPS_PROXY on the false premise that net/http could not speak socks, which
	// downgraded a working, secure setup to the injected path and wrote the real
	// relay key into the child env. HTTP_PROXY is deliberately absent: it applies
	// only to http targets and so neither rescues nor strands an https launch.
	for _, name := range []string{"HTTPS_PROXY", "https_proxy"} {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return "" // the connector has a proxy variable it will actually read
		}
	}
	// ALL_PROXY as the only usable variable strands the launch: neither the
	// connector nor the transparent child reads it. Report it so Use falls back
	// to the injected path.
	for _, name := range []string{"ALL_PROXY", "all_proxy"} {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return name
		}
	}
	return ""
}
