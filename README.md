> 🌐 **English** · [简体中文](translations/README.zh-CN.md) · [日本語](translations/README.ja.md) · [한국어](translations/README.ko.md) · [Español](translations/README.es.md) · [Deutsch](translations/README.de.md) · [Français](translations/README.fr.md)

# `everyapi` CLI

Buyer-onboarding CLI for the [EveryAPI](https://everyapi.ai) AI API gateway. Launch supported coding agents through one audited registry **in under a minute**.

Status: **core flows shipped** — buyer onboarding, seller commands (plain-key + OAuth across three providers), sanitizer proxy, QR sign-in main path, and anti-phishing layers are all in place. The only unimplemented items are OS-level code signing and a platform keychain backend (see "What this binary does NOT include yet" at the end).

## Installation

**macOS (Homebrew):**

```bash
brew tap everyapi-ai/tap && brew install everyapi
```

Later upgrades — `brew update` first (without it, `brew upgrade everyapi` uses the cached formula and reports "already installed" even when a newer release exists):

```bash
brew update && brew upgrade everyapi
```

**Linux / macOS (install script):**

```bash
curl -fsSL https://dl.everyapi.ai/install.sh | bash
```

The script auto-detects OS + arch, downloads the matching `everyapi_{os}_{arch}.tar.gz`, verifies the SHA256, and installs to `~/.local/bin` (or `/usr/local/bin` when run as root). If [cosign](https://github.com/sigstore/cosign) is installed it also verifies the keyless signature — pass `--require-signature` to make that step mandatory (recommended for CI / supply-chain-sensitive setups).

One command, worldwide: the script picks its download source at runtime — GitHub Releases where reachable, and a mainland-China mirror when GitHub is slow or blocked — so the same line installs from inside China as well as overseas. Set `EVERYAPI_DOWNLOAD_BASE` to force a specific mirror.

Common flags:

```bash
curl -fsSL https://dl.everyapi.ai/install.sh | bash -s -- --version v0.2.2     # pin a version
curl -fsSL https://dl.everyapi.ai/install.sh | bash -s -- --prefix /usr/local  # custom prefix
curl -fsSL https://dl.everyapi.ai/install.sh | bash -s -- --require-signature  # fail if cosign verify fails
curl -fsSL https://dl.everyapi.ai/install.sh | bash -s -- --force              # reinstall the same version
```

To upgrade later, re-run the same command. The script resolves the latest release tag and replaces the binary in place when a newer one exists; if the installed binary is already at the resolved target version, it exits with `already at vX.Y.Z — nothing to do` (safe to put in setup scripts / dotfiles). Pass `--force` to reinstall on top (useful for verifying integrity or recovering a damaged file). The script is also published in this repo at [`install.sh`](install.sh) if you'd rather download + read it first.

**Go users (`go install`):**

```bash
go install github.com/everyapi-ai/everyapi-ai/v3@latest
```

**Windows (PowerShell):**

```powershell
irm https://dl.everyapi.ai/install.ps1 | iex
```

Same flow as the shell script — resolves the latest tag, downloads `everyapi_windows_amd64.zip` + `SHA256SUMS`, verifies the hash (and the cosign signature when cosign is on `PATH`), installs `everyapi.exe` into `%LOCALAPPDATA%\everyapi\bin`, and adds it to your User `PATH`. To pin a version or pass other options, materialize the script first: `& ([scriptblock]::Create((irm https://dl.everyapi.ai/install.ps1))) -Version v0.2.2`. It's also published in this repo at [`install.ps1`](install.ps1).

**Windows (manual):** grab `everyapi_windows_amd64.zip` (or any other artifact) from the [Releases page](https://github.com/everyapi-ai/everyapi-ai/releases/latest) and verify against `SHA256SUMS` before placing the binary on `%PATH%`.

## Commands

| Command | Purpose |
|---|---|
| `everyapi auth login` | Sign in to EveryAPI on this device |
| `everyapi auth logout` | Clear local credentials |
| `everyapi auth status` | Show balance, usage, quota |
| `everyapi wallet topup` | Open the topup page (with anti-phishing phrase check) |
| `everyapi use <tool>` | Set env and exec into a third-party CLI (pointed at EveryAPI) |
| `everyapi seller <sub>` | Marketplace seller commands (list / withdraw / add-key / setup) |
| `everyapi edge <sub>` | One-command deploy for the BYO-GPU supplier agent (register / start / status / logs / models / stop / update / remove) |
| `everyapi computer <sub>` | Read and control local macOS app windows through Accessibility |
| `everyapi mcp` | Run as an MCP server (stdin/stdout JSON-RPC) |
| `everyapi update` | Check for new versions and print the upgrade command for your install method |
| `everyapi version` | Show build version |
| `everyapi help` | Help |

### `everyapi computer <sub>` — local macOS computer use

The macOS CLI can discover running apps and windows, return a bounded Accessibility snapshot, and perform semantic or coordinate actions. This surface is local-only and is not registered in `everyapi mcp`. Linux and Windows builds return `unsupported_platform` explicitly.

On macOS, `everyapi computer` drives a small, independently code-signed helper app (`EveryAPI Computer Use.app`, built from `clients/desktop/native/computer-use-macos`) over a local Unix socket, downloading and launching it automatically on first use if it is not already installed — including when EveryAPI Connect has already installed its own bundled copy, which this CLI reuses instead of downloading a second one. The helper reports screenshot support as false because macOS does not expose a reliable public window-scoped capture identifier through this provider; it never substitutes a screen-region capture that could contain an overlapping app.

```bash
everyapi computer capabilities --json
everyapi computer permissions --json
everyapi computer list-apps
everyapi computer list-windows --app com.apple.TextEdit
everyapi computer get-app-state --app com.apple.TextEdit --window-index 0 --json
everyapi computer click --app com.apple.TextEdit --window-index 0 --element-index 12
printf '%s' 'safe text' | everyapi computer set-value --app com.apple.TextEdit --window-index 0 --element-index 12 --value-stdin
everyapi computer hotkey --app com.apple.TextEdit --window-index 0 --key cmd+a
```

Run `everyapi computer permissions --json` and grant Accessibility to **EveryAPI Computer Use** — not to `everyapi`, `osascript`, or your terminal — under System Settings > Privacy & Security > Accessibility. Because the helper is its own signed app with its own bundle identity, that grant is scoped to this one capability: it does not also authorize every AppleScript or JXA script on the machine, and it survives CLI and helper updates. `permissions` reports Accessibility directly and Automation as `unknown`, since this provider does not depend on System Events and has no separate Automation preflight to run.

Element indexes come from the latest `get-app-state` snapshot and expire after two minutes. Windows are selected by index (`--window-index`) but identified internally by the real per-window id CoreGraphics assigns on screen where one is available, falling back to a snapshot-scoped synthetic id for minimized windows; either way the provider uses an internal fingerprint to detect observable changes, but public Accessibility attributes cannot prove that a replacement window or control with identical attributes is the same native instance. The cache stores only opaque application, process, window, path, role, frame, action-name, and fingerprint data under `~/.config/everyapi/computer-use/state/` with private permissions. Obtain a fresh snapshot after `app_stale`, `element_stale`, or `window_stale`. A successful GUI action remains successful when its best-effort state refresh fails; JSON then includes `refreshError` instead of returning a retryable action error. If the helper call is interrupted or returns an invalid receipt after an action is handed off, `action_outcome_unknown` means the action may already have occurred; refresh state before deciding whether to retry.

A maintained list of known terminal apps, password managers, Keychain Access, Passwords, System Settings, and EveryAPI Connect is blocked as defense-in-depth friction. Bundle-ID blocking is not a comprehensive application classifier: unlisted apps, editors with integrated terminals, browsers, and renamed or newly released apps may expose equivalent capabilities. The explicit `--app` target, macOS TCC, and the caller's same-user authority remain the real trust boundary. Observed text is stripped of terminal control sequences and scanned for credentials before output; typed or set text that matches the built-in secret detectors is rejected. Prefer `--text-stdin` and `--value-stdin` to keep ordinary text out of shell history.

### `everyapi use <tool>` — exec into a third-party CLI (pointed at the EveryAPI gateway)

The main reason to install this CLI. It configures and launches supported coding clients through EveryAPI. Native integrations (`antigravity`, `librefang`) keep their own authentication path and never receive a copied relay key.

```bash
everyapi use claude         # Claude Code → EveryAPI
everyapi use codex          # OpenAI Codex CLI → EveryAPI
everyapi use opencode       # OpenCode → process-scoped EveryAPI provider
everyapi use gemini         # Google Gemini CLI → EveryAPI
everyapi use antigravity    # Antigravity (native Google auth and routing)
everyapi use aider          # Aider → EveryAPI (pick a model)
everyapi use goose          # Goose CLI → EveryAPI (pick a model)
everyapi use crush          # Crush CLI → isolated EveryAPI catalog
everyapi use cline          # Cline CLI → lifecycle-bound provider settings
everyapi use openclaw       # OpenClaw local TUI → isolated EveryAPI catalog
everyapi use continue       # Continue CLI → isolated assistant config
everyapi use kilo           # Kilo Code CLI → process-scoped provider config
everyapi use pi             # Pi coding agent → isolated models catalog
everyapi use vibe           # Mistral Vibe → isolated generic provider
everyapi use copilot        # GitHub Copilot CLI → official process-scoped BYOK
everyapi use droid          # Factory Droid → isolated runtime settings
everyapi use openhands      # OpenHands CLI → explicit process-only env override
everyapi use forge          # ForgeCode → isolated OpenAI-compatible session
everyapi use llxprt         # LLxprt Code → isolated homes + pinned runtime flags
everyapi use grok           # xAI Grok Build → EveryAPI
everyapi use qwen-code      # Alibaba Qwen Code → EveryAPI (pick a model)
everyapi use kimi-code      # Moonshot Kimi Code → EveryAPI (pick a model)
everyapi use hermes         # Nous Research Hermes Agent → EveryAPI (pick a model)
everyapi use librefang      # LibreFang start (native EveryAPI credential process)
everyapi use hermes --model gpt-5.1   # pin the model, skip the picker
everyapi use claude                    # transparent by default: stays on api.anthropic.com
everyapi use codex                     # stays on api.openai.com
everyapi use antigravity               # stays on Google's official origin
everyapi use claude --transparent=false  # opt out: inject the gateway Base URL + relay key
everyapi use                # no arg → interactive picker over installed tools
```

Each tool uses different conventions; the CLI remembers them:

| Tool | How it's pointed at EveryAPI |
|---|---|
| claude | env: `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`; live compatible models through gateway discovery |
| codex | env: `OPENAI_API_KEY` + persistent EveryAPI `CODEX_HOME` for sessions + lifecycle-bound `--profile` and key-scoped model catalog (codex routes via config, not `OPENAI_BASE_URL`) |
| gemini | env: `GEMINI_API_KEY`, `GOOGLE_GEMINI_BASE_URL`, `GEMINI_MODEL`; isolated auth-mode settings overlay |
| antigravity | native Antigravity launcher (`agy`) |
| aider | OpenAI-compatible env plus `openai/<model>` LiteLLM model namespace |
| goose | `GOOSE_PROVIDER=openai`, `GOOSE_MODEL`, `OPENAI_API_KEY`, `OPENAI_BASE_URL` |
| crush | process-scoped `CRUSH_GLOBAL_CONFIG`; key referenced from env, live model catalog generated |
| cline | lifecycle-bound `CLINE_PROVIDER_SETTINGS_PATH` removed after exit |
| openclaw | local embedded TUI with process-scoped config and env-backed SecretRef |
| continue | lifecycle-bound `CONTINUE_GLOBAL_DIR/config.yaml`; env-backed Continue secret reference |
| kilo | process-scoped `KILO_CONFIG_CONTENT`; OpenCode-compatible provider with env-backed key |
| pi | isolated `PI_CODING_AGENT_DIR` containing `models.json` and selected-model settings, with existing `{extensions,skills,prompts,themes}` from the pre-launch `PI_CODING_AGENT_DIR` (default `~/.pi/agent`) loaded by absolute path |
| vibe | isolated `VIBE_HOME/config.toml`; generic provider with `api_key_env_var` |
| copilot | official `COPILOT_PROVIDER_*` BYOK environment; wire API follows the selected model capability |
| droid | official `--settings` runtime-only file with one `custom:EveryAPI-0` model and env-backed key |
| openhands | `--override-with-envs` plus process-only `LLM_API_KEY`, `LLM_BASE_URL`, and `LLM_MODEL` |
| forge | isolated `FORGE_CONFIG`; OpenAI-compatible provider/model pinned in config and process env |
| llxprt | isolated application homes plus reserved `--provider openai`, `--baseurl`, and `--model` runtime flags |
| grok | env: `XAI_API_KEY`, `GROK_MODELS_BASE_URL`; isolated `GROK_HOME`; filtered live model discovery |
| qwen-code | env: `OPENAI_API_KEY`, `OPENAI_BASE_URL`, `OPENAI_MODEL`; process-scoped `QWEN_HOME` user settings and pinned `--auth-type=openai` |
| kimi-code | env: `KIMI_MODEL_API_KEY`, `KIMI_MODEL_BASE_URL`, `KIMI_MODEL_PROVIDER_TYPE`, `KIMI_MODEL_NAME`; isolated `KIMI_CODE_HOME` with generated model aliases |
| hermes | generated `HERMES_HOME/config.yaml` (named custom provider, `base_url`, inline `api_key`); filtered live model discovery |
| librefang | native `librefang start`, which detaches the daemon and returns the terminal (`librefang stop` ends it); LibreFang resolves the current EveryAPI credential per request |

No more looking up which variable name each tool reads, whether you need to append `/v1`, or which auth-header style applies.

**relay key selection**: With no `--group`, a launch resolves the account's auto-group key — the one key that routes across every group you can reach — and caches it in `credentials.json`. An account without an auto key (or one whose tier may no longer use that group) falls back to its newest enabled key. Run `everyapi token switch` to pin a different key as the default, or pass `--group <id>` for a one-off launch through another pool; a group override is never written to that cache. Which key you are on decides the catalog below: a key pinned to one group only ever sees that group's models.

A key already cached from an earlier launch keeps being used — that lookup is deliberately offline, so it never re-picks on its own. If `/model` shows one group's models, run `everyapi token switch` and choose `Auto` once.

**model selection**: At launch, EveryAPI fetches the live catalog available to the selected relay key/group, removes incompatible media/embedding protocols, and injects the resulting snapshot into every routed client's native selector. Use `/model` in Claude Code, Codex, Qwen Code, or Kimi Code; use Grok's `/model`/`models` entry or `hermes model` for Hermes. Non-Claude model IDs are represented internally with Claude-compatible aliases but are displayed and sent upstream under their real IDs.

Tools with a `ModelEnv` contract (Gemini, Aider, Goose, Crush, Cline, OpenClaw, Continue, Kilo, Pi, Vibe, GitHub Copilot CLI, Factory Droid, OpenHands, ForgeCode, LLxprt, Hermes, Qwen Code, and Kimi Code) open EveryAPI's picker; pass `--model <id>` to skip it. In a non-interactive run EveryAPI deterministically uses the first compatible model. Plain claude/codex/grok still own their boot-model behavior. `antigravity` launches native `agy` with Google authentication and `librefang` uses its first-party EveryAPI credential process.

**reasoning level**: After the model, `everyapi use codex` and `everyapi use pi` ask which reasoning level to launch at, and remember the answer for the next launch — asked once, then reused without prompting, the same way as the safety preferences below. The two clients are gated differently because they know different things. Codex reads the levels its own bundled catalog publishes for that model (`low` … `ultra`, which differ per model — `gpt-5.6-sol` reaches `ultra`, `gpt-5.5` stops at `xhigh`) and receives the choice as `model_reasoning_effort`; it never consults the gateway for this, so a model Codex has not heard of gets no step. Pi has no per-model table for a custom provider, so its step appears only where the gateway has verified the model takes an effort (`supports_thinking` on `/v1/models`); it is offered `off` … `high` and receives the choice as `defaultThinkingLevel`. A remembered level the current model does not offer is dropped rather than pinned, and on the first launch after this shipped the cursor starts on the effort Codex already had in its persistent home, so accepting the default changes nothing. Both clients keep their own in-session controls — Codex's `/model`, pi's shift+tab — while the launcher retains the cross-launch choice because Codex's generated profile and Pi's isolated home are removed at exit.

Provider names are not CLI names: use `qwen-code` or `kimi-code` for those vendors' official clients, and select provider models from a supported client's live model catalog.

**hermes config isolation**: `everyapi use hermes` redirects `HERMES_HOME` to a process-scoped directory under `~/.config/everyapi/sessions`; its credential-bearing config and live proxy URL are removed at exit and cannot collide with another key/group. Only the last selected model ID is retained as a safe preference. Your personal `~/.hermes` remains untouched. The generated config registers EveryAPI as a named custom provider so `hermes model` can discover and switch models without falling back to OpenRouter. Bare `hermes` opens the interactive chat; pass `everyapi use hermes -- --tui` for the terminal UI.

**grok config isolation**: `everyapi use grok` redirects `GROK_HOME` to `~/.config/everyapi/grok-home`. This prevents a cached xAI browser session from overriding the EveryAPI relay key and keeps EveryAPI-routed sessions separate from plain `grok`. Pass Grok-specific flags after `--`, for example `everyapi use grok -- --model grok-4.5`.

**Qwen/Kimi config isolation**: each routed launch receives a process-scoped home under `~/.config/everyapi/sessions`, removed when the child exits, so concurrent keys/groups cannot overwrite one another's catalog or loopback URL. Qwen's real system settings remain untouched and retain administrator precedence. If administrator or workspace settings define `modelProviders.openai` (and would hide the live EveryAPI catalog), launch stops with an actionable conflict instead of silently showing stale/incompatible models.

> ⚠️ **Subprocess env safety note**: the env vars above contain your relay API key. Third-party CLIs in debug / verbose mode may log env — before running `everyapi use`, make sure the debug flag you turn on does not leak `*_TOKEN` / `*_API_KEY`. Before sharing debug logs, run `sed -i 's/sk-everyapi-[A-Za-z0-9]*/REDACTED/g'`.

#### Transparent connector (default)

Transparent mode keeps supported clients on their vendor's official API origin instead of setting a third-party Base URL. It is the default for every tool that supports it; pass `--transparent=false` to opt out. The CLI starts an ephemeral HTTP CONNECT proxy on a random loopback port, creates a per-run CA whose private key stays in memory, and gives the child only the proxy URL, public CA bundle, and a non-secret placeholder credential. Registered model routes are decrypted locally and relayed to EveryAPI with the real relay key; other HTTPS hosts use raw CONNECT passthrough. An unknown path beneath a protected model prefix is blocked, and a relay failure never falls back to the vendor.

Verified against Claude Code and Codex CLI, which are the tools it defaults on for. Native Antigravity and LibreFang bypass the connector; the other registered tools use their documented injected/configured path, so an explicit unsupported `--transparent` fails loudly.

`--sanitize` composes with transparent mode rather than conflicting with it: the connector relays through the sanitizer (child → connector → sanitizer → gateway), so masking and the Claude recovery response guard apply on either launch path.

If `ALL_PROXY` is your only proxy variable, transparent mode is declined and the launch falls back to the injected path — Go's proxy resolution never reads `ALL_PROXY`, so the connector could not honor it. Set `HTTPS_PROXY` (socks5 included; net/http dials it natively) to keep transparent mode on.

This mode is experimental and intentionally process-scoped:

- the intercepted client side currently uses HTTP/1.1 and supports normal JSON/SSE requests (HTTP/2 gateway responses are translated to HTTP/1.1); client-side HTTP/2, HTTP/3/QUIC, WebSocket, certificate-pinned clients, and clients that ignore `HTTPS_PROXY` are not covered;
- Codex's built-in OpenAI provider probes the Responses WebSocket once; Connector returns HTTP 426 so Codex immediately falls back to HTTPS/SSE without consuming its retry budget. Codex may still print that single failed-probe log line;
- Claude Code still treats the non-secret placeholder as API-key authentication, so claude.ai connectors are disabled even though `ANTHROPIC_BASE_URL` remains the official `https://api.anthropic.com` origin. Transparent mode avoids third-party-origin detection; it cannot make API-key auth behave like a claude.ai OAuth login;
- it does not install a system CA, require administrator access, or change the default `everyapi use` behavior;
- it is not undetectable: clients can inspect proxy variables, the local certificate chain, sockets, timing, or response differences;
- the Connector sees decrypted model content. Its CA signing key is never written or uploaded, and the public CA file is removed on exit;
- the relay key is absent from the child environment and generated client configs, but the existing `~/.config/everyapi/credentials.json` is still readable by any process running as the same OS user. Transparent mode is credential-injection isolation, not a sandbox against a hostile child process.

### `everyapi auth login` — Device Authorization Grant + QR sign-in

Uses Device Authorization Grant (RFC 8628 style) + docs §7-5 Layer 1 "device-to-device QR sign-in":

1. The CLI creates a session, **renders a terminal QR + prints a short code + URL**
2. Scan the QR with your phone (or open the URL in a browser already signed into EveryAPI) — the URL inside the QR already carries `?code=USR-789`, the dashboard auto-fills the code, and the user only needs to click Approve
3. The CLI receives the access token and stores it at `~/.config/everyapi/credentials.json` (mode 0600)

```bash
everyapi auth login                                    # production; renders QR + opens browser by default
everyapi settings set gateway_region cn               # use the China-accelerated gateway for future commands
everyapi auth login --api-base http://localhost:8787   # local dev / self-hosted
everyapi auth login --no-browser                       # don't auto-open the browser (scan the QR)
everyapi auth login --no-qr                            # don't render the QR (non-UTF-8 terminals / piping)
```

Sample terminal QR rendering (Unicode half-block characters; ~18-20 rows tall):

```
█▀▀▀▀▀█  ▀▀ ▄  █▀▀▀▀▀█
█ ███ █  ▀▄█▀  █ ███ █
█ ▀▀▀ █ ▄ ▀ █▀ █ ▀▀▀ █
▀▀▀▀▀▀▀ █▄█▄█▄ ▀▀▀▀▀▀▀
... (actual QR encodes verification_uri?code=USR-789)
```

Why this is a stronger anti-phishing path:

- The user **does not enter a password on the new device** → no opportunity for a phishing site to capture credentials
- The user **does not get redirected to an unfamiliar browser page** → web-redirect phishing surface disappears
- Even if the CLI is a malicious fork producing a fake QR, the approval page after scanning is the real everyapi.ai dashboard (triggered from a device the user is already signed in to), and an unfamiliar code is not something a user will Approve

The remaining layers of docs §7-5 (cert pinning / phrase string / PKCE OAuth) have landed in independent PRs (cert pinning is report-only; enforce was a product decision not to ship).

### `everyapi seller <sub>` — marketplace seller subcommands

Brings dashboard channel-mount and withdrawal flows into the terminal for scripted onboarding. Before mounting a channel, `seller setup` checks eligibility (account active / email verified / account age / spend history / channel cap), and any failing gates are listed **before the user types a key** to avoid finding out via a 422 after submit.

```bash
everyapi seller list                          # list mounted channels
everyapi seller withdraw                      # move all pending seller earnings to main balance
everyapi seller withdraw --quota 1000         # partial transfer (DB units)
everyapi seller add-key   --type claude --name 'my-pro' \
                        --key 'sk-ant-...' --models 'claude-3-opus,claude-3-sonnet'
everyapi seller add-oauth codex --name 'my-chatgpt' --models 'gpt-4'
                                            # one-click OAuth: CLI starts a device flow, user enters
                                            # the user_code in the browser, token lands on the channel
everyapi seller add-oauth claude --name 'my-claude' --models 'claude-3-opus,claude-3-sonnet'
                                            # paste flow: CLI opens the Anthropic authorize page; user
                                            # pastes the callback-displayed code#state back into the terminal
everyapi seller add-oauth gemini --name 'my-gemini' --models 'gemini-1.5-pro'
                                            # true one-click loopback: CLI starts a random-port listener,
                                            # Google posts the code directly to the CLI — no pasting
everyapi seller setup                         # interactive wizard: checks eligibility first, then guides add-key
```

#### `add-key` — multi-key backup pool

`--key` can be repeated to mount N equivalent credentials on the same channel as a backup pool (B2, PRODUCT §4.5); when the primary key returns 401/403, the backend automatically fails over to the next one. `--key-remark` may also be repeated, positionally aligned with `--key` (the i-th `--key-remark` is the label for the i-th `--key`, for later dashboard identification). OAuth blobs cannot go into the backup pool — they remain single-key channels.

```
everyapi seller add-key   --type claude --name 'claude-pool' \
                        --models 'claude-3-opus' \
                        --key 'sk-ant-primary' --key-remark 'primary' \
                        --key 'sk-ant-backup'  --key-remark 'team backup'
```

`add-key`'s `--type` accepts aliases (`openai` / `claude` / `gemini` / `codex` / `vertex` / `aws` / `xai` / `deepseek`) or a numeric id. Mounting is subject to marketplace eligibility (account active, email verified, spend history, channel cap), and the CLI lists the failing checklist on all three entry points (`add-key` / `add-oauth` / `setup`) before doing anything else.

#### `add-oauth codex` — one-click OAuth (device flow)

`everyapi seller add-oauth codex --name 'my-chatgpt' --models 'gpt-4'` walks the Codex / ChatGPT RFC 8628-ish device authorization flow — the seller **never touches the token string**:

1. CLI calls `/api/seller/codex/device/start` and receives a short `user_code` and `verification_uri`
2. CLI opens the browser by default to `https://auth.openai.com/codex/device` (skip with `--no-browser`); the user enters the `user_code` in the browser to complete authorization
3. CLI polls `/api/seller/codex/device/poll`; once authorized, the backend creates the channel and writes the OAuth token into the channel's `key` field
4. Output: channel id + the bound ChatGPT email

Authorization cookies are managed by an in-process `http.CookieJar` (not persisted) — device-flow state is short-lived and process-bound, matching the threat model.

#### `add-oauth claude` — paste-and-submit OAuth

`everyapi seller add-oauth claude --name … --models …`. Anthropic's OAuth provider hard-codes `redirect_uri` to `https://console.anthropic.com/oauth/code/callback` on their side, so the CLI cannot use a localhost listener to receive the callback. Flow:

1. CLI calls `/api/seller/claude/oauth/start`; backend creates the PKCE pair + state and returns Anthropic's authorize URL
2. CLI opens the browser by default (skip with `--no-browser`); the user signs in to Anthropic and approves
3. Anthropic redirects to their callback page showing a `<code>#<state>` string
4. **User copies that string back to the CLI**
5. CLI calls `/api/seller/claude/oauth/complete`; backend exchanges code+verifier for the token and mints the channel

One extra paste step vs the device flow, but still much easier than hand-finding `~/.claude/auth.json`. The session cookie is issued by the backend at start; complete must hit the same session — the CLI's `http.CookieJar` is in-process and isolated per invocation.

#### `add-oauth gemini` — true one-click loopback OAuth

`everyapi seller add-oauth gemini --name … --models … [--no-browser] [--timeout 5m]`. Google's gemini-cli installed-app OAuth client accepts `http://127.0.0.1:<port>/callback` as `redirect_uri`, so **the CLI runs its own listener for the callback** — the user signs in via browser and never has to paste anything. Flow:

1. CLI starts a one-shot HTTP listener on a random ephemeral port (`127.0.0.1:0`), fixed path `/callback`
2. CLI calls `/api/seller/gemini/oauth/start` with `redirect_uri = http://127.0.0.1:<port>/callback`; backend strictly validates the redirect: loopback / port ≥ 1024 / scheme=http / path=/callback / no query/fragment/userinfo (prevents SSRF + redirect hijacking)
3. CLI opens the browser by default; the user signs in to Google and consents
4. Google redirects with `?code=…&state=…` to the CLI's listener
5. CLI verifies the state matches (prevents stale flows / forgery) and calls `/api/seller/gemini/oauth/complete`
6. Backend exchanges code + same redirect_uri for the token and mints the channel

Comparison with the other two providers:

| Provider | UX | Reason |
|---|---|---|
| `codex` | User types a 6-digit user_code in the browser; CLI auto-polls | OpenAI device flow, no redirect_uri |
| `claude` | User signs in via browser, copies `code#state` back to the CLI | Anthropic hard-codes redirect_uri to their own callback URL |
| `gemini` | User signs in via browser, closes the tab, done | Google accepts loopback redirects |

`--timeout` bounds the wait (default 5 minutes). On timeout the CLI exits and cleanly closes the listener.

### `everyapi edge <sub>` — one-command BYO-GPU supplier agent deploy

Onboard idle GPUs to sell compute through EveryAPI. The CLI condenses the deployment to 8 subcommands, sparing suppliers from hand-copying docker-compose, filling `.env`, or moving the registration token around:

```bash
everyapi auth login                              # reuses existing login
everyapi edge register --name "rtx-4090"    # calls /api/seller/edge/nodes for node_id + token, writes to ~/.local/share/everyapi/edge/<id>/
everyapi edge start                         # auto-detects NVIDIA / ROCm / Apple Silicon / CPU, docker compose up -d
everyapi edge models pull llama3.1:8b       # docker compose exec ollama ollama pull ...
everyapi edge status                        # local docker compose ps + dashboard online/offline
everyapi edge logs -f                       # tail logs
everyapi edge update                        # docker compose pull && up -d
everyapi edge remove                        # down -v + delete local dir + backend DELETE
```

`start` renders `docker-compose.yml` at runtime via `text/template` (**not from embedded static YAML**) — this lets container names be namespaced by node_id so multiple nodes on one host don't collide, and GPU passthrough is rendered conditionally by mode (NVIDIA = `deploy.resources.devices` + nvidia driver; ROCm = `/dev/kfd` + `/dev/dri` + `group_add: video`; macOS = no ollama container, the agent connects to the host's native ollama via `host.docker.internal`).

Credential flow: CLI uses an existing `sk-everyapi-` Bearer to call `POST /api/seller/edge/nodes` → backend returns the `registration_token` once (subsequently the backend stores only the sha256, never re-displays) → CLI writes it 0600 to `~/.local/share/everyapi/edge/<id>/node.json` → renders into the compose `EVERYAPI_REGISTRATION_TOKEN` env. **The registration token is never written to any .env file** (so suppliers don't accidentally commit it).

Requirements: `docker` + `docker compose v2` (v1 is EOL and not supported). On macOS, `brew install ollama && brew services start ollama` (Metal acceleration cannot run inside a docker container).

### `everyapi wallet topup` — topup redirect with anti-phishing phrase

`everyapi wallet topup` opens the dashboard topup page. Before redirecting, it goes through docs §7-5 Layer 3 verification:

1. CLI calls backend `POST /api/cli/jump-session` and receives a session id + a 4-emoji phrase string (e.g. `🌊 🦊 🍕 🚀`)
2. CLI prints both the URL and the phrase to the terminal, telling the user "the same phrase should appear at the top of the page in a moment"
3. User presses Enter; the CLI opens the URL via the system browser (with `?jump_session=<id>`)
4. As the dashboard loads, it calls backend `GET /api/cli/jump-session/:id/phrase`, receives the same phrase, and **displays it prominently in the page header**
5. The user does a visual compare: phrases match → genuine EveryAPI; mismatch or not displayed → close the tab, possible phishing

Why this blocks phishing: the phrase lives in backend memory keyed by a random 32-hex session id; a phishing site has no auth path to fetch it, and an attacker's forged `wallet/topup?jump_session=<id>` cannot read the phrase either. Short TTL (10 min) + single-use (the session is deleted after the dashboard fetches it once) further limit reuse risk.

```bash
everyapi wallet topup                    # opens the browser by default
everyapi wallet topup --no-browser       # only print the URL, copy manually
```

### `everyapi auth status` — current balance / usage / quota

```
$ everyapi auth status

  alice (alice@example.com)
  quota:     $12.34 remaining   $5.67 used
  requests:  1,234
  topup:     https://app.everyapi.ai/wallet
```

### `everyapi update` — run the brew upgrade automatically

Checks the latest release on the GitHub mirror, compares with the current version, and **automatically runs `brew update && brew upgrade everyapi`** — one command, no copy-paste.

```bash
$ everyapi update

Update available: v0.2.0 → v0.2.1
Install method:   Homebrew

$ brew update
==> Updated Homebrew from ...

$ brew upgrade everyapi
==> Upgrading everyapi-ai/tap/everyapi
  v0.2.0 -> v0.2.1
...

Done. Run `everyapi version` to confirm.
```

Why not just swap the binary directly? Homebrew's verification chain (SHA / bottle signing) is stronger than anything we'd rebuild inside the CLI, and self-replacing a running executable is a minefield on Windows.

Flags:
- `--check` — silent compare; exit 0 if up to date, exit 1 if outdated. For CI / cron:
  ```bash
  everyapi update --check || echo "needs upgrade"
  ```
- `--dry-run` — print the command that would run but don't actually run it (for inspection)

### `everyapi settings` — CLI preferences (language, etc.)

The CLI ships i18n in 7 languages: English, Simplified Chinese, Japanese, Korean, Spanish, German, French — CLI strings render in the user's chosen language. Backend API errors auto-negotiate via the `Accept-Language` header and cover 8 — the same 7 plus Traditional Chinese.

```bash
$ everyapi settings                          # interactive picker: choose a language
$ everyapi settings list                     # show current settings
$ everyapi settings set language zh          # set directly
$ everyapi settings set language fr          # French likewise
$ everyapi settings set terminal_mode tmux   # keep interactive tool launches in tmux
$ everyapi use codex -- resume               # reattach the sole live project tmux, or open Codex's picker
$ everyapi settings reset                    # reset to default (en + LANG auto-detect)
```

**Terminal mode**: the first interactive `everyapi use` asks whether launches should stay in the native terminal or run in tmux, then saves the choice as `terminal_mode`. Tmux mode restarts the complete `everyapi use` process inside an `everyapi-v3-*` session identified by its selected tool, workspace filesystem identity, and a random 128-bit launch identity, so its connector, sanitizer, temporary configuration, and target tool all survive a detach; the launch message prints the exact `tmux attach -t <session>` command. A bare Codex `resume` first looks for that identity: one live managed-agent pane is revalidated and reattached by its exact session name, while zero or multiple matches fall back to the normal Codex resume picker rather than guessing. Before every tmux launch, the CLI considers only strictly generated `everyapi-v3-*`, `everyapi-v2-*`, or legacy `everyapi-<pid>-<timestamp>` sessions. It removes a candidate only when one atomic tmux command revalidates that its sole window contains its sole, dead EveryAPI wrapper pane. Live detached agents, ordinary user-created tmux sessions, and every session containing a user-added pane or window are preserved; a session whose managed pane died but whose user-added pane remains live is preserved but not reused. Every launched client can inspect `EVERYAPI_TERMINAL_MODE`, `EVERYAPI_TMUX_SESSION`, and `EVERYAPI_TMUX_ATTACH_COMMAND`; Codex, Claude Code, OpenCode, and Kilo also receive the same session context through their documented model-instruction surfaces, including a rule not to create a nested tmux session. Other clients retain the environment contract without an injected user message. A launch that is already inside tmux is not nested, and non-interactive launches always remain native. If tmux is unavailable, the first-run picker disables that option; an existing tmux preference fails with installation/switch-back guidance instead of silently changing behavior.

**Auto-detect**: if you haven't explicitly set anything, the CLI reads env vars in the order `EVERYAPI_LANG > LC_ALL > LC_MESSAGES > LANG` on startup. A system locale of `zh_CN.UTF-8` / `ja_JP.UTF-8` / `fr_FR.UTF-8` etc. takes effect immediately — zero config.

**One-shot override**:

```bash
EVERYAPI_LANG=zh everyapi auth status             # this one invocation shows in Chinese; not persisted
```

**Translation example** (not-logged-in error, 7 languages × same line):

```
en : Error: not logged in — run 'everyapi auth login' first
zh : 错误: 未登录 — 先运行 'everyapi auth login'
ja : エラー: ログインしていません — まず 'everyapi auth login' を実行してください
ko : 오류: 로그인되어 있지 않습니다 — 먼저 'everyapi auth login' 을 실행하세요
es : Error: no has iniciado sesión — ejecuta primero 'everyapi auth login'
de : Fehler: nicht angemeldet — führe zuerst 'everyapi auth login' aus
fr : Erreur: non connecté — exécutez d'abord 'everyapi auth login'
```

Settings live in `~/.config/everyapi/settings.json` (same directory as `credentials.json`, but mode `0644` — no secrets).

**To improve translations or add a language**: see [`internal/i18n/locales/README.md`](internal/i18n/locales/README.md).

## Configuration files

Credentials live in `~/.config/everyapi/credentials.json` (or `$XDG_CONFIG_HOME/everyapi/` if `$XDG_CONFIG_HOME` is set), file mode `0600`. Written by `everyapi auth login`, read by every other command.

> ⚠️ **Tokens are stored in plaintext**. File mode `0600` + private `$HOME` path matches the convention of industry CLIs like `gh auth` / `aws configure`, but **for home-machine-theft / malware threat models**, any process that can read this file can call the EveryAPI API as you (including the MCP tools — see the §money-path friction step below). Recommended:
> - Don't `everyapi auth login` on shared / public machines
> - macOS users: consider `everyapi auth logout` before enabling FileVault
> - Linux users: enable home-dir encryption (`ecryptfs` / LUKS)
> - If you suspect leakage → `everyapi auth logout` immediately clears local credentials, and rotate the API key from the EveryAPI dashboard
>
> A platform keychain backend (macOS Keychain / Windows DPAPI / Linux Secret Service) is planned but not shipped.

Fields:

- `api_base` — the EveryAPI gateway URL. Default `https://api.everyapi.ai`. Self-hosted users / local development can override with `--api-base` on `login`.
- `access_token` — bearer used by every authenticated API call.
- `relay_key` — relay API key (`sk-everyapi-…`) used for the subprocess env of `everyapi use`. Fetched from `/api/token/*` and cached here.
- `user_id` / `username` — cached so `status` can render the identity line before its first API round-trip.

Gateway region is a CLI preference in `settings.json`: if it is unset, interactive login asks once and saves the choice. `everyapi settings set gateway_region cn` switches official gateway traffic to `https://api-cn.everyapi.ai`; `global` uses `https://api.everyapi.ai`. A custom `--api-base` for self-hosting still wins.

## Development

In the CLI source directory (the one containing this README, `go.mod`, and `Makefile`):

```bash
go test ./...
go run . status            # against production
go run . login --api-base http://localhost:8787   # against local backend
```

Local cross-compile for all platforms (same recipe CI uses):

```bash
make cli-release           # artifacts in dist/ (5 platforms × 1 binary = 5 files)
```

## MCP server (`everyapi mcp` subcommand)

The `everyapi` binary **includes a built-in** [Model Context Protocol](https://modelcontextprotocol.io) server — exposed as a subcommand (`everyapi mcp` reads stdin and writes stdout). AI agents (Claude Code / Cursor / Codex CLI / any MCP client) can invoke it directly, **without the user opening a terminal**.

> ⚠️ **MCP server auth model + exposure surface**
>
> - **No open ports**: `everyapi mcp` is pure stdio JSON-RPC, forked by the host CLI. It **does not listen on any socket / TCP port** — no network surface.
> - **Reads `~/.config/everyapi/credentials.json` directly**: the MCP server has no auth flow of its own; ability to read the credentials file = ability to call every exposed tool as you. Any MCP host that can run a process as your user has full access.
> - **Money path `everyapi_seller_withdraw` has a friction step**: callers must pass `confirm: "yes"`, ensuring the AI agent surfaces the transfer action in the UI to a human and avoids a silent drain. Other read-only tools (status / topup / seller_list) have no such requirement.
>
> Do not install MCP hosts you don't trust.

### Installation

Same binary as the CLI — installing the CLI gives you the MCP server:

```bash
make cli                                              # local build, produces ./bin/everyapi
# or via go install:
go install github.com/everyapi-ai/everyapi-ai/v3@latest
```

### Wiring into Claude Code

Add to `~/.claude/settings.json`:

```json
{
  "mcpServers": {
    "everyapi": {
      "command": "/abs/path/to/everyapi",
      "args": ["mcp"]
    }
  }
}
```

Wiring into Cursor, Codex CLI, or other MCP clients is similar — point `command` at the `everyapi` binary with `args: ["mcp"]`.

### Auth prerequisite

You must have run `everyapi auth login` in a terminal at least once — the MCP server is a background process with no terminal interaction capability, so it cannot run the device-code flow itself. It reads `~/.config/everyapi/credentials.json` directly; if missing, every tool returns a `isError: true` "not logged in" message guiding the user to log in.

### Tools exposed in v1 (8 total)

| Tool | Input | Purpose |
|---|---|---|
| `everyapi_status` | none | Current balance / used / request count |
| `everyapi_topup` | none | Returns the web topup URL |
| `everyapi_seller_list` | none | Lists marketplace seller channels |
| `everyapi_seller_withdraw` | `{confirm: "yes", quota?: int}` | Transfers seller_quota to main balance; **`confirm: "yes"` required** (money-path friction) |
| `everyapi_seller_add_oauth_codex_start` | `{name, models}` | Starts the Codex / ChatGPT device authorization flow; returns `user_code` + `verification_uri` + `flow_id` |
| `everyapi_seller_add_oauth_codex_poll` | `{flow_id}` | Checks Codex authorization status. `pending`/`slow_down` keep polling; `authorized` returns the channel id; `expired`/`denied` terminate |
| `everyapi_seller_add_oauth_claude_start` | `{name, models}` | Starts the Anthropic OAuth flow; returns `authorize_url`. After the user signs in via browser, they receive a `<code>#<state>` string |
| `everyapi_seller_add_oauth_claude_complete` | `{input}` | Submits the `<code>#<state>` string the user pasted in the previous step; mints the channel |

**OAuth tool usage pattern** (how an AI agent walks through this in a conversation):

```
User: Add a ChatGPT Plus seller channel for me, name it my-chatgpt, models gpt-4
AI    → everyapi_seller_add_oauth_codex_start({name: "my-chatgpt", models: "gpt-4"})
       ← "Go to chatgpt.com/codex, enter USR-789, then tell me when done"
User: Done in the browser
AI    → everyapi_seller_add_oauth_codex_poll({flow_id: "..."})
       ← "status=pending, wait a few more seconds"
[keep polling until authorized]
       ← "status=authorized — channel #314 mounted"

User: Add the Claude Pro one too, my-claude / claude-3-opus
AI    → everyapi_seller_add_oauth_claude_start({...})
       ← "Go to [URL] to complete authorization, then give me the code#state string"
User: code-abc123#state-xyz
AI    → everyapi_seller_add_oauth_claude_complete({input: "code-abc123#state-xyz"})
       ← "Channel #315 mounted"
```

Gemini OAuth (loopback flow) is **not exposed via MCP** — the loopback listener's lifetime doesn't match the cross-tool-call lifecycle. Gemini still goes through the CLI's `everyapi seller add-oauth gemini`.

### Manual smoke test

```bash
make cli
./bin/everyapi mcp <<'EOF'
{"jsonrpc":"2.0","id":1,"method":"initialize"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"everyapi_status","arguments":{}}}
EOF
```

You should see three JSON response lines: initialize result, list of 14 tools, status text (or a not-logged-in isError).

## What this binary does NOT include yet

Still **unimplemented** (ordered by importance; subsequent releases will add incrementally without breaking v1 surface):

- ⚠️ OS-level code signing (macOS notarization / Windows Authenticode) — for now we rely on sigstore cosign keyless + SHA256SUMS two-layer verification; both ship with every GitHub Release and Homebrew checks them automatically
- ❌ Platform keychain backend — tokens still stored in plaintext on disk (mode 0600)

Previously listed here but **now shipped** (don't treat as unimplemented):

- ✅ Local sanitizer proxy — the command is `everyapi proxy {start,stop,status,configure}` (not `everyapi start`/`everyapi configure`); engine + 6 built-in detectors + custom regex + integrated into `everyapi use`
- ✅ Seller OAuth onboarding across all three providers (codex device / claude paste / gemini loopback)
- ✅ QR sign-in main path — `login` uses device-code **+ QR as the main path**, with `--no-qr` as fallback
- ✅ Anti-phishing layers — phrase string (`everyapi wallet topup`), PKCE/state strict-check, and cert pinning have all landed; cert pinning is **report-only** (silent on match / alert on mismatch / never refuses to connect), with the product decision being "alert only, do not enforce"

## Reporting vulnerabilities

See [`SECURITY.md`](./SECURITY.md).
