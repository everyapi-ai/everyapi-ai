# Edge nodes: selling your own GPU

An edge node turns idle hardware into marketplace supply: a small agent plus Ollama, serving open models to buyers routed through the gateway. `everyapi edge` condenses the deployment into one command set, so nobody has to hand-copy a compose file, fill in a `.env`, or move a registration token around.

## Requirements

- `docker` and `docker compose` v2. Compose v1 is end-of-life and not supported.
- On macOS, install Ollama natively — `brew install ollama && brew services start ollama`. Metal acceleration cannot run inside a container, so the macOS compose file has no Ollama service and the agent reaches the host's own through `host.docker.internal`.

## The usual path

```
everyapi auth login
everyapi edge register --name "rtx-4090"
everyapi edge start
everyapi edge models pull llama3.1:8b
everyapi edge status
everyapi edge logs -f
everyapi edge update
everyapi edge remove
```

## Full command surface

```
everyapi edge register [--name N] [--country CC] [--attach-to-channel ID]
everyapi edge start    [--mode auto|nvidia|rocm|macos|cpu] [--node ID]
everyapi edge status   [--node ID]   compose ps plus the dashboard view
everyapi edge stop     [--node ID]
everyapi edge logs     [-f] [--node ID]
everyapi edge models   {list | pull <m> | rm <m>} [--node ID]
everyapi edge update   [--node ID]   docker compose pull && up -d
everyapi edge rename   --name NEW [--country CC] [--region R] [--node ID]
everyapi edge pause    [--node ID]   Manual disable, sticky
everyapi edge resume   [--node ID]   Clear the manual pause
everyapi edge list                   Nodes on the active backend
everyapi edge remove   [--node ID] [--keep-backend]
```

`pause` is sticky on purpose: a node stays out of routing until `resume`, rather than rejoining on the next heartbeat. That is what makes it usable for "I need this machine back for a few hours".

## Hardware detection

`start` detects the accelerator and renders the compose file at runtime from a template — not from a static embedded YAML — for two reasons: container names are namespaced by node id, so several nodes on one host do not collide, and GPU passthrough differs per mode.

```
nvidia  deploy.resources.devices plus the nvidia driver
rocm    /dev/kfd and /dev/dri plus group_add: video
macos   no ollama container; the agent uses the host's native ollama
cpu     no accelerator
```

Override the detection with `--mode`.

## How the registration token is handled

`register` calls the backend with your existing `sk-everyapi-…` bearer. The backend returns a `registration_token` exactly once, then stores only its SHA-256 and never displays it again. The CLI writes it mode `0600` to `~/.local/share/everyapi/edge/<id>/node.json` (parent directory `0700`) and renders it into the compose environment at start time.

The token is deliberately never written to a `.env` file. `.env` files get committed.

## Attaching to an existing channel

`--attach-to-channel <id>` pairs a new node with a channel you already own instead of minting a new one. The id must appear as `channel_id` on one of your existing nodes; the CLI validates that before calling the backend so an invalid id gets a message naming your actual channels rather than a bare rejection.

## Removal

`remove` runs `docker compose down -v`, deletes the local node directory, and deletes the backend row. `--keep-backend` skips the last step. When the removed node was the last one on its paired channel, the channel goes with it.

## Through an AI agent

The MCP server exposes `everyapi_edge_list` and `everyapi_edge_status` read-only, and `everyapi_edge_remove` behind a required `confirm: "yes"`. See the `mcp` topic.
