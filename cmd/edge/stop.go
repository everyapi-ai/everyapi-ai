package edge

import (
	"flag"
	"fmt"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
)

func edgeStop(args []string) error {
	fs := flag.NewFlagSet("edge stop", flag.ContinueOnError)
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
	cliout.Printf("→ docker compose down (node #%d)\n", nodeID)
	if err := runComposeCmd(dir, projectFor(nodeID), "down"); err != nil {
		return fmt.Errorf("docker compose down failed: %w", err)
	}
	cliout.Println("✓ Stopped. Run 'everyapi edge start' to bring it back online.")
	return nil
}
