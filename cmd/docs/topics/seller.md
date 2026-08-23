# Selling capacity on the marketplace

The marketplace lets an ordinary user mount their own upstream channel — a provider API key, or an OAuth-linked subscription — and earn a share of what buyers spend routing through it. The platform takes a configurable cut.

Whether it is open at all is a deployment flag (`marketplace.enabled`, default off). An operator toggles it with `everyapi admin marketplace on|off`.

## Commands

```
everyapi seller list           Channels you have mounted
everyapi seller setup          Interactive wizard: key or OAuth
everyapi seller eligibility    Why you can or cannot mount more
everyapi seller add-key --type T --name N --key K --models M [--remark R]
everyapi seller add-oauth codex|claude|gemini --name N --models M
everyapi seller update <id> [flags]   Name / models / status / keys
everyapi seller refresh <id>          Rotate an OAuth credential
everyapi seller remove <id> [-y]
everyapi seller sales [--page P] [--limit N]
everyapi seller withdraw [--quota <int>]
```

## Check eligibility first

Mounting is gated on account status, verified email, account age, prior spend history, and a channel cap. All three entry points — `add-key`, `add-oauth`, `setup` — run the checklist and print every failing gate **before** you type a credential. Finding out via a 422 after submitting a key is exactly the failure this avoids.

```
everyapi seller eligibility
```

## Mounting with a plain API key

```
everyapi seller add-key --type claude --name 'my-pro' \
  --key 'sk-ant-…' --models 'claude-3-opus,claude-3-sonnet'
```

`--type` accepts aliases — `openai`, `claude`, `gemini`, `codex`, `vertex`, `aws`, `xai`, `deepseek` — or a numeric channel-kind id.

`--key` repeats to mount several equivalent credentials on one channel as a backup pool: when the primary returns 401 or 403, the backend fails over to the next. `--key-remark` repeats in parallel, so the i-th remark labels the i-th key.

```
everyapi seller add-key --type claude --name 'claude-pool' \
  --models 'claude-3-opus' \
  --key 'sk-ant-primary' --key-remark 'primary' \
  --key 'sk-ant-backup'  --key-remark 'team backup'
```

OAuth blobs cannot join a backup pool; those channels stay single-key.

## Mounting with OAuth

Three providers, three different flows, because three vendors made three different decisions about redirect URIs. You never handle the token string in any of them.

| Provider | What you do | Why |
| --- | --- | --- |
| codex | Type a short code in the browser | Device flow, no redirect_uri |
| claude | Paste code#state back to the CLI | Anthropic pins its own callback |
| gemini | Sign in, close the tab, done | Google accepts loopback redirects |

**codex** — the CLI starts a device authorization, opens `https://auth.openai.com/codex/device` (skip with `--no-browser`), and polls until the backend reports authorized. Output is the channel id and the bound ChatGPT email.

**claude** — the backend creates the PKCE pair and state and returns Anthropic's authorize URL. You approve, Anthropic's callback page shows a `<code>#<state>` string, and you paste that one string back into the terminal. One extra step than the device flow, and still far easier than hand-extracting a token from a config file.

**gemini** — the CLI opens a one-shot listener on a random loopback port at a fixed `/callback` path, and the backend strictly validates the redirect before starting: loopback host, port ≥ 1024, scheme `http`, path `/callback`, no query, fragment, or userinfo. That validation is what stops the redirect from being turned into an SSRF or a hijack. Google posts the code straight to the listener; the CLI checks the state matches and completes. `--timeout` bounds the wait, default five minutes, and the listener closes cleanly on expiry.

Authorization cookies are held in an in-process cookie jar, never persisted — device-flow state is short-lived and process-bound.

## Earnings

```
everyapi seller sales
everyapi seller withdraw
everyapi seller withdraw --quota 1000
```

Buyer charges accrue as pending seller quota. `withdraw` moves it to your main balance; with no flag it moves everything.

## Through an AI agent

The MCP server exposes the eligibility check, plain-key mounting, and the codex and claude OAuth flows, each with the money path gated behind an explicit `confirm: "yes"`. Gemini's loopback flow is CLI-only — see the `mcp` topic for why.

## Disputes

If a buyer charge or a channel interaction goes wrong:

```
everyapi market dispute my
everyapi market dispute show <id>
everyapi market dispute submit --counterparty <uid> --type <t> \
  --target-kind <k> --target <id> --description <d> [--amount <quota>]
```
