package edge

import (
	"flag"
)

func edgeLogs(args []string) error {
	fs := flag.NewFlagSet("edge logs", flag.ContinueOnError)
	nodeFlag := fs.Int("node", 0, "Operate on this node id (default: active node)")
	follow := fs.Bool("f", false, "Follow log output (same as docker compose logs -f)")
	followLong := fs.Bool("follow", false, "Follow log output")
	if err := fs.Parse(args); err != nil {
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
	logArgs := []string{"logs"}
	if *follow || *followLong {
		logArgs = append(logArgs, "-f")
	}
	return runComposeCmd(dir, projectFor(nodeID), logArgs...)
}
