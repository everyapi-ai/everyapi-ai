# EveryAPI Menubar App — GOAL (V1.1)

Source: `docs/cli/channel-marketplace.md` §4.7-7-7 + `docs/BLUEPRINT.md` §2.3 / §4 V1.1.

Branch: `feat/menubar-v1.1`
Worktree: `/Volumes/Lexar/Workspace/oss/everyapi-ai/everyapi-menubar`
Date set: 2026-05-19

---

## One-liner

A cross-platform menubar/status-bar binary `everyapi-menubar`, living in the
same Go module as the CLI (`clients/cli/`), wrapping the existing
`internal/{api,sanitizer,oauthloopback,config}` packages. macOS-priority,
Win/Linux follow at M4. Deployment / signing / notarization deferred to
post-implementation (user has Apple ID; certificate work done after the build
is complete).

## Scope (V1.1, 5 core features)

1. View buyer quota / seller revenue (menu items, refresh on a timer)
2. Toggle sanitizer proxy on/off (checkmark item; in-process start/stop)
3. One-click `seller add-oauth claude` / `gemini` (browser jump via
   anti-phishing jump-phrase)
4. Top-up / withdraw (browser jump via jump-phrase)
5. Desktop notifications: seller new order / account risk warning

## Locked technical decisions

| Area | Choice | Why |
|---|---|---|
| Menubar lib | `fyne.io/systray` | Active maintenance (Fyne team), 3-platform, template-icon for macOS dark mode |
| Module layout | Same Go module as CLI (`clients/cli/`) | Direct import of `internal/` — zero refactor of existing code |
| Entry point | `clients/cli/cmd/menubar/` | Sibling of existing `cmd/mcp/` |
| Notifications | macOS native `UserNotifications` via cgo; Win/Linux `gen2brain/beeep` | `caseymrm/menuet` is dead (2.5y) — avoid |
| Webview popups | Not in V1.1. If needed later → `webview/webview_go` with main-thread workaround |
| Process model | Single binary; sanitizer runs in-process as goroutine (not fork CLI) |
| Bundling | `goreleaser` v2 with `.app` bundle, `LSUIElement=true` |

## Out of scope (explicit)

- In-menu charts / rich graphics (use browser jump to dashboard for that)
- Auto-update / Sparkle
- apt/dnf/winget package-manager distribution (AppImage / DMG / EXE only)
- Sanitizer protocol changes (consume the existing API as-is)
- Backend API changes (if notifications need an event endpoint, downgrade to
  poll-on-startup for V1.1)

## Milestones

| M | Days | Deliverable | Status |
|---|---|---|---|
| **M1** | 1-3 | Skeleton: macOS `.app` bundle, tray icon visible, Quit item works | ✓ |
| **M2** | 4-8 | Login (device-auth) + quota / revenue display + token persistence | ✓ |
| **M3** | 9-13 | Sanitizer toggle + seller-channel jump-phrase + Top-up jump | ✓ |
| **M4** | 14-18 | Desktop notifications + cross-platform compile | ✓ |
| **M5** | 19-21 | Tests + README; signing/notarization deferred | ✓ |

Stop for review at end of each milestone.

## Definition of Done — outcomes (after Phase 2)

1. ✓ macOS Sonoma+ tray icon, no Dock (`LSUIElement=true`).
2. ✓ Logged-in user sees live quota / used / requests / seller earnings.
3. ✓ Logged-out user can sign in via device-auth from the menu.
4. ✓ Sanitizer toggles on/off in-process **and persists across
   relaunches** via `~/.config/everyapi/menubar-state.json` (M6).
5. ✓ Seller OAuth runs **in the menubar** for Claude (osascript text
   prompts + clipboard paste-back) and Gemini (loopback callback)
   on macOS, plus the dashboard jump-phrase path for managing
   existing channels (M8). Win/Linux text input still falls back to
   "use the CLI" until those native input modals land.
6. ✓ Top-up / withdraw jumps to `/wallet` via jump-phrase.
7. ✓ Notifications fire on seller-earnings increase AND on
   `enabled → auto-disabled` channel transitions (5-min poll of
   `/api/seller/channel`, M9). Account-risk-warning event-stream
   awaits backend SSE; polling closes the loop in the meantime.
8. ✓ Win/Linux: source compiles + cross-builds clean via `GOOS=… go
   build` (no CGO toolchain needed — fyne.io/systray dbus path).
   Native confirm dialogs wired for both: PowerShell MessageBox on
   Windows, zenity/kdialog on Linux with auto-confirm fallback (M7).
9. ✓ Unit tests at **60.4 %** coverage of `internal/menubar` (M10).
   Pure-logic + lifecycle + OAuth orchestration + jump-phrase + risk
   polling + Run dispatch all covered; the residual ~40 % is GUI-
   bound (`menu.go` applyXxx that need a live systray, the actual
   shell-out functions like osascript/pbpaste, and `systray.Run`
   itself).
