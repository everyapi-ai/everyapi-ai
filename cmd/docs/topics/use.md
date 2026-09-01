# Launching AI tools with everyapi use

The main reason to install the CLI. Every AI coding client has its own idea of which environment variable holds the key, whether the base URL wants a `/v1` suffix, and where its config file lives. `everyapi use` knows all of them.

```
everyapi use claude
everyapi use codex
everyapi use        Picker over the tools actually installed
```

## Syntax

```
everyapi use [<tool>] [--group <name> | --channel <name>] [--model <id>]
             [--sanitize] [--transparent[=false]] [-- tool args...]
```

```
--group <name>         Relay via the key bound to that routing group
--channel <name>       Alias of --group
--model <id>           Pick the model for this launch and remember it
--sanitize             Route through the local sanitizer proxy
--transparent[=false]  Keep the tool on its vendor's official origin
                       (on by default for claude and codex); =false
                       injects the gateway base URL and relay key
--                     End of parsing; the rest goes to the tool
```

A bare `--group` or `--channel` with no value opens a picker over your enabled keys' routing groups.

## Supported tools

```
claude      codex       opencode    gemini      antigravity
aider       goose       crush       cline       openclaw
continue    kilo        pi          vibe        copilot
droid       openhands   forge       llxprt      grok
qwen-code   kimi-code   hermes      librefang
open-webui  pi-web      deepseek-harness
```

`antigravity` and `librefang` are native integrations: they keep their own authentication path and never receive a copied relay key. `open-webui`, `pi-web`, and `deepseek-harness` serve a browser UI — EveryAPI registers the provider and the whole compatible catalog up front, and the model is chosen inside that UI rather than by a terminal picker.

Provider names are not tool names. Use `qwen-code` and `kimi-code` for those vendors' official clients; to reach a provider's models from some other client, select them from that client's live catalog.

## How each tool is pointed at the gateway

```
claude      ANTHROPIC_BASE_URL, ANTHROPIC_AUTH_TOKEN, live catalog
codex       OPENAI_API_KEY, persistent EveryAPI CODEX_HOME, a
            lifecycle-bound --profile, key-scoped model catalog
gemini      GEMINI_API_KEY, GOOGLE_GEMINI_BASE_URL, GEMINI_MODEL
aider       OpenAI-compatible env plus openai/<model> for LiteLLM
goose       GOOSE_PROVIDER=openai, GOOSE_MODEL, OPENAI_API_KEY
crush       Process-scoped CRUSH_GLOBAL_CONFIG, key from env
cline       Lifecycle-bound CLINE_PROVIDER_SETTINGS_PATH
openclaw    Process-scoped config with an env-backed SecretRef
continue    Lifecycle-bound CONTINUE_GLOBAL_DIR/config.yaml
kilo        Process-scoped KILO_CONFIG_CONTENT, env-backed key
pi          Isolated PI_CODING_AGENT_DIR holding models.json
pi-web      providers.everyapi in the durable PI_CODING_AGENT_DIR
vibe        Isolated VIBE_HOME/config.toml with api_key_env_var
copilot     Official COPILOT_PROVIDER_* BYOK environment
droid       Official --settings runtime file, one custom model
openhands   --override-with-envs plus process-only LLM_API_KEY
forge       Isolated FORGE_CONFIG with a pinned provider
llxprt      Isolated homes, pinned --provider/--baseurl/--model
grok        XAI_API_KEY, GROK_MODELS_BASE_URL, isolated GROK_HOME
qwen-code   OPENAI_API_KEY/_BASE_URL/_MODEL, scoped QWEN_HOME
kimi-code   KIMI_MODEL_* env, isolated KIMI_CODE_HOME
hermes      Generated HERMES_HOME/config.yaml, custom provider
open-webui  open-webui serve with OPENAI_API_BASE_URLS and _KEYS
deepseek-harness
            A provider entry in $DSH_HOME/settings.yaml plus creds
```

The pattern behind the table: wherever a client supports a process-scoped or lifecycle-bound configuration root, EveryAPI uses it and deletes it at exit, so a routed session cannot collide with your own configuration or with a concurrent launch on a different key.

## Transparent mode

