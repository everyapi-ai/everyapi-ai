package edge

import (
	"flag"
	"fmt"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
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

	cliout.Printf("→ docker compose pull (node #%d)\n", nodeID)
	if err := runComposeCmd(dir, projectFor(nodeID), "pull"); err != nil {
		return fmt.Errorf("docker compose pull failed: %w", err)
	}
	cliout.Println("→ docker compose up -d")
	if err := runComposeCmd(dir, projectFor(nodeID), "up", "-d"); err != nil {
		return fmt.Errorf("docker compose up failed: %w", err)
	}
	cliout.Println("✓ Updated. Run 'everyapi edge status' to check it picked up the new image.")
	return nil
}
