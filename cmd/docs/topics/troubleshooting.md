# When something is wrong

Symptoms and their causes, in the order you are likely to hit them: sign-in and session errors, gateway 401s and 429s, a model list that is shorter than expected, tool launches that do not find their binary, and the macOS permission grants that are easy to give to the wrong application.

Start with the self-check, which covers credentials, gateway reachability, the sanitizer, and installed tools in one pass:

```
everyapi doctor
```

It checks credentials, gateway reachability, the sanitizer, and which supported tools are installed. `everyapi doctor <tool>` narrows it to one client; `--format=json` makes it machine-readable.

## "not logged in — run 'everyapi auth login' first"

No credentials file, or it is unreadable. `everyapi auth login`. If it appears inside an MCP client rather than a terminal: the MCP server cannot run the device flow itself, because a background process has no terminal to render the QR into. Log in once from a shell and the server picks the credentials up.

## "your session expired — run 'everyapi auth login' again"

The access token was rejected. The credentials file is left in place — logging in overwrites it — but until then the launcher renders its logged-out menu rather than advertising commands that would only 401.

Note the deliberate asymmetry: a definitive 401 flips the menu, but a network error or a 5xx does not. An offline launch keeps showing the cached menu instead of falsely claiming you are signed out.

## 401 from the gateway on a request that used to work

In order of likelihood:

1. The key was revoked or disabled. `everyapi token list`.
2. The key expired. `everyapi token show <id>`.
3. The key has an IP allowlist and you moved network. Same command.
4. You are sending the login access token instead of the relay key. The relay key starts with `sk-everyapi-`; `everyapi token key <id>` prints it.

## 429

Either the per-user per-model rate limit or an upstream's own limit. `everyapi stats upstream` shows the provider status rollup — a provider-wide problem looks different from your own limit. `everyapi stats log list --since 1h` shows what you actually sent.

## "no available channel" / requests fail for one model only

No channel in your routing group can currently serve that model. Check what you can actually reach:

```
everyapi models list
everyapi models groups
```

If the list is shorter than you expect, the key is pinned to one group. Run `everyapi token switch` and pick `Auto`.

## Claude Code or Codex shows the wrong model list

The same cause. The relay key is cached in `credentials.json` and the lookup is deliberately offline and sticky, so a launch never silently re-picks. `everyapi token switch`, choose `Auto`, relaunch.

For a one-off through a different pool without disturbing the cache: `everyapi use <tool> --group <name>`.

## "<tool> installed, but not on $PATH yet"

The installer put a binary somewhere your current shell does not search. Open a new shell, or add the installer's bin directory to `PATH`. The message's longer variant lists exactly which directories were searched.

## "<tool> is not installed, but its installer needs <x>"

The installer runs in a plain non-login shell, so anything your shell rc adds — and anything that exists only as a shell function — is invisible to it. Install the named prerequisite properly, or install the tool yourself with the command the message prints.

## Transparent mode silently did not engage

If `ALL_PROXY` is your only proxy variable, transparent mode declines and the launch falls back to the injected path. Go's proxy resolution never reads `ALL_PROXY`, so the connector could not honour it. Set `HTTPS_PROXY` instead — socks5 included, `net/http` dials it natively.

## A tmux launch disappeared

It did not; it detached. The attach command is printed once at launch, and after that:

```
everyapi tmux
everyapi tmux attach
```

Session pruning before a launch only ever removes strictly generated EveryAPI sessions whose sole window holds their sole, dead wrapper pane. A live detached agent, one of your own tmux sessions, or a managed session you added a pane to is never removed and never reused.

## Requests reach the model but the content is mangled

The sanitizer is masking something. It replaces detected secrets with stable placeholders, which is right for stateless SDK traffic and wrong for agentic coding, where the model reasons over exact file contents. That is why `--sanitize` is off by default. Drop the flag, or narrow the detectors with `everyapi proxy configure`.

## macOS: `everyapi computer` cannot see anything

Accessibility must be granted to **EveryAPI Computer Use** — not to `everyapi`, not to `osascript`, not to your terminal. Ask macOS to register the signed helper and show its consent flow:

```
everyapi computer permissions --request accessibility
everyapi computer permissions --request screen-recording
```

If a previously dismissed prompt does not reappear, add it manually under System Settings → Privacy & Security with the **+** button:

```
~/Library/Application Support/everyapi/computer-use/
  EveryAPI Computer Use.app
/Applications/EveryAPI Connect.app/Contents/Resources/
  EveryAPI Computer Use.app
```

The first path is the CLI's own copy; the second is Connect's bundled one, which the CLI reuses when present. `everyapi computer permissions --json` reports the current grants but does not currently say which of the two it is using.

`screenshot` additionally needs Screen Recording, granted the same way.

## An `element_stale` / `app_stale` / `window_stale` error

Element indexes come from the most recent `get-app-state` snapshot and expire after two minutes. Take a fresh snapshot. If two workflows are driving the same window, give each its own `--session <id>` — without it they share one cache slot and silently invalidate each other's indexes.

## `action_outcome_unknown`

The action may already have happened. Refresh state and look before retrying.

## Upgrade says "already installed" but a newer version exists

Homebrew is using a cached formula. `brew update && brew upgrade everyapi`. Or let the CLI pick the right path for however it was installed:

```
everyapi version update
```

In CI, `everyapi version update --check` exits 0 up-to-date, 1 outdated, 2 when the latest version could not be determined. Treat 2 as unknown, not as "an upgrade is available" — a network blip must not trigger a deploy.

## Debug logs contain your key

Some third-party CLIs dump their environment in verbose mode. Before sharing a log:

```
sed -i 's/sk-everyapi-[A-Za-z0-9]*/REDACTED/g' debug.log
```

Then rotate the key: `everyapi token revoke <id>` and mint a new one. Transparent mode avoids the exposure entirely for Claude Code and Codex, since the key never enters the child's environment.

## Reporting it

```
everyapi feedback --content "…" --kind bug
```

Goes straight to the team chat. Nothing stores a copy, so a failure here means it reached nobody — keep your text and try again.
