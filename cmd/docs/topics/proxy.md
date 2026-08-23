# The sanitizer proxy

A local HTTP proxy that masks secrets in outbound requests before they reach the gateway, and restores them in the response on the way back. The mapping table exists only in that process's memory: never written to disk, never sent over the wire, dropped on exit.

## Running it

```
everyapi proxy start [flags]  Run the proxy
everyapi proxy stop           Stop a running proxy (uses the PID file)
everyapi proxy status         Show running stats
everyapi proxy configure      Interactive detector and custom-pattern setup
```

Start flags:

```
--listen <addr>   Bind address; default 127.0.0.1:8888
--upstream <url>  Upstream gateway; defaults from credentials or
                  settings, else https://api.everyapi.ai
--detach          Re-exec in the background and return. Writes
                  ~/.config/everyapi/sanitizer.pid for `proxy stop`;
                  logs go to ~/.config/everyapi/sanitizer.log
```

Then point your SDK's base URL at `http://localhost:8888` instead of the gateway.

## What it detects

Built-in detectors cover API keys, PEM private keys, credit-card numbers, Chinese national ID numbers, and similar high-confidence patterns. Each detected substring is replaced with a stable placeholder, so a value that appears three times in one request is masked consistently and the model still sees a coherent document.

Detector toggles and custom regular expressions live in `~/.config/everyapi/sanitizer.json`. Edit them with `everyapi proxy configure`.

## Using it with `everyapi use`

```
everyapi use <tool> --sanitize
```

Off by default, and that default is deliberate: the mask-and-restore round trip corrupts agentic coding sessions, where the model reasons over exact file contents and a placeholder breaks the edit it is about to make. For non-agentic SDK traffic — a summarisation pipeline, a classification job, anything that treats the model as a stateless function — the standalone `everyapi proxy` is the right shape.

`--sanitize` composes with transparent mode rather than conflicting with it: the chain becomes child → connector → sanitizer → gateway, so masking and the Claude recovery response guard both apply on either launch path.

## What it is not

It is a privacy filter, not a guarantee. A detector matches patterns; a secret that does not look like one — a plain-language password, an internal hostname, a customer name — passes through untouched. Treat it as one layer, not as permission to send anything.
