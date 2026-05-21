package edge

import (
	"flag"
	"fmt"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
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
	cliout.Printf("→ Mode: %s%s\n", resolved, modeDescription(resolved, mode == ModeAuto))

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
	cliout.Printf("→ Wrote %s\n", composePath)

	if resolved == ModeMacOS {
		// Native ollama hint — don't BLOCK on it (advanced users may
		// run ollama via brew + custom port / a non-default install
		// path), but flag the dependency so a typical macOS user
		// doesn't get a baffling 'connection refused' a step later.
		cliout.Printf("ℹ macOS mode assumes ollama is running natively on :11434. If you haven't:\n")
		cliout.Printf("  brew install ollama && brew services start ollama\n")
	}

	cliout.Println("→ docker compose pull")
	if err := runComposeCmd(dir, projectFor(nodeID), "pull"); err != nil {
		return fmt.Errorf("docker compose pull failed: %w", err)
	}
	cliout.Println("→ docker compose up -d")
	if err := runComposeCmd(dir, projectFor(nodeID), "up", "-d"); err != nil {
		return fmt.Errorf("docker compose up failed: %w", err)
	}

	// Persist the resolved mode so `status` / `update` / `remove` know
	// which compose variant to re-render without re-detecting. Best
	// effort — a write failure here doesn't unwind the running
	// containers, so we warn instead of fail.
	meta.Mode = resolved
	if err := saveNodeMeta(nodeID, meta); err != nil {
		cliout.Printf("⚠ couldn't persist resolved mode (%v); 'edge update' may re-detect\n", err)
	}

	cliout.Printf("\n✓ Started. Dashboard should show node #%d as 'online' within ~30s.\n", nodeID)
	cliout.Printf("  everyapi edge status   — check state\n")
	cliout.Printf("  everyapi edge logs -f  — tail agent logs\n")
	cliout.Printf("  everyapi edge models pull llama3.1:8b   — install a model\n")
	return nil
}

func modeDescription(m Mode, fromAuto bool) string {
	if !fromAuto {
		return " (operator-set)"
	}
	switch m {
	case ModeNVIDIA:
		return " (detected: nvidia-smi reports a GPU)"
	case ModeROCm:
		return " (detected: rocminfo on PATH)"
	case ModeMacOS:
		return " (detected: Darwin arm64; ollama must be on the host)"
	case ModeCPU:
		return " (no GPU detected — chat throughput will be low; embeddings still work)"
	default:
		return ""
	}
}
