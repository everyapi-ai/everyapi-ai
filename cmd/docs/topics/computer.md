# Driving macOS apps

`everyapi computer` reads and controls local macOS application windows through Accessibility. It is the one surface in this CLI that touches things outside EveryAPI, so it is also the one with the most explicit boundaries.

macOS only. Linux and Windows builds answer `unsupported_platform` rather than pretending.

## How it runs

The CLI drives a small, separately code-signed helper app — `EveryAPI Computer Use.app` — over a local Unix socket, downloading and launching it on first use. If EveryAPI Connect already installed its own bundled copy, the CLI reuses that rather than downloading a second one.

The helper being its own signed app is the point: the Accessibility grant is scoped to that one bundle identity. It does not also authorize every AppleScript on the machine, and it survives CLI and helper updates.

## Granting permission

```
everyapi computer permissions --json
```

Accessibility must be granted to **EveryAPI Computer Use** — not to `everyapi`, not to `osascript`, not to your terminal.

The helper will not appear in the permission list on its own. It only ever calls `AXIsProcessTrusted()` and never the prompting variant, so it never registers itself and no system dialog will ever offer it. Add it yourself under System Settings → Privacy & Security → Accessibility with the **+** button:

```
~/Library/Application Support/everyapi/computer-use/
  EveryAPI Computer Use.app
/Applications/EveryAPI Connect.app/Contents/Resources/
  EveryAPI Computer Use.app
```

The first is the CLI's own copy, the second is Connect's bundled one. `permissions` reports the grants but does not currently say which of the two it is using.

`permissions` reports three things: Accessibility directly; Automation as `unknown`, since this provider does not depend on System Events and has no separate Automation preflight; and Screen Recording — needed only for `screenshot` — through the non-prompting `CGPreflightScreenCaptureAccess` check, granted the same way under Privacy & Security → Screen Recording.

## Reading

```
everyapi computer capabilities            Provider and supported operations
everyapi computer permissions             Accessibility / Screen Recording
everyapi computer list-apps               Running desktop applications
everyapi computer list-windows --app <a>  Windows for one app
everyapi computer get-app-state --app <a> The accessibility tree
everyapi computer screenshot --app <a>    That window's own pixels as PNG
```

## Acting

```
everyapi computer click        Click an element or a window-local point
everyapi computer set-value    Set an editable element's value
everyapi computer type-text    Type into the focused receiver
everyapi computer paste-text   Paste through the native clipboard
everyapi computer press-key    Press one key
everyapi computer hotkey       Press a modifier chord
everyapi computer scroll       Scroll at an element or point
everyapi computer drag         Drag between elements or points
everyapi computer perform-secondary-action
                               Run a listed accessibility action
```

## Selecting a target

Every command takes `--app`, and every command that works on a window takes one of two window selectors:

```
--app <selector>     App name, bundle ID, or pid:<number>   (required)
--window-index <n>   An index from list-windows
--window-id <id>     A window id from list-windows
                     (mutually exclusive with --window-index)
--element-index <n>  An element from the latest get-app-state snapshot
--session <id>       Namespace the element-index cache
                     (letters, digits, - _ .)
--restore-window     Bring the window forward first; a failed focus
                     check becomes a no-op instead of an error
--no-screenshot      Skip the screenshot attached to the result
--json               Machine-readable envelope
```

Windows are identified internally by the real per-window id CoreGraphics assigns on screen where one exists, falling back to a snapshot-scoped synthetic id for minimized windows. The provider also keeps a fingerprint to detect observable changes — but public Accessibility attributes cannot prove that a replacement window with identical attributes is the same native instance.

## Element indexes expire

Indexes come from the most recent `get-app-state` snapshot and expire after two minutes. `app_stale`, `element_stale`, and `window_stale` all mean the same thing: take a fresh snapshot.

`--session <id>` matters more than it looks. Without it, every caller targeting the same app and window shares one cache slot, so two concurrent workflows overwrite each other's element indexes — one workflow's `get-app-state` silently invalidates the other's references, surfacing as `element_stale` at best and a mismatched click at worst. Give each concurrent workflow its own session id and their cached elements stay isolated.

The cache holds only opaque application, process, window, path, role, frame, action-name, and fingerprint data, under `~/.config/everyapi/computer-use/state/` with private permissions.

## Command-specific flags

```
click       --x <n> --y <n>        Window-local point instead of an element
            --mouse-button <btn>   left (default) | right | middle
            --click-count <n>      2 for a double-click
            --modifiers <chord>    cmd | shift | alt/option | ctrl, + joined
set-value   --value <text> | --value-stdin
type-text   --text <text>  | --text-stdin
paste-text  --text <text>  | --text-stdin
press-key   --key <key>            Key name or modifier chord
hotkey      --key <key>            Modifier chord, e.g. cmd+a
scroll      --direction <dir>      up | down | left | right
            --amount <pixels>      Default 600
            --x <n> --y <n>        Point instead of an element
drag        --from-element-index <n> --to-element-index <n>
            --from-x <n> --from-y <n> --to-x <n> --to-y <n>
screenshot  --out <path>           Write the PNG here
perform-secondary-action
            --action <AXAction>    An advertised accessibility action
```

Anything beyond a plain single left click — a different button, a click count, a modifier — forces a synthesized `CGEvent` click instead of the semantic `AXPress` / `AXConfirm` / `AXOpen` shortcut, because those Accessibility actions have no right-click or multi-click equivalent.

`paste-text` delivers text through the pasteboard (save, write, `cmd+v`, restore) rather than synthesizing keystrokes, for element types that reject direct character input.

## Screenshots

`screenshot` captures the target window only — never the full screen, never another window — via `CGWindowListCreateImage` scoped to that window's id. It needs the Screen Recording grant. Write the PNG with `--out <path>`, or get it base64-encoded in `--json` output.

Separately, `get-app-state` and every mutating action attach a screenshot of the target window to their result by default, taken after the operation completes. Pass `--no-screenshot` to skip it. This is best-effort on top of the accessibility snapshot: it never fails the action it is attached to, so a missing Screen Recording grant surfaces as `screenshotError` in JSON (or a `screenshot unavailable: ...` line in plain output) rather than blocking the click that requested it. On success the PNG is written under the OS temp directory and referenced by path, not inlined — copy it out promptly, since files older than an hour are swept the next time a screenshot is taken.

## When an action's outcome is unknown

A successful GUI action stays successful when its best-effort state refresh fails; JSON then carries `refreshError` instead of returning a retryable error.

`action_outcome_unknown` is different and means exactly what it says: the helper call was interrupted, or returned an invalid receipt, after the action was already handed off. **The action may already have happened.** Refresh state and look before deciding whether to retry.

## What is blocked, and what that is worth

A maintained list of known terminal apps, password managers, Keychain Access, Passwords, System Settings, and EveryAPI Connect is blocked as defense-in-depth friction.

Bundle-ID blocking is not a comprehensive application classifier, and it should not be read as one. Unlisted apps, editors with integrated terminals, browsers, and renamed or newly released apps may expose equivalent capabilities. The explicit `--app` target, macOS TCC, and the caller's same-user authority remain the real trust boundary.

Observed text is stripped of terminal control sequences and scanned for credentials before output. Text you type or set is rejected when it matches the built-in secret detectors. Prefer `--text-stdin` and `--value-stdin` to keep ordinary text out of your shell history.

## Not exposed over MCP

This surface is local-only and is deliberately not registered in `everyapi mcp`. See the `mcp` topic for what is.
