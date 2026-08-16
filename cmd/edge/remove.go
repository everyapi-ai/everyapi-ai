package edge

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
)

func edgeRemove(args []string) error {
	fs := flag.NewFlagSet("edge remove", flag.ContinueOnError)
	nodeFlag := fs.Int("node", 0, "Operate on this node id (default: active node)")
	keepBackend := fs.Bool("keep-backend", false, "Skip the DELETE /api/seller/edge/nodes/<id> call (useful when re-pointing the node at a different host)")
	yes := fs.Bool("yes", false, "Skip the interactive confirmation")
	// `-y` writes the same *bool via fs.BoolVar so either --yes or -y skips the prompt, matching the other destructive commands (uninstall / pause / stop).
	fs.BoolVar(yes, "y", false, "alias for --yes")
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

	// docker compose down -v — best effort. A failure shouldn't block the backend delete (operator may have already manually destroyed the containers).
	if dockerErr := ensureDocker(); dockerErr == nil {
		dir, dirErr := nodeDir(nodeID)
		// Only attempt compose down when the node was actually started here (docker-compose.yml exists). A registered-but-never-started node has none, and `compose -f docker-compose.yml down` against a missing file aborts with a cryptic "no configuration file provided" (surfaced via down_failed) for what is a no-op. Mirror status.go's composeFileExists guard.
		if dirErr == nil && composeFileExists(dir) {
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
		if *keepBackend {
			// The backend node row survives (the flag's purpose is re-pointing the node at a different host), and node.json holds the ONLY copy of the raw registration token — the backend keeps just its sha256. Wiping the whole workdir here would leave a live backend row that can never be reconnected. So preserve node.json; drop only the regenerable compose file and container data.
			kept := true
			for _, name := range []string{"docker-compose.yml", "data"} {
				p := filepath.Join(dir, name)
				if rmErr := os.RemoveAll(p); rmErr != nil {
					kept = false
					// Must NOT reuse workdir_failed here: its hint tells the user to `sudo rm -rf <workdir>`, which would delete the node.json this branch exists to preserve. The ollama `data` dir is root-owned, so this EPERM is the common case — point only at the specific leftover subpath and reassure the token is kept.
					cliout.Printf(i18n.T("edge.remove.workdir_kept_partial"), dir, p, rmErr, p)
				}
			}
			if kept {
				cliout.Printf(i18n.T("edge.remove.workdir_kept"), dir)
			}
		} else if rmErr := os.RemoveAll(dir); rmErr != nil {
			cliout.Printf(i18n.T("edge.remove.workdir_failed"), dir, rmErr, dir)
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
