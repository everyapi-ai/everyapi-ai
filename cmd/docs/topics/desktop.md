# EveryAPI Connect (desktop)

The desktop client — Tauri 2 and React, with the EveryAPI Go CLI bundled as a sidecar. It signs in through the same device flow, finds the AI tools installed on the machine, and installs, configures, or launches them without anyone opening a terminal.

Windows and macOS. There is no Linux build: Linux users are served by the CLI, which does everything Connect does.

## What it does

Discovery, then one of three actions per target: install it, configure it to use EveryAPI, or open it already configured. The catalog holds 31 targets. Every supported one has a concrete native adapter, a documented configuration surface, or an isolated CLI launch path.

A sample of the matrix:

| Target | Behaviour |
| --- | --- |
| Claude Code | A terminal running an isolated `use claude` |
| Codex CLI | Isolated CODEX_HOME plus a Responses provider |
| Claude / Codex desktop | Downloads the vendor's notarized build if absent |
| OpenCode, Aider, Goose | Documented CLI surface plus the live catalog |
| Continue extension | A marked model plus a key in its global .env |
| Cherry Studio, Chatbox | After consent, the app's own import URL scheme |
| Pi Web, Open WebUI | Starts the official server, opens it when ready |
| EveryAPI for VS Code | Key via a nonce-bound 127.0.0.1 handoff |
| LobeHub | Coming soon — listed, actions disabled |

Connect does not edit private databases, install a root CA, mutate DNS, or intercept traffic. Products with no safe integration surface are omitted rather than approximated.

## The security boundary

The React renderer is secret-free, and that is enforced rather than intended: credential storage and refresh stay in the bundled CLI, and Rust owns the fixed sidecar commands, target discovery, terminal launching, deep links, clipboard writes, and loopback handoffs. No renderer command returns an API key or a token.

## Installing third-party tools

Connect fetches an installer only where the vendor's own distribution can be verified before you are invited to run it:

- A version-pinned URL is checked against a pinned digest.
- A rolling `latest` URL — the only form Anthropic and OpenAI publish for their desktop builds — is checked with Gatekeeper against the vendor's notarized Developer ID Team ID. A digest pinned against a rolling URL would start rejecting the genuine installer the day the vendor shipped a new one.
- Where neither applies, including Windows and Linux for those two vendors, Connect opens the vendor's download page rather than guessing a URL.

A download that fails either check is deleted rather than opened. You still complete the install yourself.

## Update checks

After discovery, and once every 24 hours, Connect asks only the package channel that owns each installation: npm for global Node tools, PyPI for uv-managed Python tools, GitHub's release API for reviewed standalone binaries, winget's fixed package and source pair for Windows desktop apps. Results are bounded, secret-free, and advisory — a newer version adds a badge and an optional notification. A timeout or a malformed vendor response is omitted, never treated as an update. Connect never builds a package id from renderer input and never performs an unattended third-party update.

## Getting it

Download links are at `https://app.everyapi.ai/connect`. Releases are cut from the monorepo and ship Windows and macOS artifacts together.
