package edge

import (
	"flag"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
)

func edgeLogs(args []string) error {
	fs := flag.NewFlagSet("edge logs", flag.ContinueOnError)
	nodeFlag := fs.Int("node", 0, "Operate on this node id (default: active node)")
	follow := fs.Bool("f", false, "Follow log output (same as docker compose logs -f)")
	followLong := fs.Bool("follow", false, "Follow log output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := rejectPositionals(fs); err != nil {
		return err
	}
	nodeID, err := resolveNodeID(*nodeFlag)
	if err != nil {
		return err
	}
	if _, err := loadNodeMeta(nodeID); err != nil {
		return err
	}
	if err := ensureDocker(); err != nil {
		return err
	}
	dir, err := nodeDir(nodeID)
	if err != nil {
		return err
	}
	// No compose file => the node was never started, so there are no
	// containers to tail. Bail cleanly rather than letting compose emit
	// "no configuration file provided".
	if !composeFileExists(dir) {
		cliout.Printf(i18n.T("edge.not_started"), nodeID)
		return nil
	}
	logArgs := []string{"logs"}
	if *follow || *followLong {
		logArgs = append(logArgs, "-f")
	}
	return runComposeCmd(dir, projectFor(nodeID), logArgs...)
}
