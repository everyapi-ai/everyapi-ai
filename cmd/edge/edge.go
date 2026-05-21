// Package edge implements `everyapi edge ...`, the supplier-side
// one-shot onboarding flow for the BYO-GPU marketplace. Each
// subcommand is a thin handler: load creds → call backend through the
// SDK → drive `docker compose` against a generated compose file under
// the workdir. The compose YAML is rendered at runtime by compose.go
// (text/template) — bundling pre-written YAML via go:embed would work
// but ties cli's release cadence to clients/edge/docker-compose.yml's,
// which we avoid.
//
// Subcommands:
//
//	everyapi edge register [--name N] [--country CC]      One-time: mint node + token, persist
//	everyapi edge start    [--mode auto|nvidia|rocm|macos|cpu] [--node ID]
//	everyapi edge status   [--node ID]                    `docker compose ps` + dashboard view
//	everyapi edge stop     [--node ID]
//	everyapi edge logs     [-f] [--node ID]
//	everyapi edge models   {list | pull <m> | rm <m>}  [--node ID]
//	everyapi edge update   [--node ID]                    docker compose pull && up -d
//	everyapi edge remove   [--node ID] [--keep-backend]
//	everyapi edge help
//
// Active node: `register` writes the new node id to ~/.config/everyapi/edge/active;
// subsequent commands act on the active node unless `--node ID` is passed
// to override.
package edge

import (
	"errors"
	"fmt"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
)

func Run(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		cliout.Println(edgeUsage)
		if len(args) == 0 {
			return errors.New("missing subcommand (try 'everyapi edge help')")
		}
		return nil
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "register":
		return edgeRegister(rest)
	case "list", "ls":
		return edgeList(rest)
	case "start":
		return edgeStart(rest)
	case "status":
		return edgeStatus(rest)
	case "stop":
		return edgeStop(rest)
	case "logs":
		return edgeLogs(rest)
	case "models":
		return edgeModels(rest)
	case "update":
		return edgeUpdate(rest)
	case "remove":
		return edgeRemove(rest)
	default:
		cliout.Printf("%s\n", edgeUsage)
		return fmt.Errorf("unknown 'edge' subcommand %q", sub)
	}
}

const edgeUsage = `everyapi edge — BYO-GPU supplier agent

USAGE
  everyapi edge <subcommand> [flags]

SUBCOMMANDS
  register [--name N] [--country CC]
        Mint a node + registration token on the EveryAPI backend, persist
        them to ~/.local/share/everyapi/edge/<node-id>/. Reuses your
        cli login (no separate token paste).

  list   (alias: ls)
        Print every node the signed-in user owns on the current backend,
        with api_base and account id at the top — handy when the seller
        dashboard shows nothing and you want to confirm which backend /
        account the CLI is actually talking to.

  start  [--mode auto|nvidia|rocm|macos|cpu] [--node ID] [--gateway URL]
        Detect GPU hardware, render docker-compose.yml into the node's
        workdir, then 'docker compose up -d'. Pulls ollama + agent images
        the first time. --mode auto is the default (nvidia-smi → nvidia,
        rocminfo → rocm, Darwin arm64 → macos, otherwise → cpu).

  status [--node ID]
        Show 'docker compose ps' next to the dashboard's view of the
        node (online / offline / paused, last seen, current models).

  stop   [--node ID]                 docker compose down
  logs   [-f|--follow] [--node ID]   docker compose logs
  update [--node ID]                 docker compose pull && up -d
  remove [--node ID] [--keep-backend]
        docker compose down -v + delete the local workdir. Also calls
        DELETE /api/seller/edge/nodes/<id> on the backend unless
        --keep-backend is set (useful when re-pointing a node at a
        different machine).

  models list  [--node ID]
  models pull  <model> [--node ID]
  models rm    <model> [--node ID]
        Wraps 'docker compose exec ollama ollama <op>'.

  help
        Print this message.

FIRST RUN
  $ everyapi login                    # if not already
  $ everyapi edge register --name "rtx-4090-tokyo"
  $ everyapi edge start
  $ everyapi edge models pull llama3.1:8b

REQUIREMENTS
  - docker + docker compose v2 on PATH
  - Linux/Windows: NVIDIA Container Toolkit (for nvidia mode) OR
    ROCm 5.7+ (for rocm mode)
  - macOS (Apple Silicon): native 'brew install ollama && brew services
    start ollama' — Metal acceleration isn't available through docker
`
