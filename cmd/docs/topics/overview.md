# What EveryAPI is

EveryAPI is an AI API gateway: one OpenAI-compatible endpoint, one key, and one bill in front of 40+ upstream providers — OpenAI, Anthropic, Google, Azure, AWS Bedrock, DeepSeek, xAI, and the rest. It also runs a marketplace where ordinary users mount their own upstream capacity as sellers, and a BYO-GPU supply side where idle hardware serves open models.

## The problem it solves

Using more than one model provider directly means more than one account, key, quota, rate limit, billing page, SDK dialect, and outage to track. Switching a coding agent from Claude to GPT means finding out which environment variable that particular tool reads, whether its base URL wants a `/v1` suffix, and which of three auth-header conventions it uses.

EveryAPI collapses that into: one `sk-everyapi-…` key, one base URL, and `everyapi use <tool>`.

## The pieces

- **The gateway** — an OpenAI-compatible HTTP API at `https://api.everyapi.ai`. Accepts OpenAI Chat/Responses, Anthropic Messages, and Google Gemini request formats, converts between them where needed, and routes each request to a healthy upstream channel. See the `api` topic.
- **The dashboard** — `https://app.everyapi.ai`. Sign-up, API keys, usage charts, wallet, playground. See the `dashboard` topic.
- **The `everyapi` CLI** — a single Go binary that signs you in, points third-party AI clients at the gateway, and exposes the account surface (tokens, usage, wallet, marketplace) in the terminal. See the `cli` topic.
- **The MCP server** — the same binary, run as `everyapi mcp`, exposing account operations to AI agents over stdio JSON-RPC. See the `mcp` topic.
- **EveryAPI Connect** — the Tauri desktop app that installs and configures supported AI tools without a terminal. See the `desktop` topic.
- **The marketplace** — sellers mount their own provider channels and earn a share of what buyers spend through them. See the `seller` topic.
- **Edge nodes** — a supplier agent that serves open models from your own GPU through Ollama. See the `edge` topic.

## How a request flows

1. Your client sends an OpenAI-, Anthropic-, or Gemini-shaped request to the gateway with `Authorization: Bearer sk-everyapi-…`.
2. Token auth resolves the key to a user, a routing group, a quota, and any per-key model or IP restriction.
3. Distribution picks a channel that can serve the requested model — weighted random across eligible platform and seller channels, with automatic failover retries when one returns an error.
4. The request is converted to the upstream provider's dialect if it differs, sent, and the response converted back.
5. Settlement debits your wallet or subscription for the tokens actually used, and writes a row you can read back with `everyapi stats log list`.

## Where to go next

- Never used it before: read `quickstart`.
- Writing code against the API: read `api`, then `models` and `tokens`.
- Using Claude Code / Codex / another agent: read `use`.
- Money questions: read `billing`.
- Something is broken: read `troubleshooting`.
