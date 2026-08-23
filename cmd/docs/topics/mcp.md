# The MCP server

The `everyapi` binary is also a Model Context Protocol server. Run it as `everyapi mcp` and an AI agent — Claude Code, Cursor, Codex CLI, any MCP client — can check your balance, list seller channels, mount a channel, or withdraw earnings, without you opening a terminal.

## Registering it

```
everyapi mcp install [client]    Register with claude / codex / gemini
everyapi mcp uninstall [client]  Remove the registration
everyapi mcp status              Which clients have everyapi registered
everyapi mcp                     Run the stdio server
```

`everyapi mcp` with no arguments does the right thing for both callers: an MCP client that spawned it through a pipe gets the JSON-RPC server, and a human who typed it at a prompt gets the install / uninstall picker instead of a dead cursor.

Manual wiring, for a client `mcp install` does not cover — point `command` at the binary with `args: ["mcp"]`:

```
{
  "mcpServers": {
    "everyapi": {
      "command": "/abs/path/to/everyapi",
      "args": ["mcp"]
    }
  }
}
```

## Auth model, stated up front

- **No open ports.** The server is pure stdio JSON-RPC, forked by the host. It listens on no socket and no TCP port.
- **It reads `~/.config/everyapi/credentials.json` directly.** It has no auth flow of its own. Being able to read that file is being able to call every exposed tool as you. Any MCP host that can run a process as your user has full access. Do not install MCP hosts you do not trust.
- **You must have run `everyapi auth login` in a terminal at least once.** A background process cannot drive the device-code flow. Without credentials every tool returns an `isError` "not logged in" result pointing at the login command.
- **Money and destructive paths carry a friction step.** `everyapi_seller_withdraw` and `everyapi_edge_remove` require `confirm: "yes"`, which forces the agent to surface the action in its UI to a human rather than performing it silently. Read-only tools have no such requirement.

## Tools exposed

| Tool | Purpose |
| --- | --- |
| everyapi_status | Balance, used, request count |
| everyapi_topup | Returns the web top-up URL |
| everyapi_seller_list | Your marketplace channels |
| everyapi_seller_withdraw | Seller quota to main balance |
| everyapi_seller_eligibility | Read-only mount-gate checklist |
| everyapi_seller_add_key | Mount a channel from plain keys |
| everyapi_seller_add_oauth_codex_start | Start the Codex device flow |
| everyapi_seller_add_oauth_codex_poll | Poll it |
| everyapi_seller_add_oauth_claude_start | Start the Anthropic OAuth flow |
| everyapi_seller_add_oauth_claude_complete | Submit the pasted code#state |
| everyapi_edge_list | BYO-GPU nodes and their state |
| everyapi_edge_status | One node in detail |
| everyapi_edge_remove | Delete a node |
| everyapi_admin_marketplace_status | Marketplace flag (admin) |
| everyapi_admin_marketplace_set | Open or close it (admin) |

Inputs: `everyapi_seller_withdraw` takes `confirm: "yes"` and an optional `quota`; `everyapi_seller_add_key` takes `name`, `type`, `keys[]`, `models`, and optional `key_remarks[]` and `remark`; the OAuth start calls take `name` and `models`, and the poll and complete calls take the `flow_id` or `input` the start call handed back; `everyapi_edge_status` and `everyapi_edge_remove` take a `node_id`; `everyapi_edge_remove` and `everyapi_admin_marketplace_set` also require `confirm: "yes"`.

Call `everyapi_seller_eligibility` **before** asking a user for an API key: it reports the mount gates (marketplace open, account active, email verified, account age, prior usage, channel cap) so a failing account is told why up front instead of after submitting a credential.

Gemini's seller OAuth is not exposed over MCP. Its loopback listener's lifetime does not match a cross-tool-call lifecycle, so it stays a CLI-only flow: `everyapi seller add-oauth gemini`.

## What an OAuth conversation looks like

```
User: Add a ChatGPT seller channel, my-chatgpt, models gpt-4
AI   -> everyapi_seller_add_oauth_codex_start(
          {name: "my-chatgpt", models: "gpt-4"})
     <- "Go to the verification URI, enter USR-789, then say when done"
User: Done
AI   -> everyapi_seller_add_oauth_codex_poll({flow_id: "…"})
     <- "status=pending"
     … keep polling …
     <- "status=authorized — channel #314 mounted"
```

## Smoke test

```
everyapi mcp <<'EOF'
{"jsonrpc":"2.0","id":1,"method":"initialize"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call",
 "params":{"name":"everyapi_status","arguments":{}}}
EOF
```

Three JSON lines back: the initialize result, the tool list, and either your status or a not-logged-in `isError`.