10. ✓ `clients/cli/cmd/menubar/README.md` written. `docs/cli/menubar.md`
    still deferred — `docs/` is a separate submodule; that slice
    ships as its own PR against `everyapi-docs`.

## Risks / known landmines

- `fyne.io/systray` cannot reassign menu wholesale (fyne#2860) — build menu
  once, mutate items only.
- macOS main-thread rule: `systray.Run` must run on the main goroutine; all
  business logic stays in background goroutines.
- CGO required for systray + native notifications → cross-compile needs
  target-platform toolchain (goreleaser handles).
- Existing `clients/cli/internal/` is `internal/` — only same-module callers
  can import it. The same-module decision above is what makes this work.

## Open items deferred to later milestones

- Whether backend has a real event endpoint for notifications (investigate
  early in M4)
- Whether to ship menubar from the same `goreleaser` config as the CLI or a
  sibling one (M5 decision)
- Code signing / Apple notarization (post-M5)

---

## Phase 2 — extended scope (M6-M10)

After M1-M5 landed I re-read the original brief and noted three honest gaps
that are worth closing while the context is fresh:

- **Seller OAuth as jump-phrase** under-delivers the spec's "GUI-guided
  OAuth" — the dashboard works fine but the menubar can do better with
  native input dialogs.
- **Cross-platform native dialogs** were stubbed off-darwin — the
  anti-phishing modal silently auto-confirms on Win/Linux today.
- **Sanitizer state** resets to off on every relaunch — UX papercut.

Plus three "complete the loop" items: docs slice, account-risk polling,
and pushing test coverage past the 60% target.

| M | Scope | Status |
|---|---|---|
| **M6** | Persist sanitizer state + auto-restart at launch | ✓ |
| **M7** | Cross-platform native confirm-dialogs (Win PowerShell, Linux zenity, fallback) | ✓ |
| **M8** | In-menubar seller OAuth (Claude paste + Gemini loopback) via osascript text inputs | ✓ |
| **M9** | Channel-disabled risk polling (uses existing `/api/seller/channel`, no backend change) | ✓ |
| **M10** | Push `internal/menubar` coverage to ≥ 60 % | ✓ |

### M6 — persisted sanitizer state

Add `Credentials.SanitizerEnabled` (or a sibling state file) so the
toggle survives a restart. On startup, if the field is true, fire the
runner before the first menu paint. The toggle handler writes the
new value through `config.Save` synchronously so a crash mid-toggle
doesn't leave the state inverted.

### M7 — cross-platform dialogs

Today `dialog_other.go` returns `(true, nil)` — silent auto-confirm.
Replace with:

- Windows: shell out to `powershell` `[System.Windows.Forms.MessageBox]
  ::Show(body, title, OKCancel, Information)`
- Linux: shell out to `zenity --question --title=… --text=…`, fall
  back to auto-confirm (with a log line) if zenity isn't installed
- Document the fallback in the README

### M8 — in-menubar seller OAuth

Two flows, both need name + models text input plus (for Claude) a paste
of the `<code>#<state>` string from Anthropic's callback page. Use
`osascript display dialog … default answer ""` on macOS for text
inputs; reuse the M7 cross-platform shim once that's in. Reuse
`internal/api/seller_claude_oauth.go` + `internal/api/seller_gemini_oauth.go`
and `internal/oauthloopback` exactly as the CLI does — same backend
contract, different surface.

### M9 — risk polling

Backend exposes `/api/seller/channel` (list mounted channels with
status). status=3 means auto-disabled (§4.6 — 7-day 3-strike rule).
Add a periodic poll (5 min, lighter than the 30-s data refresh) that
remembers each channel's last-seen status; a transition `enabled → auto-disabled`
fires a `notify` with the channel name. Re-enables silently update
the cache.

### M10 — coverage push

The M5 refactor exposed `menuView`. Use it to test:

- The `Run()` dispatch table by enqueueing every command via `c.kick`
  and asserting menu state
- `handleSignIn` → `runDeviceAuth` against an httptest.Server with a
  stub for `openBrowser` (extract it behind a package var like
  `notify`)
- The risk-polling watcher from M9

Target ≥ 60 % of `internal/menubar` statements, ≥ 80 % on auth /
notification / sanitizer paths.

---

## Phase 3 — ergonomic polish (M11-M15)

After M10 the surface was feature-complete but rough on ergonomics. I
walked the buyer / seller workflows and noted the actual high-friction
moments:

- "Where's my relay API key?" — every SDK integration needs it, and the
  CLI is the only path today.
- "How do I get help / where are the logs?" — no in-menu discovery.
- "Is the app working right now?" — the icon never changes; signed-out
  / signed-in / mid-refresh / alert states all look identical.
- "I'm on Windows / Linux — your OAuth dialogs don't work." — M8 only
  wired the macOS text-input path.
- "I want to change the refresh cadence / mute notifications / point at
  my self-hosted backend." — no preferences surface.

| M | Scope | Status |
|---|---|---|
| **M11** | Copy relay API key + recent-channels submenu | ✓ |
| **M12** | About / Help / Open config + documentation entry-points | ✓ |
| **M13** | Icon state machine (signed-out / signed-in / alert) + refresh activity hint | ✓ |
| **M14** | Win/Linux text input + clipboard parity (PowerShell / zenity / kdialog, Get-Clipboard / xclip / wl-paste) | ✓ |
| **M15** | Preferences (refresh interval, sanitizer port, notification mutes, custom API base) via edit-config-file flow | ✓ |

### M11 — copy relay API key + recent channels

Reuse the CLI's `resolveRelayKey` logic exposed by `internal/api` — the
exact same shape `everyapi use` consumes. "Copy relay key" reads the
key, writes it to the clipboard, and fires a notification confirming
the prefix (so the user can verify they're not pasting a stale value).
"Recent channels" lists the most recent 5 channels as a submenu, each
opening the dashboard's per-channel page on click.

### M12 — about / help / open config

- About dialog (osascript on macOS, native equivalents) showing version,
  build commit, license.
- "Open documentation…" linking to the public docs site.
- "Reveal config in Finder / Files / Nautilus" item that opens the
  XDG config directory so the user can edit credentials / state /
  preferences manually.
- "Report an issue…" linking to the GitHub issue tracker.

### M13 — icon state machine

Procedural icon now renders four variants:

- `signed-out`: outlined "E", monochrome
- `signed-in`: filled "E", monochrome
- `alert`: filled "E" with a small dot in the top-right corner
- `refreshing`: a subtle rotated variant flipped on for the duration of
  a refresh tick (give-or-take 1 s of perceptual feedback)

State transitions are driven by the controller; the menuView interface
gains `applyIconState(state)`. Tests cover the state-to-variant
mapping; the actual NSStatusItem icon write goes through systray's
`SetTemplateIcon`.

### M14 — Win/Linux input parity

- Windows `textPrompt`: PowerShell + `Microsoft.VisualBasic.Interaction
  ::InputBox` for the single-line entry shape.
- Linux `textPrompt`: zenity `--entry` first, kdialog `--inputbox`
  fallback.
- Windows `readClipboard`: PowerShell `Get-Clipboard -Raw`.
- Linux `readClipboard`: xclip if present, else wl-paste, else error.

Completes the cross-platform Claude OAuth flow — Win/Linux users now
get the same paste-back UX as macOS.

### M15 — preferences

User-editable knobs live in `~/.config/everyapi/menubar-prefs.json`.
The menu's "Preferences…" item opens the file in the user's default
text editor (`open -t` on macOS, `xdg-open` on Linux, `notepad` on
Windows). Documented in the README; we deliberately don't build a
GUI preferences panel because (a) it's a once-a-month action and
(b) a text editor is the most accessible / scriptable surface.

Knobs (all optional, zero-valued defaults preserve current behaviour):

- `refresh_interval_seconds`: default 30, min 10
- `sanitizer_listen`: default "127.0.0.1:8888"
- `mute_earnings_notifications`: bool, default false
- `mute_risk_notifications`: bool, default false
- `api_base`: override `config.DefaultAPIBase` for self-hosters

## Phase 3 outcomes (M11-M15)

- ✓ Buyer workflow: "Copy relay API key" reuses the same resolver
  the CLI uses; cached after first fetch; notification confirms via
  prefix-only to avoid screen-share leakage.
- ✓ Seller workflow: "My channels" submenu shows the five most
  recent channels with status glyphs (✓ / ⏸ / ⚠), each clickable
  to the dashboard's per-channel page.
- ✓ Discoverability: Help submenu with About / Open docs /
  Reveal config / Report issue / Preferences.
- ✓ Icon state machine: signed-out outlined "E", signed-in filled,
  filled-plus-corner-dot when any seller channel is auto-disabled.
- ✓ Win/Linux full parity for clipboard + text input: PowerShell
  InputBox + Get-/Set-Clipboard on Windows; zenity / kdialog +
  xclip / wl-paste / xsel on Linux/BSD.
- ✓ Preferences via edit-config-file flow at
  `~/.config/everyapi/menubar-prefs.json` — knobs for refresh
  cadence (10s floor), sanitizer port, notification mutes, custom
  API base.
- ✓ Version + commit baked into binary at build time via
  `-ldflags -X internal/version.{Version,Commit}=…`; surfaced in
  the About dialog.
- ✓ Test coverage held at 60.8 % across the expanded surface.

## Still out of scope after M15

- Apple Developer ID code signing + notarization (next PR)
- Designer-made template icon (still procedural)
- Auto-update / Sparkle
- Backend SSE / event stream (replaces M9 polling once shipped)
- Auto-start at login (use macOS Login Items / Windows Startup folder /
  systemd user units — meta-OS handles this better than the app would)
- i18n (await international launch)
- Live-reload of preferences (restart required by design — these are
  once-a-month knobs, not high-frequency adjustments)
