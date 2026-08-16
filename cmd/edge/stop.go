package edge

import (
	"flag"
	"fmt"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
)

func edgeStop(args []string) error {
	fs := flag.NewFlagSet("edge stop", flag.ContinueOnError)
	nodeFlag := fs.Int("node", 0, "Operate on this node id (default: active node)")
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
	// A registered-but-never-started node has no docker-compose.yml; `docker compose -f docker-compose.yml down` would abort with a cryptic "no configuration file provided". Treat it as a clean no-op instead.
	if !composeFileExists(dir) {
		cliout.Printf(i18n.T("edge.not_started"), nodeID)
		return nil
	}
	cliout.Printf(i18n.T("edge.stop.down"), nodeID)
	if err := runComposeCmd(dir, projectFor(nodeID), "down"); err != nil {
		return fmt.Errorf(i18n.T("edge.stop.down_failed"), err)
	}
	cliout.Println(i18n.T("edge.stop.stopped"))
	return nil
}
