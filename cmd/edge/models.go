package edge

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/everyapi-ai/everyapi-ai/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
)

func edgeModels(args []string) error {
	// No args + TTY → sub-sub picker. Otherwise keep the original
	// usage-error so scripted callers see the documented failure
	// rather than a silent prompt hang.
	if len(args) == 0 {
		if !cliprompt.IsInteractive() {
			return errors.New(i18n.T("edge.models.usage"))
		}
		actions := []string{"list", "pull", "rm"}
		idx, err := cliprompt.Pick(i18n.T("edge.models.pick_action"),
			[]string{
				i18n.T("edge.models.pick_list"),
				i18n.T("edge.models.pick_pull"),
				i18n.T("edge.models.pick_rm"),
			})
		if err != nil {
			return err
		}
		args = []string{actions[idx]}
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
		model, rest2, err := resolveModelArg("pull", rest)
		if err != nil {
			return err
		}
		if err := fs.Parse(rest2); err != nil {
			return err
		}
		return execOllama(*nodeFlag, "pull", model)
	case "rm":
		model, rest2, err := resolveModelArg("rm", rest)
		if err != nil {
			return err
		}
		if err := fs.Parse(rest2); err != nil {
			return err
		}
		return execOllama(*nodeFlag, "rm", model)
	default:
		return fmt.Errorf(i18n.T("edge.models.unknown_subcommand"), sub)
	}
}

// resolveModelArg pulls the model name off the front of rest, or
// prompts for it on a TTY when missing. The first arg of rest that
// doesn't start with "-" is treated as the model name; an empty
// rest (or one that leads with a flag) on a TTY triggers the
// interactive prompt instead of a usage error.
func resolveModelArg(op string, rest []string) (string, []string, error) {
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		return rest[0], rest[1:], nil
	}
	if !cliprompt.IsInteractive() {
		return "", nil, fmt.Errorf(i18n.T("edge.models.usage_op"), op)
	}
	in := bufio.NewReader(os.Stdin)
	v, err := cliprompt.Line(in, i18n.T("edge.models.model_prompt"), "")
	if err != nil {
		return "", nil, err
	}
	return strings.TrimSpace(v), rest, nil
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
		return fmt.Errorf(i18n.T("edge.models.macos_native"),
			strings.Join(ollamaArgs, " "))
	}
	dir, err := nodeDir(nodeID)
	if err != nil {
		return err
	}
	composeArgs := append([]string{"exec", "ollama", "ollama"}, ollamaArgs...)
	return runComposeCmd(dir, projectFor(nodeID), composeArgs...)
}
