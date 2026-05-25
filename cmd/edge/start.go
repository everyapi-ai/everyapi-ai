package edge

import (
	"flag"
	"fmt"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
)

func edgeStart(args []string) error {
	fs := flag.NewFlagSet("edge start", flag.ContinueOnError)
	nodeFlag := fs.Int("node", 0, "Operate on this node id (default: active node from 'edge register')")
	modeFlag := fs.String("mode", "auto", "Hardware mode: auto|nvidia|rocm|macos|cpu")
	gatewayFlag := fs.String("gateway", "", "Override the gateway WS URL (default: derived from your cli login's API base)")
	agentImageFlag := fs.String("agent-image", "", "Pin a specific agent image (default: ghcr.io/everyapi-ai/everyapi-edge:latest)")
	ollamaImageFlag := fs.String("ollama-image", "", "Pin a specific ollama image (default: ollama/ollama:latest for nvidia/cpu, ollama/ollama:rocm for rocm; ignored on macOS)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Order matters: identity checks (login → active node → local
	// metadata) before docker presence. A user who hasn't run
	// 'register' first should hear about that, not about docker
	// compose v2.
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

	mode, err := parseMode(*modeFlag)
	if err != nil {
		return err
	}
	resolved := resolveMode(mode)
	cliout.Printf(i18n.T("edge.start.mode"), resolved, modeDescription(resolved, mode == ModeAuto))

	gateway := *gatewayFlag
	if gateway == "" {
		gateway = meta.Gateway
	}

	dir, err := nodeDir(nodeID)
	if err != nil {
		return err
	}
	cd := composeData{
		NodeID:            nodeID,
		NodeName:          meta.NodeName,
		Mode:              resolved,
		Gateway:           gateway,
		RegistrationToken: meta.RegistrationToken,
		AgentImage:        *agentImageFlag,
		OllamaImage:       *ollamaImageFlag,
	}
	composePath, err := writeCompose(dir, cd)
	if err != nil {
		return err
	}
	cliout.Printf(i18n.T("edge.start.wrote"), composePath)

	if resolved == ModeMacOS {
		// Native ollama hint — don't BLOCK on it (advanced users may
		// run ollama via brew + custom port / a non-default install
		// path), but flag the dependency so a typical macOS user
		// doesn't get a baffling 'connection refused' a step later.
		cliout.Printf("%s", i18n.T("edge.start.macos_hint"))
		cliout.Printf("%s", i18n.T("edge.start.macos_hint_cmd"))
	}

	cliout.Println(i18n.T("edge.start.pull"))
	if err := runComposeCmd(dir, projectFor(nodeID), "pull"); err != nil {
		return fmt.Errorf(i18n.T("edge.start.pull_failed"), err)
	}
	cliout.Println(i18n.T("edge.start.up"))
	if err := runComposeCmd(dir, projectFor(nodeID), "up", "-d"); err != nil {
		return fmt.Errorf(i18n.T("edge.start.up_failed"), err)
	}

	// Persist the resolved mode so `status` / `update` / `remove` know
	// which compose variant to re-render without re-detecting. Best
	// effort — a write failure here doesn't unwind the running
	// containers, so we warn instead of fail.
	meta.Mode = resolved
	if err := saveNodeMeta(nodeID, meta); err != nil {
		cliout.Printf(i18n.T("edge.start.persist_mode_warn"), err)
	}

	cliout.Printf(i18n.T("edge.start.started"), nodeID)
	cliout.Printf("%s", i18n.T("edge.start.hint_status"))
	cliout.Printf("%s", i18n.T("edge.start.hint_logs"))
	cliout.Printf("%s", i18n.T("edge.start.hint_models"))
	return nil
}

func modeDescription(m Mode, fromAuto bool) string {
	if !fromAuto {
		return i18n.T("edge.start.desc_operator_set")
	}
	switch m {
	case ModeNVIDIA:
		return i18n.T("edge.start.desc_nvidia")
	case ModeROCm:
		return i18n.T("edge.start.desc_rocm")
	case ModeMacOS:
		return i18n.T("edge.start.desc_macos")
	case ModeCPU:
		return i18n.T("edge.start.desc_cpu")
	default:
		return ""
	}
}
