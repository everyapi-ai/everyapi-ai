package edge

import (
	"errors"
	"flag"
	"fmt"
	"strings"
)

func edgeModels(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: everyapi edge models {list | pull <model> | rm <model>} [--node ID]")
	}
	sub := args[0]
	rest := args[1:]

	fs := flag.NewFlagSet("edge models", flag.ContinueOnError)
	nodeFlag := fs.Int("node", 0, "Operate on this node id (default: active node)")

	switch sub {
	case "list":
		if err := fs.Parse(rest); err != nil {
			return err
		}
		return execOllama(*nodeFlag, "list")
	case "pull":
		if len(rest) == 0 {
			return errors.New("usage: everyapi edge models pull <model>")
		}
		model := rest[0]
		if err := fs.Parse(rest[1:]); err != nil {
			return err
		}
		return execOllama(*nodeFlag, "pull", model)
	case "rm":
		if len(rest) == 0 {
			return errors.New("usage: everyapi edge models rm <model>")
		}
		model := rest[0]
		if err := fs.Parse(rest[1:]); err != nil {
			return err
		}
		return execOllama(*nodeFlag, "rm", model)
	default:
		return fmt.Errorf("unknown 'edge models' subcommand %q (expected: list | pull | rm)", sub)
	}
}

// execOllama runs `docker compose exec ollama ollama <args...>` against
// the resolved node's workdir. macOS mode has no ollama container in
// compose (ollama is host-native), so we report a friendlier error
// pointing the operator at the native CLI.
func execOllama(explicitNode int, ollamaArgs ...string) error {
	nodeID, err := resolveNodeID(explicitNode)
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
	if meta.Mode == ModeMacOS {
		return fmt.Errorf("macOS mode uses native ollama (no container) — run 'ollama %s' directly on the host",
			strings.Join(ollamaArgs, " "))
	}
	dir, err := nodeDir(nodeID)
	if err != nil {
		return err
	}
	composeArgs := append([]string{"exec", "ollama", "ollama"}, ollamaArgs...)
	return runComposeCmd(dir, projectFor(nodeID), composeArgs...)
}
