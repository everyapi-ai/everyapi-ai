# API keys

A relay API key — `sk-everyapi-…` — is what your code, your SDK, and your AI tools authenticate with. It is separate from your login session: signing out of the CLI does not revoke a key, and a leaked key is revoked on its own.

## Managing keys from the CLI

```
everyapi token list          List your keys (masked)
everyapi token show <id>     One key in detail
everyapi token key <id>      Print the full plaintext key
everyapi token create --name <n> …
everyapi token update <id> … Edit fields; omitted flags keep their value
everyapi token enable <id>
everyapi token disable <id>
everyapi token revoke <id> [-y]
everyapi token usage <sk-…>  A key's remaining quota, signed out
everyapi token switch        Choose the default key the CLI launches with
```

Create and update take the same flags:

```
--name <n>          Display name; required on create, max 50 chars
--group <g>         Routing group; "" (default) means auto
--unlimited         No quota cap; overrides --quota
--quota <int>       Remaining quota in gateway units
--expires <when>    "never" (create default) or absolute Unix seconds
--models <a,b,c>    Restrict to this model list; omit for all
--ip <cidr,cidr>    IP allowlist; omit for no restriction
--cross-group       Let auto-routing retry in another group on failure
```

A key needs a quota to be useful: pass `--quota` or `--unlimited`. A zero-quota key is created enabled but cannot relay a single request.

```
everyapi token create --name prod --unlimited
everyapi token create --name ci --quota 1000000 \
  --models gpt-4o,claude-opus-4-7
everyapi token create --name byteplus-only --quota 1000000 \
  --group byteplus
everyapi token key 42
everyapi token revoke 42 -y
```

## Restrict every key you can

The three restriction flags are the cheapest security you will ever buy, and they cost one extra argument at creation:

- `--models` on a key that only ever calls one model turns a leak into a bounded loss.
- `--ip` on a key that only runs from your CI or your own server makes a leak useless off that network.
- `--quota` caps the blast radius in currency terms even when the key is otherwise unrestricted.
- `--expires` on anything handed to a contractor, a demo, or a script you will not maintain.

## Which key the CLI uses

`everyapi use` resolves the account's auto-group key — the one that routes across every group you can reach — and caches it in `credentials.json`. An account with no auto key falls back to its newest enabled key.

That lookup is deliberately offline and sticky: once a key is cached, it keeps being used, so a launch never silently re-picks between sessions. To change it, run `everyapi token switch`. To route one launch elsewhere without disturbing the cache, pass `everyapi use <tool> --group <name>`.

## Where keys live on disk

`~/.config/everyapi/credentials.json`, mode `0600` (or under `$XDG_CONFIG_HOME/everyapi/` when that is set). Fields:

```
api_base       Gateway URL; default https://api.everyapi.ai
access_token   Bearer for authenticated account API calls
relay_key      The cached sk-everyapi-… used by `everyapi use`
user_id        Cached identity, so `auth status` renders before its
               first round-trip
username
```

Tokens are stored in plaintext. Mode `0600` in a private home directory matches what `gh auth` and `aws configure` do, but it means any process running as your user can read it and call the API as you — the MCP server included. A platform keychain backend is planned and not shipped. Practical consequences:

- Do not `everyapi auth login` on a shared or public machine.
- Encrypt the home directory (FileVault, LUKS, ecryptfs) if the machine can be stolen.
- On suspected leakage: `everyapi auth logout` clears local credentials, then rotate the key from the dashboard or with `everyapi token revoke`.

## Keys in subprocess environments

`everyapi use` puts the relay key in a child process's environment for the tools that need it. A third-party CLI running in debug or verbose mode may log its environment. Before turning on a debug flag, check it does not dump `*_TOKEN` / `*_API_KEY`; before sharing a debug log, scrub it:

```
sed -i 's/sk-everyapi-[A-Za-z0-9]*/REDACTED/g' debug.log
```

Transparent mode (the default for Claude Code and Codex) avoids this entirely — the key never enters the child's environment. See the `use` topic.
