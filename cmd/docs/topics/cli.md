# The everyapi CLI

One binary. It signs you in, launches AI tools through the gateway, and exposes the whole account surface — keys, usage, wallet, marketplace, supply — without opening a browser. It is also the MCP server (`everyapi mcp`) and the sidecar EveryAPI Connect drives.

Running `everyapi` with no arguments on a terminal opens an interactive launcher over every command. `everyapi help` prints the same set as text. Any command takes `help` for its own subcommands and flags: `everyapi seller help`, `everyapi token help`.

## Command map

```
auth       Sign in / out, session status
wallet     Top-up, payment history, methods, redemption keys
checkin    Claim today's daily-grant quota
account    Profile, 2FA, passkeys, OAuth, affiliate code, subscriptions
use        Launch a third-party AI CLI routed through EveryAPI
tmux       List / reattach / close the tmux sessions `use` left running
token      Relay API keys
models     Model catalog: list / pricing / groups
stats      Usage, request log, model performance, upstream health
market     Demand posts, disputes, abuse reports
inbox      In-app notifications, direct messages
seller     Channel-marketplace seller commands
edge       BYO-GPU supplier agent
artifacts  Publish and manage self-contained HTML reports
docs       This handbook
events     Subscribe to the live event stream (SSE)
feedback   Send a bug report or feature request to the team
proxy      Local sanitizer proxy
computer   Read and control local macOS windows through Accessibility
mcp        Run as an MCP server, or register with an AI client
doctor     Self-check: credentials, gateway, sanitizer, installed tools
settings   CLI preferences
admin      Operator console — visible to admin accounts only
version    Build version, update, uninstall
```

EveryAPI includes native workspace automation commands as CLI-only subcommands. They do not add rows to the interactive launcher. Repository, worktree, terminal, file, skills, diagnostics, and fetched-page state are handled locally in this process; no second executable is required.

Useful entry points include:

```text
everyapi status --json
everyapi repo list --json
everyapi worktree list --json
everyapi terminal read --screen --json
everyapi snapshot --json
everyapi screenshot --json
everyapi goto --url https://example.com --json
everyapi click --element @e1 --json
everyapi fill --element @e1 --value text --json
everyapi skills list --json
everyapi orchestration inbox --json
everyapi automations list --json
everyapi environment list --json
everyapi project list --json
everyapi file diff --json
everyapi linear search "bug" --json
everyapi vm recipe doctor cloud-sandbox --json
everyapi emulator list --json
everyapi open --json
everyapi serve --json
everyapi claude-teams --help
everyapi host list --json
everyapi type --help
everyapi select --help
everyapi scroll --help
everyapi back --json
everyapi reload --json
everyapi eval --help
everyapi wait --help
everyapi check --help
everyapi uncheck --help
everyapi focus --help
everyapi clear --help
everyapi select-all --help
everyapi keypress --help
everyapi pdf --help
everyapi full-screenshot --help
everyapi hover --help
everyapi drag --help
everyapi upload --help
everyapi tab list --json
everyapi exec --help
everyapi cookie get --json
everyapi storage local get --json
everyapi console --json
everyapi network --json
everyapi find --help
everyapi clipboard read --json
everyapi dialog accept --json
everyapi download --json
everyapi highlight --help
everyapi capture start --json
everyapi viewport --help
everyapi geolocation --help
everyapi intercept list --json
everyapi mouse move --help
everyapi inserttext --help
everyapi is --help
everyapi get --help
everyapi scrollintoview --help
everyapi dblclick --help
everyapi forward --json
everyapi set device --help
everyapi agent-context --json
everyapi agent --help
everyapi diagnostics memory --json
```

Page commands use a local, persisted page model: `goto` fetches and stores HTML, navigation and form actions update that model, and `screenshot`/`pdf` render it without a desktop UI. Mobile commands use `xcrun simctl` or `adb` when either bridge is installed and fall back to a persisted local device registry.

## Signing in

```
everyapi auth login          Device flow with a terminal QR
everyapi auth login --no-browser
everyapi auth login --no-qr
everyapi auth login --api-base http://localhost:8787
everyapi auth status         Identity, quota, usage, balance
everyapi auth logout         Clear this device's credentials
```

The device flow prints a short code and a URL, and renders the URL as a QR whose payload already carries the code. Scanning it from a phone that is already signed in gets you to an approval button with the code pre-filled. No password is typed on the machine being authorised, which is what removes the phishing surface.

## Reading your usage

```
everyapi stats usage [--days N] [--since <window>] [--per-day|--per-model]
everyapi stats log list [--limit N] [--page P] [--token T] [--model M]
everyapi stats log stat [--since 24h]
everyapi stats log summary
everyapi stats perf
everyapi stats upstream
```

`--since` accepts windows (`1h`, `24h`, `7d`, `30d`) or absolute Unix seconds. `stats perf` is per-model success rate, latency, and throughput; `stats upstream` is the provider status-page rollup, and both work signed out.

## Settings

```
everyapi settings              Interactive editor
everyapi settings list         Current values, and where the file lives
everyapi settings get <key>
everyapi settings set <key> <value>
everyapi settings reset
```

Keys:

```
language                 en | zh | zh-TW | ja | ko | es | de | fr
menu_layout              grouped | nested
gateway_region           global | cn
terminal_mode            native | tmux
codex_hook_trust_bypass  true | false
dangerous_mode           true | false
```

