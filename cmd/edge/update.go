package edge

import (
	"flag"
	"fmt"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
)

func edgeUpdate(args []string) error {
	fs := flag.NewFlagSet("edge update", flag.ContinueOnError)
	nodeFlag := fs.Int("node", 0, "Operate on this node id (default: active node)")
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

	cliout.Printf(i18n.T("edge.update.pull"), nodeID)
	if err := runComposeCmd(dir, projectFor(nodeID), "pull"); err != nil {
		return fmt.Errorf(i18n.T("edge.update.pull_failed"), err)
	}
	cliout.Println(i18n.T("edge.update.up"))
	// --remove-orphans for defense-in-depth: keep the running set in sync
	// with the (possibly shrunk) compose file. Scoped to this node's -p
	// project, so it only removes this node's stale containers.
	if err := runComposeCmd(dir, projectFor(nodeID), "up", "-d", "--remove-orphans"); err != nil {
		return fmt.Errorf(i18n.T("edge.update.up_failed"), err)
	}
	cliout.Println(i18n.T("edge.update.updated"))
	return nil
}
