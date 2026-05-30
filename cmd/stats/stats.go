// Package stats groups the read-only observability commands —
// usage, perf, upstream, and the request log — under a single
// `everyapi stats <sub>` namespace so they don't each take a top-level
// slot. It's a thin dispatcher: every sub routes to the existing
// command package unchanged, so `everyapi stats log list` behaves
// exactly like the old `everyapi log list`.
package stats

import (
	"fmt"

	logcmd "github.com/everyapi-ai/everyapi-ai/cmd/log"
	"github.com/everyapi-ai/everyapi-ai/cmd/perf"
	"github.com/everyapi-ai/everyapi-ai/cmd/upstream"
	usagecmd "github.com/everyapi-ai/everyapi-ai/cmd/usage"
	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
)

// Run dispatches `everyapi stats <sub>`. Bare/help prints usage; an
// unknown sub errors. Each known sub forwards args[1:] to its original
// command package, so subcommands of the inner commands (e.g.
// `stats log list`) keep working.
func Run(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		cliout.Println(i18n.T("stats.usage"))
		return nil
	}
	switch args[0] {
	case "usage":
		return usagecmd.Run(args[1:])
	case "perf":
		return perf.Run(args[1:])
	case "upstream":
		return upstream.Run(args[1:])
	case "log":
		return logcmd.Run(args[1:])
	default:
		cliout.Println(i18n.T("stats.usage"))
		return fmt.Errorf(i18n.T("stats.unknown_sub"), args[0])
	}
}
