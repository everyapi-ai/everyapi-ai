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

// resolveModelArg pulls the model name out of rest, or prompts for it on
// a TTY when missing. The model is the first non-flag token, tolerating
// flags BEFORE it (`pull --node 5 mistral`) as well as after
// (`pull mistral --node 5`) — Go's stdlib flag stops at the first
// positional, so without this a flag-first invocation would drop the
// model. --node is the only flag and takes one value, so a `--node`/
// `-node` token is skipped together with the value that follows it; the
// surviving flags are returned for the caller's fs.Parse.
func resolveModelArg(op string, rest []string) (string, []string, error) {
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case a == "--node" || a == "-node":
			i++ // skip the flag's value too
			continue
		case strings.HasPrefix(a, "--node=") || strings.HasPrefix(a, "-node="):
			continue
		case strings.HasPrefix(a, "-"):
			continue // unknown flag — leave it for fs.Parse to reject
		}
		// First non-flag token is the model. Drop it, leaving the flags.
		remaining := append(append([]string{}, rest[:i]...), rest[i+1:]...)
		return a, remaining, nil
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
