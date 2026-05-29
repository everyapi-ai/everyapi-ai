> 🌐 **English** · [简体中文](translations/README.zh-CN.md) · [日本語](translations/README.ja.md) · [한국어](translations/README.ko.md) · [Español](translations/README.es.md) · [Deutsch](translations/README.de.md) · [Français](translations/README.fr.md)

# `everyapi` CLI

Buyer-onboarding CLI for the [EveryAPI](https://everyapi.ai) AI API gateway. Point any Claude Code / Codex / Gemini CLI at the gateway **in under a minute**.

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
curl -fsSL https://everyapi.ai/install.sh | bash
```

The script auto-detects OS + arch, downloads the matching `everyapi_{os}_{arch}.tar.gz` from the public release mirror, verifies the SHA256, and installs to `~/.local/bin` (or `/usr/local/bin` when run as root). If [cosign](https://github.com/sigstore/cosign) is installed it also verifies the keyless signature — pass `--require-signature` to make that step mandatory (recommended for CI / supply-chain-sensitive setups).

Common flags:

```bash
curl -fsSL https://everyapi.ai/install.sh | bash -s -- --version v0.2.2     # pin a version
curl -fsSL https://everyapi.ai/install.sh | bash -s -- --prefix /usr/local  # custom prefix
curl -fsSL https://everyapi.ai/install.sh | bash -s -- --require-signature  # fail if cosign verify fails
curl -fsSL https://everyapi.ai/install.sh | bash -s -- --force              # reinstall the same version
```

To upgrade later, re-run the same command. The script resolves the latest release tag and replaces the binary in place when a newer one exists; if the installed binary is already at the resolved target version, it exits with `already at vX.Y.Z — nothing to do` (safe to put in setup scripts / dotfiles). Pass `--force` to reinstall on top (useful for verifying integrity or recovering a damaged file). The script is also published in this repo at [`install.sh`](install.sh) if you'd rather download + read it first.

**Go users (`go install`):**

```bash
go install github.com/everyapi-ai/everyapi-ai@latest
```

**Windows / manual:** grab `everyapi_windows_amd64.zip` (or any other artifact) from the [Releases page](https://github.com/everyapi-ai/everyapi-ai/releases/latest) and verify against `SHA256SUMS` before placing the binary on `%PATH%`.

## Commands

| Command | Purpose |
|---|---|
| `everyapi login` | Sign in to EveryAPI on this device |
| `everyapi logout` | Clear local credentials |
| `everyapi status` | Show balance, usage, quota |
| `everyapi topup` | Open the topup page (with anti-phishing phrase check) |
| `everyapi use <tool>` | Set env and exec into a third-party CLI (pointed at EveryAPI) |
| `everyapi seller <sub>` | Marketplace seller commands (list / withdraw / add-key / setup) |
| `everyapi edge <sub>` | One-command deploy for the BYO-GPU supplier agent (register / start / status / logs / models / stop / update / remove) |
| `everyapi mcp` | Run as an MCP server (stdin/stdout JSON-RPC) |
| `everyapi update` | Check for new versions and print the upgrade command for your install method |
| `everyapi version` | Show build version |
| `everyapi help` | Help |

### `everyapi use <tool>` — exec into a third-party CLI (pointed at the EveryAPI gateway)

The main reason to install this CLI. It sets the target tool's env vars per the conventions it expects, then execs into it — your existing Claude Code / OpenAI Codex CLI / Gemini CLI **needs no config change** to point at the EveryAPI gateway.

```bash
everyapi use claude         # Claude Code → EveryAPI
everyapi use codex          # OpenAI Codex CLI → EveryAPI
everyapi use gemini         # Gemini CLI → EveryAPI
everyapi use hermes         # Nous Research Hermes Agent → EveryAPI
everyapi use                # no arg → interactive picker over installed tools
```

Each tool uses different conventions; the CLI remembers them:

| Tool | How it's pointed at EveryAPI |
|---|---|
| claude | env: `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN` |
| codex | env: `OPENAI_API_KEY` + generated `CODEX_HOME/config.toml` (codex routes via config, not `OPENAI_BASE_URL`) |
| gemini | env: `GEMINI_API_KEY`, `GOOGLE_GEMINI_BASE_URL` |
| hermes | generated `HERMES_HOME/config.yaml` (`provider: custom`, `base_url`, inline `api_key`); hermes ignores `OPENAI_BASE_URL` for its main model |

No more looking up which variable name each tool reads, whether you need to append `/v1`, or which auth-header style applies.

**hermes model default**: `provider: custom` has no built-in model, so `everyapi use hermes` boots with `claude-sonnet-4-6` by default. Both claude and gpt families are reachable through the same `/v1` endpoint — switch at runtime with `hermes model`, or set a different boot default per launch: `EVERYAPI_HERMES_MODEL=gpt-5.1 everyapi use hermes`.

**hermes config isolation**: `everyapi use hermes` redirects `HERMES_HOME` to `~/.config/everyapi/hermes-home`, so its `config.yaml` (and the sessions/logs that accumulate there) are kept separate from your personal `~/.hermes` — a plain `hermes` invocation is untouched, and the two won't share session history. Bare `hermes` opens the interactive chat; pass `everyapi use hermes -- --tui` for the terminal UI.

> ⚠️ **Subprocess env safety note**: the env vars above contain your relay API key. Third-party CLIs in debug / verbose mode may log env — before running `everyapi use`, make sure the debug flag you turn on does not leak `*_TOKEN` / `*_API_KEY`. Before sharing debug logs, run `sed -i 's/sk-everyapi-[A-Za-z0-9]*/REDACTED/g'`.

### `everyapi login` — Device Authorization Grant + QR sign-in

Uses Device Authorization Grant (RFC 8628 style) + docs §7-5 Layer 1 "device-to-device QR sign-in":

1. The CLI creates a session, **renders a terminal QR + prints a short code + URL**
2. Scan the QR with your phone (or open the URL in a browser already signed into EveryAPI) — the URL inside the QR already carries `?code=USR-789`, the dashboard auto-fills the code, and the user only needs to click Approve
3. The CLI receives the access token and stores it at `~/.config/everyapi/credentials.json` (mode 0600)

```bash
everyapi login                                    # production; renders QR + opens browser by default
everyapi login --api-base http://localhost:8787   # local dev / self-hosted
everyapi login --no-browser                       # don't auto-open the browser (scan the QR)
everyapi login --no-qr                            # don't render the QR (non-UTF-8 terminals / piping)
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
everyapi login                              # reuses existing login
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

### `everyapi topup` — topup redirect with anti-phishing phrase

`everyapi topup` opens the dashboard topup page. Before redirecting, it goes through docs §7-5 Layer 3 verification:

1. CLI calls backend `POST /api/cli/jump-session` and receives a session id + a 4-emoji phrase string (e.g. `🌊 🦊 🍕 🚀`)
2. CLI prints both the URL and the phrase to the terminal, telling the user "the same phrase should appear at the top of the page in a moment"
3. User presses Enter; the CLI opens the URL via the system browser (with `?jump_session=<id>`)
4. As the dashboard loads, it calls backend `GET /api/cli/jump-session/:id/phrase`, receives the same phrase, and **displays it prominently in the page header**
5. The user does a visual compare: phrases match → genuine EveryAPI; mismatch or not displayed → close the tab, possible phishing

Why this blocks phishing: the phrase lives in backend memory keyed by a random 32-hex session id; a phishing site has no auth path to fetch it, and an attacker's forged `wallet/topup?jump_session=<id>` cannot read the phrase either. Short TTL (10 min) + single-use (the session is deleted after the dashboard fetches it once) further limit reuse risk.

```bash
everyapi topup                    # opens the browser by default
everyapi topup --no-browser       # only print the URL, copy manually
```

### `everyapi status` — current balance / usage / quota

```
$ everyapi status

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
$ everyapi settings reset                    # reset to default (en + LANG auto-detect)
```

**Auto-detect**: if you haven't explicitly set anything, the CLI reads env vars in the order `EVERYAPI_LANG > LC_ALL > LC_MESSAGES > LANG` on startup. A system locale of `zh_CN.UTF-8` / `ja_JP.UTF-8` / `fr_FR.UTF-8` etc. takes effect immediately — zero config.

**One-shot override**:

```bash
EVERYAPI_LANG=zh everyapi status             # this one invocation shows in Chinese; not persisted
```

**Translation example** (not-logged-in error, 7 languages × same line):

```
en : Error: not logged in — run 'everyapi login' first
zh : 错误: 未登录 — 先运行 'everyapi login'
ja : エラー: ログインしていません — まず 'everyapi login' を実行してください
ko : 오류: 로그인되어 있지 않습니다 — 먼저 'everyapi login' 을 실행하세요
es : Error: no has iniciado sesión — ejecuta primero 'everyapi login'
de : Fehler: nicht angemeldet — führe zuerst 'everyapi login' aus
fr : Erreur: non connecté — exécutez d'abord 'everyapi login'
```

Settings live in `~/.config/everyapi/settings.json` (same directory as `credentials.json`, but mode `0644` — no secrets).

**To improve translations or add a language**: see [`internal/i18n/locales/README.md`](internal/i18n/locales/README.md).

## Configuration files

Credentials live in `~/.config/everyapi/credentials.json` (or `$XDG_CONFIG_HOME/everyapi/` if `$XDG_CONFIG_HOME` is set), file mode `0600`. Written by `everyapi login`, read by every other command.

> ⚠️ **Tokens are stored in plaintext**. File mode `0600` + private `$HOME` path matches the convention of industry CLIs like `gh auth` / `aws configure`, but **for home-machine-theft / malware threat models**, any process that can read this file can call the EveryAPI API as you (including the MCP tools — see the §money-path friction step below). Recommended:
> - Don't `everyapi login` on shared / public machines
> - macOS users: consider `everyapi logout` before enabling FileVault
> - Linux users: enable home-dir encryption (`ecryptfs` / LUKS)
> - If you suspect leakage → `everyapi logout` immediately clears local credentials, and rotate the API key from the EveryAPI dashboard
>
> A platform keychain backend (macOS Keychain / Windows DPAPI / Linux Secret Service) is planned but not shipped.

Fields:

- `api_base` — the EveryAPI gateway URL. Default `https://api.everyapi.ai`. Self-hosted users / local development can override with `--api-base` on `login`.
- `access_token` — bearer used by every authenticated API call.
- `relay_key` — relay API key (`sk-everyapi-…`) used for the subprocess env of `everyapi use`. Fetched from `/api/token/*` and cached here.
- `user_id` / `username` — cached so `status` can render the identity line before its first API round-trip.

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
go install github.com/everyapi-ai/everyapi-ai@latest
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

You must have run `everyapi login` in a terminal at least once — the MCP server is a background process with no terminal interaction capability, so it cannot run the device-code flow itself. It reads `~/.config/everyapi/credentials.json` directly; if missing, every tool returns a `isError: true` "not logged in" message guiding the user to log in.

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

You should see three JSON response lines: initialize result, list of 4 tools, status text (or a not-logged-in isError).

## What this binary does NOT include yet

Still **unimplemented** (ordered by importance; subsequent releases will add incrementally without breaking v1 surface):

- ⚠️ OS-level code signing (macOS notarization / Windows Authenticode) — for now we rely on sigstore cosign keyless + SHA256SUMS two-layer verification; both ship with every GitHub Release and Homebrew checks them automatically
- ❌ Platform keychain backend — tokens still stored in plaintext on disk (mode 0600)

Previously listed here but **now shipped** (don't treat as unimplemented):

- ✅ Local sanitizer proxy — the command is `everyapi proxy {start,stop,status,configure}` (not `everyapi start`/`everyapi configure`); engine + 6 built-in detectors + custom regex + integrated into `everyapi use`
- ✅ Seller OAuth onboarding across all three providers (codex device / claude paste / gemini loopback)
- ✅ QR sign-in main path — `login` uses device-code **+ QR as the main path**, with `--no-qr` as fallback
- ✅ Anti-phishing layers — phrase string (`everyapi topup`), PKCE/state strict-check, and cert pinning have all landed; cert pinning is **report-only** (silent on match / alert on mismatch / never refuses to connect), with the product decision being "alert only, do not enforce"

## Reporting vulnerabilities

See [`SECURITY.md`](./SECURITY.md).
