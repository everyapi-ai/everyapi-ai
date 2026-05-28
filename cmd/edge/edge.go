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
//	everyapi edge register [--name N] [--country CC] [--attach-to-channel ID]
//	                                                      One-time: mint node + token, persist
//	everyapi edge start    [--mode auto|nvidia|rocm|macos|cpu] [--node ID]
//	everyapi edge status   [--node ID]                    `docker compose ps` + dashboard view
//	everyapi edge stop     [--node ID]
//	everyapi edge logs     [-f] [--node ID]
//	everyapi edge models   {list | pull <m> | rm <m>}  [--node ID]
//	everyapi edge update   [--node ID]                    docker compose pull && up -d
//	everyapi edge rename   --name NEW [--country CC] [--region R] [--node ID]
//	                                                      Backend rename + location edit
//	everyapi edge pause    [--node ID]                    Manual disable (sticky)
//	everyapi edge resume   [--node ID]                    Clear manual disable
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

	"github.com/everyapi-ai/everyapi-sdk/api"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
)

func Run(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		cliout.Println(edgeUsage())
		if len(args) == 0 {
			return errors.New(i18n.T("edge.usage.missing_subcommand"))
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
	case "rename":
		return edgeRename(rest)
	case "pause":
		return edgeSetStatus(rest, "pause", api.EdgeNodeActionDisable, "edge.pause.done")
	case "resume":
		return edgeSetStatus(rest, "resume", api.EdgeNodeActionEnable, "edge.resume.done")
	case "remove":
		return edgeRemove(rest)
	default:
		cliout.Printf("%s\n", edgeUsage())
		return fmt.Errorf(i18n.T("edge.usage.unknown_subcommand"), sub)
	}
}

func edgeUsage() string {
	return i18n.T("edge.usage.text")
}
