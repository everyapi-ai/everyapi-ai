# Models, catalogs and routing groups

Which models you can call is a property of your key, not of the platform. The catalog you see is the intersection of what the deployment has channels for, what your routing group covers, and what the key itself is restricted to.

## Seeing the catalog

```
everyapi models list                 Every model id your group can route to
everyapi models pricing              Per-model rate sheet
everyapi models pricing --model gpt-4o
everyapi models groups               Routing groups your account can use
```

Over HTTP, the same catalog is `GET /v1/models` — and `GET /v1beta/models` for the Gemini shape, `GET /v1/models` with `x-api-key` plus `anthropic-version` for the Anthropic shape.

Always treat these as the authoritative answer to "what can I call". The set moves as channels are mounted and retired, and a hardcoded model list in your own code will drift.

## Model naming

Model ids pass through under the upstream vendor's own name: `gpt-4o`, `claude-opus-4-7`, `gemini-2.5-pro`, `deepseek-chat`. There is no EveryAPI-specific renaming scheme to learn, and no prefix to strip.

One exception is internal and invisible on the wire: when `everyapi use` injects a non-Claude model into a Claude-shaped client, it represents it with a Claude-compatible alias so the client's own validation accepts it. The real id is what is displayed and what is sent upstream.

## Routing groups

A routing group is a named pool of channels. Groups exist so a deployment can separate, for example, a cheap high-volume pool from a premium one, or platform-owned channels from marketplace ones. Your account's tier decides which groups it may reach.

A key is bound to a group at creation:

```
everyapi token create --name prod --unlimited
everyapi token create --name byteplus-only --quota 1000000 --group byteplus
```

An empty `--group` means **auto**: the key routes across every group the account can reach. That is the one key that behaves like "just give me the best available channel", and it is what `everyapi use` prefers by default.

`--cross-group` on a key allows auto-routing to retry in a different group after a failure, rather than failing within the original one.

## Which key decides which catalog

This trips people up more than anything else in this topic. A key pinned to one group only ever sees that group's models. If `/model` inside Claude Code or Codex shows a surprisingly short list, the launch is using a group-pinned key.

```
everyapi token switch
```

Pick `Auto` once and the CLI caches that key as the default. A one-off launch through a different pool is `everyapi use <tool> --group <name>`; a group override is deliberately never written to the cache.

## Model capability flags

The catalog carries capability metadata the gateway uses, and which `everyapi use` reads when it builds a client's model picker:

- Media and embedding models are filtered out of a chat client's catalog rather than offered and then failing.
- `supports_thinking` marks a model that accepts a reasoning-effort parameter. `everyapi use pi` only offers its reasoning step for models where the gateway has verified this.
- Stream-options support is a per-channel-kind property; a channel that cannot report usage in a stream is not asked to.

## Pricing

`everyapi models pricing` prints the per-model rate sheet in gateway quota units. Pricing can be per-call, per-token, cache-aware, or expression-driven for tiered rates. What you are actually charged for a request is settled after the response, from the usage the upstream reports. See the `billing` topic.
