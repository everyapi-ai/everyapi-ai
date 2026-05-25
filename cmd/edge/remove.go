package edge

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
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
		summary := fmt.Sprintf(i18n.T("edge.remove.confirm_summary"), nodeID)
		detail := fmt.Sprintf(i18n.T("edge.remove.detail_base"), nodeID)
		if !*keepBackend {
			detail += i18n.T("edge.remove.detail_with_backend")
		} else {
			detail += i18n.T("edge.remove.detail_keep_backend")
		}
		cliout.Println(detail)
		confirmed, err := cliprompt.YesNo(bufio.NewReader(os.Stdin), summary, false)
		if err != nil {
			return err
		}
		if !confirmed {
			return errors.New(i18n.T("edge.remove.aborted"))
		}
	}

	// docker compose down -v — best effort. A failure shouldn't block
	// the backend delete (operator may have already manually
	// destroyed the containers).
	if dockerErr := ensureDocker(); dockerErr == nil {
		dir, dirErr := nodeDir(nodeID)
		if dirErr == nil {
			cliout.Printf(i18n.T("edge.remove.down"), nodeID)
			if err := runComposeCmd(dir, projectFor(nodeID), "down", "-v"); err != nil {
				cliout.Printf(i18n.T("edge.remove.down_failed"), err)
			}
		}
	} else {
		cliout.Printf("%s", i18n.T("edge.remove.docker_unavailable"))
	}

	if !*keepBackend {
		client, _, err := edgeClient()
		if err != nil {
			return err
		}
		if err := client.DeleteEdgeNode(cliout.WithCtx(), nodeID); err != nil {
			return fmt.Errorf(i18n.T("edge.remove.delete_row_failed"), err)
		}
		cliout.Printf(i18n.T("edge.remove.row_deleted"), nodeID)
	}

	dir, err := nodeDir(nodeID)
	if err == nil {
		if rmErr := os.RemoveAll(dir); rmErr != nil {
			cliout.Printf(i18n.T("edge.remove.workdir_failed"), dir, rmErr)
		} else {
			cliout.Printf(i18n.T("edge.remove.workdir_removed"), dir)
		}
	}
	if err := clearActiveNodeID(nodeID); err != nil {
		cliout.Printf(i18n.T("edge.remove.clear_active_failed"), err)
	}
	cliout.Println(i18n.T("edge.remove.done"))
	return nil
}
