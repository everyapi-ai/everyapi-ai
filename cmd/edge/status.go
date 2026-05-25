package edge

import (
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
)

func edgeStatus(args []string) error {
	fs := flag.NewFlagSet("edge status", flag.ContinueOnError)
	nodeFlag := fs.Int("node", 0, "Operate on this node id (default: active node)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	nodeID, err := resolveNodeID(*nodeFlag)
	if err != nil {
		return err
	}

	// Backend view first — even if docker is unavailable here, the
	// dashboard may still see the node as online if it was started on
	// a different machine.
	client, _, err := edgeClient()
	if err != nil {
		return err
	}
	remote, rErr := client.GetEdgeNode(cliout.WithCtx(), nodeID)
	cliout.Printf(i18n.T("edge.status.node"), nodeID)
	if rErr != nil {
		cliout.Printf(i18n.T("edge.status.lookup_failed"), rErr)
	} else {
		cliout.Printf(" — %s\n", remote.Name)
		cliout.Printf("  %-12s%s%s\n", i18n.T("edge.field.status"), remote.Status, pauseSuffix(remote.Paused))
		if remote.LastSeenAt > 0 {
			cliout.Printf(i18n.T("edge.status.last_seen_line"), i18n.T("edge.field.last_seen"),
				time.Unix(remote.LastSeenAt, 0).Format(time.RFC3339),
				formatDuration(time.Since(time.Unix(remote.LastSeenAt, 0))))
		}
		if remote.AgentVer != "" {
			cliout.Printf("  %-12s%s\n", i18n.T("edge.field.agent_ver"), remote.AgentVer)
		}
		if len(remote.Models) > 0 {
			cliout.Printf("  %-12s%s\n", i18n.T("edge.field.models"), strings.Join(remote.Models, ", "))
		}
		if remote.Hardware != nil && remote.Hardware.GPUModel != "" {
			cliout.Printf(i18n.T("edge.status.hardware_line"), i18n.T("edge.field.hardware"),
				remote.Hardware.GPUModel, max1(remote.Hardware.GPUCount), remote.Hardware.VRAMGB)
		}
	}

	// Local docker view — only attempt if docker is on PATH; quietly
	// skip otherwise (a seller's main machine could be cli-only).
	if err := ensureDocker(); err != nil {
		cliout.Printf("%s", i18n.T("edge.status.docker_unavailable"))
		return nil
	}
	dir, err := nodeDir(nodeID)
	if err != nil {
		return err
	}
	cliout.Println(i18n.T("edge.status.local_containers"))
	if err := runComposeCmd(dir, projectFor(nodeID), "ps"); err != nil {
		return fmt.Errorf(i18n.T("edge.status.ps_failed"), err)
	}
	return nil
}

func pauseSuffix(p bool) string {
	if p {
		return i18n.T("edge.status.paused_suffix")
	}
	return ""
}

func max1(v int) int {
	if v < 1 {
		return 1
	}
	return v
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