Default for Claude Code and Codex. Instead of pointing the client at a third-party base URL, the CLI starts an ephemeral HTTP CONNECT proxy on a random loopback port, mints a per-run CA whose private key never leaves memory, and gives the child only the proxy URL, the public CA bundle, and a non-secret placeholder credential. Registered model routes are decrypted locally and relayed to EveryAPI with the real key; every other HTTPS host is raw CONNECT passthrough.

What that buys: the relay key is absent from the child's environment and from any generated config, and the client stays on `api.anthropic.com` / `api.openai.com`.

What it does not buy, stated plainly:

- It is experimental and process-scoped. The intercepted client side is HTTP/1.1; client-side HTTP/2, HTTP/3, WebSocket, certificate-pinned clients, and clients that ignore `HTTPS_PROXY` are not covered.
- It is not undetectable. A client can inspect proxy variables, the local certificate chain, sockets, or timing.
- The connector sees decrypted model content.
- It installs no system CA, needs no administrator access, and changes nothing outside the launch.
- `~/.config/everyapi/credentials.json` is still readable by any process running as you. This is credential-injection isolation, not a sandbox against a hostile child.
- Claude Code still treats the placeholder as API-key auth, so claude.ai connectors stay disabled even though the origin is official.
- If `ALL_PROXY` is your only proxy variable, transparent mode declines and the launch falls back to the injected path — Go's proxy resolution never reads `ALL_PROXY`. Set `HTTPS_PROXY` instead to keep it on.

Opt out per launch with `--transparent=false`. `--sanitize` composes with it rather than conflicting: the connector relays through the sanitizer.

## Choosing a model

At launch the CLI fetches the live catalog for the selected key and group, drops incompatible media and embedding protocols, and injects the result into the client's own selector. Use `/model` in Claude Code, Codex, Qwen Code, or Kimi Code; Grok's `/model`, or `hermes model` for Hermes.

Tools with a model-environment contract — Gemini, Aider, Goose, Crush, Cline, OpenClaw, Continue, Kilo, Pi, Vibe, Copilot, Droid, OpenHands, Forge, LLxprt, Hermes, Qwen Code, Kimi Code — open EveryAPI's own picker; `--model <id>` skips it. A non-interactive run deterministically takes the first compatible model.

Claude Code, Codex, OpenCode, and Grok are the four clients whose answer is remembered per tool in `settings.json` and reused on the next launch without asking again. They are only asked on the first launch, or when the remembered model stops being routable for the current key and group — a key that moved group, or a model the account lost. Pass a bare `--model` with no value to reopen the picker deliberately; that is also where the greyed-out unavailable entries are visible. The model-environment tools listed above remember nothing and open their picker on every interactive launch, so `--model <id>` remains the way to pin one for them.

## Reasoning level

`everyapi use codex` and `everyapi use pi` ask which reasoning level to launch at, once, then reuse the answer. A bare `--model` reopens that step along with the model picker, since the levels on offer belong to the model.

The two are gated differently because they know different things. Codex reads the levels its own bundled catalog publishes for that model and receives the choice as `model_reasoning_effort`; a model Codex has never heard of gets no step at all. Pi has no per-model table for a custom provider, so its step appears only where the gateway has verified the model takes an effort (`supports_thinking` on `/v1/models`), and it receives the choice as `defaultThinkingLevel`. A remembered level the current model does not offer is dropped rather than pinned.

## Safety preferences

On the first interactive launch the CLI asks whether to enable dangerous mode, and for Codex whether to bypass hook trust review. The answers are saved in `settings.json` and reused without prompting. The prompt defaults to Yes, but nothing dangerous is enabled before you confirm. Change them later with `everyapi settings set dangerous_mode false` / `codex_hook_trust_bypass false`.

## What the launched agent is told

A routed launch writes two process-scoped standards into the client's instruction surface: a pointer at this handbook, and the Artifact delivery standard below. Nothing the user owns is edited except by the one path that says so, and that path removes what it wrote.

The capability list exists because an agent that knows a command exists runs it, and one that does not asks you to go look it up — or answers from training data this platform is not in. So the agent is handed the commands that answer questions about this gateway and this account exactly, on a machine where the binary is already installed and already authenticated:

