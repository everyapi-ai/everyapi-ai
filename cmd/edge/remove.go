package edge

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
)

func edgeRemove(args []string) error {
	fs := flag.NewFlagSet("edge remove", flag.ContinueOnError)
	nodeFlag := fs.Int("node", 0, "Operate on this node id (default: active node)")
	keepBackend := fs.Bool("keep-backend", false, "Skip the DELETE /api/seller/edge/nodes/<id> call (useful when re-pointing the node at a different host)")
	yes := fs.Bool("yes", false, "Skip the interactive confirmation")
	if err := fs.Parse(args); err != nil {
		return err
	}

	nodeID, err := resolveNodeID(*nodeFlag)
	if err != nil {
		return err
	}

	if !*yes {
		cliout.Printf("This will delete local data for node #%d", nodeID)
		if !*keepBackend {
			cliout.Println(" AND delete the node row on the EveryAPI backend.")
		} else {
			cliout.Println(" but keep the backend node row.")
		}
		cliout.Println("Continue? Type 'yes' to confirm:")
		var resp string
		_, _ = fmt.Scanln(&resp)
		if resp != "yes" {
			return errors.New("aborted")
		}
	}

	// docker compose down -v — best effort. A failure shouldn't block
	// the backend delete (operator may have already manually
	// destroyed the containers).
	if dockerErr := ensureDocker(); dockerErr == nil {
		dir, dirErr := nodeDir(nodeID)
		if dirErr == nil {
			cliout.Printf("→ docker compose down -v (node #%d)\n", nodeID)
			if err := runComposeCmd(dir, projectFor(nodeID), "down", "-v"); err != nil {
				cliout.Printf("⚠ docker compose down failed (%v) — continuing anyway\n", err)
			}
		}
	} else {
		cliout.Printf("(docker not available; skipping `docker compose down`)\n")
	}

	if !*keepBackend {
		client, _, err := edgeClient()
		if err != nil {
			return err
		}
		if err := client.DeleteEdgeNode(cliout.WithCtx(), nodeID); err != nil {
			return fmt.Errorf("delete backend node row: %w", err)
		}
		cliout.Printf("✓ Backend node row %d deleted\n", nodeID)
	}

	dir, err := nodeDir(nodeID)
	if err == nil {
		if rmErr := os.RemoveAll(dir); rmErr != nil {
			cliout.Printf("⚠ couldn't remove local workdir %s (%v) — clean up manually\n", dir, rmErr)
		} else {
			cliout.Printf("✓ Removed local workdir %s\n", dir)
		}
	}
	if err := clearActiveNodeID(nodeID); err != nil {
		cliout.Printf("⚠ couldn't clear active-node pointer (%v)\n", err)
	}
	cliout.Println("Done.")
	return nil
}
