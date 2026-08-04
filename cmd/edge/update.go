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
	if err := rejectPositionals(fs); err != nil {
		return err
	}
	nodeID, err := resolveNodeID(*nodeFlag)
	if err != nil {
		return err
	}
	meta, err := loadNodeMeta(nodeID)
	if err != nil {
		return err
	}
	if err := ensureDocker(); err != nil {
		return err
	}
	dir, err := nodeDir(nodeID)
	if err != nil {
		return err
	}

	// Re-render docker-compose.yml from persisted meta BEFORE pulling. A new
	// CLI (shipped via `everyapi update`) can carry a changed compose
	// template; without re-rendering, `edge update` would only pull images
	// and leave the node on a stale compose — contradicting the generated
	// file's own "re-render on update" note. meta.Mode was persisted by
	// `edge start`; fall back to detection for legacy nodes predating it.
	// Gateway + image overrides are seeded from meta too, so an `edge start
	// --gateway / --agent-image / --ollama-image` survives a later `edge
	// update` instead of reverting to the writeCompose defaults.
	mode := meta.Mode
	persistMode := false
	if mode == "" {
		// Legacy node predating the persisted Mode field. Re-detect, then
		// persist the result so a later transient nvidia-smi failure can't
		// silently downgrade the node to CPU on every future update, and
		// surface the resolved mode like `edge start` does so a fallback
		// is visible.
		mode = resolveMode(ModeAuto)
		persistMode = true
		cliout.Printf(i18n.T("edge.start.mode"), mode, modeDescription(mode, true))
	}
	cd := composeData{
		NodeID:            nodeID,
		NodeName:          meta.NodeName,
		Mode:              mode,
		Gateway:           meta.Gateway,
		RegistrationToken: meta.RegistrationToken,
		AgentImage:        meta.AgentImage,
		OllamaImage:       meta.OllamaImage,
		MemoryGB:          memoryGBForMode(mode),
		Workloads:         meta.Workloads,
	}
	if _, err := writeCompose(dir, cd); err != nil {
		return err
	}
	if persistMode {
		meta.Mode = mode
		if err := saveNodeMeta(nodeID, meta); err != nil {
			cliout.Printf(i18n.T("edge.start.persist_mode_warn"), err)
		}
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