```
docs list | docs <topic> | docs search <query>  the handbook, offline
auth status                                     identity, quota, usage
stats usage | stats log stat                    consumption and spend
stats log summary | stats log list              per-model spend, recent logs
models list | models pricing                    routable models and rates
stats perf | stats upstream | doctor            model, provider, local health
token list                                      this account's keys, masked
```

Those are read-only and the agent runs them unprompted. **Everything else on the account and platform surface is fenced off**, and that split is the point rather than a detail: `everyapi` is not a read-only tool — `token revoke`, `wallet topup`, `seller withdraw`, and `edge remove` move money and destroy access. An agent handed the CLI as a uniform information source will eventually run one of them to "check" something, so the safe set is enumerated explicitly, everything outside it is declared state-changing and needs an explicit yes from you naming the exact command, and `token key <id>` is called out on its own — it changes nothing and still prints a credential in plaintext.

Computer Use has a separate, target-scoped safety split. When your task explicitly involves a local macOS app, the agent is told to proactively use `everyapi computer` rather than wait for you to name the command. It may inspect capabilities and permissions, resolve the in-scope app, list that app's windows, read its accessibility state, and take a window-scoped screenshot when pixels are necessary. It must not inspect unrelated apps or request a permission grant on its own.

Clicks, typing, paste, key presses, scrolling, dragging, secondary actions, and `computer permissions --request` require a concrete UI outcome in your request. That request authorizes the necessary actions within the target you named; without it, the agent must describe the proposed action and ask first. The instructions also require fresh state before and after mutation, and forbid blind retries after `action_outcome_unknown`.

The agent is also told the handbook covers EveryAPI and not your own project, without which a model instructed to consult it reaches for it on unrelated questions.

How it reaches each client depends on what that client documents, and the three answers are not equally strong:

**Through a named configuration surface** — guaranteed to be read:

```
claude      --append-system-prompt
codex       developer_instructions in the generated config.toml
opencode    instructions file listed in the generated config
kilo        the same, through its OpenCode-compatible config
crush       options.context_paths
aider       --read
continue    rules in the generated config.yaml
gemini      context.includeDirectories in the settings overlay EveryAPI owns
```

**Through the AGENTS.md convention** — best-effort. Every process-scoped home EveryAPI creates carries `EVERYAPI.md` and `AGENTS.md`, so a client that reads the cross-tool convention out of its own home picks the pointer up, and one that does not sees an unused markdown file in a directory deleted when it exits. This covers grok, qwen-code, kimi-code, cline, droid, forge, llxprt, openclaw, pi, and vibe without naming any of them: the files are written by the single function all prepared homes come through, so a client added later inherits it.

**Through a managed block** — for a client whose only documented surface is a file the user owns. Goose is the case: its global hints live in `~/.config/goose/.goosehints`, and `CONTEXT_FILE_NAME` selects a file name searched for in that hierarchy rather than a path that could be redirected. So the launch writes a delimited block into that file and removes exactly that block when it exits, leaving every other line untouched. It will not create the directory: a machine where Goose has never stored global configuration is left as it was. A launch killed hard enough to skip its cleanup leaves a block behind; the next one replaces it rather than stacking a second.

Two clients receive nothing: GitHub Copilot CLI and OpenHands, whose documented integration surface is the BYOK environment and a model override, with no instruction channel to attach to. Run `everyapi docs` yourself alongside them.

`EVERYAPI_NO_AGENT_CONTEXT=1` opts a launch out of all of it.

## Completion reports

Claude Code, Codex, OpenCode, and Kilo launches receive a process-scoped delivery standard through their documented instruction surfaces: after finishing a task, publish a sanitized self-contained HTML report through `everyapi artifacts share` and return the artifact URL. The launcher exports its own path as `EVERYAPI_CLI_PATH`, so the child uses the same authenticated installation even when `everyapi` is not on `PATH`. A publish failure never replaces the normal text result or invents a link, and no project-owned instruction file is ever edited.

## Examples

```
everyapi use claude
everyapi use claude --transparent=false
everyapi use codex --channel byteplus
everyapi use codex -- resume
everyapi use hermes --model gpt-5.1
everyapi use grok -- --model grok-4.5
everyapi use hermes -- --tui
```

Everything after `--` is forwarded to the tool verbatim.
For Codex, `resume` keeps the native current-directory filter; pass
`resume --all` explicitly when you want its global picker.
