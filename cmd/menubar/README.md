# everyapi-menubar

GUI menu-bar / status-bar companion to the `everyapi` CLI (V1.1 surface
of the three-pronged tool-chain — see `docs/cli/channel-marketplace.md`
§4.7-7-7).

The binary stays resident in the macOS status bar (or the Windows /
Linux tray), polls the gateway for quota + seller earnings, exposes a
checkmark toggle for the local sanitizer proxy, and jumps the browser
to the dashboard via the `§4.7-7-5` anti-phishing handshake.

## Status

| Milestone | Scope | Status |
|---|---|---|
| **M1** | Tray icon + macOS `.app` bundle | ✓ |
| **M2** | Device-auth + quota/revenue display | ✓ |
| **M3** | Sanitizer toggle + jump-phrase actions | ✓ |
| **M4** | Desktop notifications + cross-platform parity | ✓ |
| **M5** | Tests + this doc; signing / notarization deferred | ✓ |

Signing / Apple notarization is **explicitly deferred** to a follow-up
PR — the user direction was to land the full surface first.

## Build

### macOS (development, host arch)

```bash
cd clients/cli
./cmd/menubar/build-macos.sh
open cmd/menubar/dist/EveryAPI.app
```

Output: `cmd/menubar/dist/EveryAPI.app` — drag to `/Applications` or
keep it in `dist/`. The `Info.plist` ships with `LSUIElement=true`, so
double-clicking shows a status-bar icon without adding a Dock icon.

Variants:

```bash
ARCH=arm64 ./cmd/menubar/build-macos.sh      # explicit arch
ARCH=universal ./cmd/menubar/build-macos.sh  # arm64 + amd64 lipo'd
VERSION=0.2.0 ./cmd/menubar/build-macos.sh   # override the plist version
```

### Windows / Linux

`fyne.io/systray`'s modern dbus path is pure-Go, so the menubar
cross-compiles from a Mac without a CGO toolchain:

```bash
GOOS=linux   go build -o everyapi-menubar-linux   ./cmd/menubar
GOOS=windows go build -o everyapi-menubar.exe     ./cmd/menubar
```

Runtime requirements:

- **Linux**: a desktop environment with a `StatusNotifier` host
  (KDE Plasma native; GNOME requires the AppIndicator extension;
  XFCE/Cinnamon natively). `notify-send`/libnotify for desktop
  notifications.
- **Windows**: Windows 10+ (toast notifications).

## Menu surface

```
─── account ─────────────────────
  alice@everyapi.ai                  (informational)
  Quota:    $10.00 remaining          (informational)
  Used:     $2.47                     (informational)
  Requests: 1,234                     (informational)
  Seller earnings: $5.00              (hidden when zero)
─── actions ─────────────────────
  Sign in…                            (hidden when signed in)
  Code: ABCD-1234                     (visible during device-auth)
  [✓] Sanitizer: 127.0.0.1:8888       (toggle in-process proxy)
  Add seller channel…                 (jump-phrase → dashboard)
  Top up / withdraw…                  (jump-phrase → dashboard)
─── housekeeping ────────────────
  Refresh now
  Open dashboard…
  Sign out
─── exit ───────────────────────
  Quit EveryAPI
```

Items that depend on auth state hide / show automatically; the menu is
built once and mutated (fyne#2860 — wholesale reassign isn't supported).

## Architecture

Single binary; the `cmd/menubar` package is `package main` (its own
entry point) and imports `internal/menubar` for all logic. Sharing the
same Go module as the CLI means the menubar reuses `internal/api`,
`internal/config`, `internal/sanitizer`, and `internal/oauthloopback`
without duplication or a separate "SDK" package.

```
clients/cli/
  main.go                  CLI entry point (existing)
  cmd/
    menubar/
      main.go              menubar entry point — package main
      icon.go              procedural 44×44 template PNG
      Info.plist.tmpl      bundle plist (LSUIElement=true)
      build-macos.sh       .app assembler
      GOAL.md              feature spec (this milestone breakdown)
      README.md            this file
  internal/
    menubar/
      controller.go        state machine + dispatch loop
      menu.go              menuView interface + systray-backed impl
      auth.go              device-auth flow (no-TTY variant)
      jumpopen.go          jump-phrase + browser-open
      sanitizer.go         in-process sanitizer.Server lifecycle
      notify.go            beeep wrapper (stubbable in tests)
      format.go            quota / URL formatting
      dialog_darwin.go     osascript confirm-dialog (build tag)
      dialog_other.go      stub on non-darwin (M4-followup)
      *_test.go            unit + light integration tests
    api/                   reused from CLI (device-auth, jump-session, cert pin)
    config/                reused from CLI (~/.config/everyapi/credentials.json)
    sanitizer/             reused from CLI (in-process proxy)
```

### Concurrency

The controller is single-goroutine: a `kick` chan command serializes
every state mutation. Click handlers, the refresh ticker, and login
callbacks all enqueue commands; the dispatch loop in `Run()` drains
them one at a time. HTTP I/O runs in spawned goroutines that report
back via the same channel — direct field mutation from those
goroutines is forbidden.

Race detector clean (`go test -race ./internal/menubar/...`).

## Anti-phishing posture

Top-up, wallet, and "Add seller channel" are routed through
`/api/cli/jump-session` (§4.7-7-5 Layer 3). On macOS the menubar shows
a native `osascript display dialog` with the four-emoji verification
phrase **before** opening the browser; the user must visually match
the same phrase on the dashboard page. On Windows / Linux V1.1 ships
with the dialog stubbed to auto-confirm — the browser still gets the
phrase via the dashboard, but the menubar-side modal lands in a
follow-up PR.

The shared `internal/api` enforces TLS pinning (`certpin.go`,
report-only today) the same way the CLI does — both surfaces talk to
the gateway through one client.

## Test coverage

```
$ go test ./internal/menubar/... -coverprofile=/tmp/c
coverage: 34.5% of statements
```

The uncovered surface is structural: `menu.go`'s `applyLoggedOut /
applyLoggedIn / applyData / applySanitizerState` mutate real
`systray.MenuItem` handles which require `systray.Run` to be active;
`dialog_darwin.go` and `auth.go` shell out to `osascript` and `open`.
Pure-logic paths (formatting, seller-earnings delta detection,
sanitizer lifecycle, state transitions) are at 80%+.

## Known follow-ups

- Apple Developer ID signing + notarization
- goreleaser entry that produces a signed DMG + universal binary
- Win/Linux native confirm-dialog (today the dialog stub auto-confirms
  off-darwin)
- Backend event stream → account-risk-warning notifications (today
  only `seller_quota` delta polling is wired)
- Designer-made template icon (placeholder is procedural)