Settings live in `~/.config/everyapi/settings.json`, mode `0644` — nothing secret is stored there, and hand-editing is fine.

Language auto-detects from `EVERYAPI_LANG`, `LC_ALL`, `LC_MESSAGES`, then `LANG` when nothing is set explicitly. A one-shot override that is not persisted:

```
EVERYAPI_LANG=zh everyapi auth status
```

## Terminal mode

The first interactive `everyapi use` asks whether launches should stay in the native terminal or run inside tmux, then remembers the answer as `terminal_mode`.

In tmux mode the whole launch — connector, sanitizer, temporary config, and the tool itself — runs inside a generated `everyapi-v3-*` session, so it survives a detach. The attach command is printed once at launch; after that:

```
everyapi tmux                      List every managed session
everyapi tmux list [--format=json]
everyapi tmux attach [<session>]   Attach (picker when the name is omitted)
everyapi tmux kill <session>...
everyapi tmux kill --all [--yes]
```

Session cleanup before a launch only ever removes strictly generated EveryAPI sessions whose sole window holds their sole, dead wrapper pane. A live detached agent, an ordinary tmux session of yours, or a managed session you added a pane to is preserved and never reused.

## Marketplace, messages and reports

```
everyapi market demand list [--state open|closed] [--page P] [--limit N]
everyapi market demand my
everyapi market demand show <id>
everyapi market demand submit --title T --model M --max-price <usd/M>
                             [--est N] [--description D] [--expires TS]
                             [--require-oauth]
everyapi market demand cancel <id>
everyapi market demand remove <id> [-y]
everyapi market dispute my | show <id> | submit …
everyapi market report --email E --category C --target-type T
                       --description D [--target-id ID] [--evidence URL]
```

`market report` is a public endpoint: it works signed out, and when you are signed in the reporter is captured server-side for triage.

```
everyapi inbox notify list [--unread] [--page P] [--limit N]
everyapi inbox notify count | read <id> | readall
everyapi inbox dm threads [--page P] [--limit N]
everyapi inbox dm contacts | count
everyapi inbox dm open <other_user_id>
everyapi inbox dm messages <thread_id> [--after <id>] [--limit N]
everyapi inbox dm send <thread_id> <body>
everyapi inbox dm read <thread_id>
```

## Account and profile

```
everyapi account info                 Rolled-up profile and security view
everyapi account 2fa                  Status: enabled, locked, backups left
everyapi account 2fa enable           Enroll, interactive
everyapi account 2fa disable --code <6-digits>
everyapi account 2fa backup --code <6-digits>
everyapi account passkey              Passkey registration status
everyapi account oauth list | unbind <provider_id>
everyapi account update [--username U] [--display-name D]
everyapi account passwd               Prompts, echo off
```

Passkey registration and email verification need a browser; 2FA does not.

## Local macOS computer use

```
everyapi computer capabilities | permissions | list-apps | list-windows
everyapi computer get-app-state | screenshot
everyapi computer click | set-value | type-text | paste-text
everyapi computer press-key | hotkey | scroll | drag
everyapi computer perform-secondary-action
```

macOS only; other platforms answer `unsupported_platform` explicitly rather than pretending. It drives a separately code-signed helper app over a local Unix socket, and the Accessibility grant goes to that helper — not to `everyapi`, not to your terminal. Deliberately not exposed over MCP.

The full surface, the permission grants, the element-index cache and its `--session` isolation, and what the app blocklist is and is not worth: see the `computer` topic.

## Self-check

```
everyapi doctor
everyapi doctor claude
everyapi doctor --format=json
```

Checks credentials, gateway reachability, the sanitizer, and which supported tools are installed. This is the first thing to run when something is wrong; see the `troubleshooting` topic.

## Publishing reports

```
everyapi artifacts share [--json] <file.html>
everyapi artifacts list [--json]
everyapi artifacts update [--json] <url> <file.html>
everyapi artifacts delete [--json] <url>
```

Self-contained single-page HTML only. This is also the surface Claude Code, Codex, OpenCode, and Kilo use for the completion-report standard `everyapi use` injects into them.

## Live events

```
everyapi events [--quiet] [--types <a,b,c>]
```

A long-lived SSE subscription to the backend event stream, with reconnect and heartbeat handled by the SDK.

## Updating and removing

```
everyapi version                  Build version
everyapi version update           Check and run the matching upgrade
everyapi version update --check   Silent compare for CI
everyapi version update --dry-run
everyapi version uninstall        Remove everyapi state and binary
```

`version update` hands the upgrade to whatever installed the binary — Homebrew, `go install`, or the published install script — rather than self-replacing a running executable. Exit code 2 means the latest version could not be determined; a network blip must never read as "an upgrade is available".

## Files the CLI owns

```
~/.config/everyapi/credentials.json  0600, written by auth login
~/.config/everyapi/settings.json     0644, CLI preferences
~/.config/everyapi/sanitizer.json    Detectors and custom patterns
~/.config/everyapi/sanitizer.pid     Written by `proxy start --detach`
~/.config/everyapi/sanitizer.log     Detached proxy log
~/.config/everyapi/sessions/         Process-scoped tool homes
~/.local/share/everyapi/edge/<id>/   Edge node registration and compose
```
