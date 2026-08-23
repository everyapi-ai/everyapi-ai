# Quickstart

From nothing to a working request. Two paths: the CLI, if you want to point an existing AI coding tool at the gateway, and a raw API key, if you are writing code.

## 1. Install the CLI

macOS, Homebrew:

```
brew tap everyapi-ai/tap && brew install everyapi
```

Linux and macOS, install script — it detects OS and architecture, verifies the SHA256, verifies the cosign signature when cosign is present, and installs to `~/.local/bin` (or `/usr/local/bin` as root):

```
curl -fsSL https://dl.everyapi.ai/install.sh | bash
```

Windows, PowerShell:

```
irm https://dl.everyapi.ai/install.ps1 | iex
```

Go:

```
go install github.com/everyapi-ai/everyapi-ai/v3@latest
```

Re-running the install command upgrades in place. `everyapi version update` picks the right upgrade path for however the binary was installed.

## 2. Sign in

```
everyapi auth login
```

The CLI renders a QR code and prints a short code and URL. Scan it with a phone already signed in to EveryAPI, or open the URL in a browser, and approve. The token lands in `~/.config/everyapi/credentials.json` with mode `0600`.

You never type a password on the machine you are signing in from — that is the point of the device flow. See the `cli` topic for the flags (`--no-browser`, `--no-qr`, `--api-base`).

Check it worked:

```
everyapi auth status
```

## 3a. Point an AI tool at the gateway

```
everyapi use claude
everyapi use codex
everyapi use gemini
everyapi use
```

The last form opens a picker over the tools actually installed on the machine. `everyapi use` sets whatever environment variables or config that particular client expects and execs it. For Claude Code and Codex the default is transparent mode: the client stays on its vendor's official API origin and the relay key never enters its environment. Full details in the `use` topic.

## 3b. Call the API directly

Mint a key:

```
everyapi token create --name dev-laptop --unlimited
everyapi token list
everyapi token key <id>
```

Or create one in the dashboard under API Keys. Either way the plaintext looks like `sk-everyapi-XXXXXXXX`.

Then it is the OpenAI API with a different base URL. Put the key in the environment rather than on the command line — an inline key lands in your shell history, and history files get shared, backed up, and pasted into bug reports:

```
export EVERYAPI_API_KEY=sk-everyapi-XXXXXXXX

curl https://api.everyapi.ai/v1/chat/completions \
  -H "Authorization: Bearer $EVERYAPI_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o","messages":[{"role":"user","content":"Hello!"}]}'
```

Python, with the official `openai` package. The SDK reads `OPENAI_API_KEY` from the environment on its own, but naming it explicitly keeps the key out of the source file either way:

```
import os
from openai import OpenAI

client = OpenAI(
    api_key=os.environ["EVERYAPI_API_KEY"],
    base_url="https://api.everyapi.ai/v1",
)
resp = client.chat.completions.create(
    model="gpt-4o",
    messages=[{"role": "user", "content": "Hello!"}],
)
print(resp.choices[0].message.content)
```

Any OpenAI SDK works — only the base URL and the key change. Streaming is standard SSE via `"stream": true`.

## 4. See what it cost

```
everyapi auth status
everyapi stats usage
everyapi stats log list
```

## Gateway region

Traffic from mainland China is faster through the accelerated gateway:

```
everyapi settings set gateway_region cn
```

That switches official gateway traffic to `https://api-cn.everyapi.ai`; `global` uses `https://api.everyapi.ai`. Interactive login asks once if the preference is unset. A self-hosted `--api-base` still wins over both.
