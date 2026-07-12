package mcp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	rootcmd "github.com/everyapi-ai/everyapi-ai/cmd"
	"github.com/everyapi-ai/everyapi-ai/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/internal/tools"
)

// clientCmd builds an exec.Cmd for a client CLI (claude / codex /
// gemini), resolving the executable the way `use` does — $PATH first,
// then the npm global bin dirs — so an npm-installed client living off
// $PATH still launches, with the resolved dir appended to the child's
// PATH for its co-located node. An unresolvable name falls through to
// the bare name so exec.Command's own not-found error surfaces.
func clientCmd(name string, argv []string) *exec.Cmd {
	path, env, ok := tools.LookupExecName(name)
	if !ok {
		path = name
	}
	cmd := exec.Command(path, argv...)
	if env != nil {
		cmd.Env = env
	}
	return cmd
}

// Install wires `everyapi mcp` into a supported MCP client's config so
// the user doesn't have to hand-edit JSON.
//
// Usage:
//
//	everyapi mcp install              # default: claude (back-compat)
//	everyapi mcp install claude
//	everyapi mcp install codex
//	everyapi mcp install gemini
//
// Why shell out instead of editing the client's settings.json
// ourselves: each client owns its own config layout (path, schema,
// scope precedence) and that surface changes across versions. The
// `<client> mcp add` subcommand is the stable API.
func Install(args []string) error {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		fmt.Fprint(os.Stdout, i18n.T("mcp.install_usage"))
		return nil
	}

	clientName, err := resolveClient(args, "install")
	if err != nil {
		return err
	}
	c, err := lookupClient(clientName)
	if err != nil {
		return err
	}

	// Same resolution `use` applies — a client sitting in an off-$PATH
	// npm global dir counts as installed here too.
	if _, _, ok := tools.LookupExecName(c.Name); !ok {
		return fmt.Errorf("`%s` CLI not found on PATH. %s\n\nAlternatively, paste the following into %s:\n\n%s", c.Name, c.InstallHint, clientConfigPath(c), c.ManualSnippet)
	}

	argv := c.AddArgv("everyapi", "everyapi", []string{"mcp"})
	stderrText, err := runClientAdd(c.Name, argv)
	// Graceful degradation for older client CLIs that predate the
	// `--scope` flag (claude/gemini inject `--scope user`): a bare
	// `unknown flag` failure would otherwise leave the server
	// unregistered. Retry once without the scope tokens so the install
	// still succeeds (in the client's default scope) rather than hard
	// failing. Modern CLIs never hit this branch.
	if err != nil && looksLikeUnknownFlag(stderrText) && hasScopeFlag(argv) {
		argv = stripScopeFlag(argv)
		stderrText, err = runClientAdd(c.Name, argv)
	}
	if err != nil {
		// The "already exists" error path: claude / codex / gemini
		// all variations of "MCP server <name> already exists ..."
		// when the registration is already in place. That's not an
		// error from our perspective — the user's goal (have
		// everyapi wired into this client) is already achieved.
		// Treat it as a no-op success and fall through to the
		// "Launch now?" prompt so the user isn't stuck retrying.
		if isAlreadyRegistered(stderrText) {
			fmt.Fprintf(os.Stdout, "Already registered in %s — nothing to do.\n", c.Name)
		} else {
			// Real failure: propagate the captured stderr to the
			// user so they see what went wrong, plus the exit-code
			// context.
			if stderrText != "" {
				fmt.Fprint(os.Stderr, stderrText)
				if !strings.HasSuffix(stderrText, "\n") {
					fmt.Fprintln(os.Stderr)
				}
			}
			return fmt.Errorf("%s mcp add failed: %w", c.Name, err)
		}
	} else {
		fmt.Fprintln(os.Stdout, strings.TrimSpace(`
Registered as `+"`everyapi`"+` in `+c.Name+`. Restart `+c.Name+` (or run
`+"`"+c.Name+` mcp list`+"`"+` to verify). Then try asking it
"what's my EveryAPI balance?" — the AI will invoke everyapi_status.
`))
	}

	// Now that the integration is wired, the natural next step is
	// to actually launch the client through EveryAPI — same call
	// the user would have made with `everyapi use <client>`. Offer
	// it on a TTY, skip off-TTY (CI / scripted install). EOF / Esc
	// is treated as "no, don't launch" rather than a real error so
	// the user can cancel without seeing a "operation failed" line.
	if !cliprompt.IsInteractive() {
		return nil
	}
	fmt.Fprintln(os.Stdout, "")
	launch, err := cliprompt.YesNo(
		bufio.NewReader(os.Stdin),
		fmt.Sprintf("Launch %s via EveryAPI now?", c.Name),
		true,
	)
	if err != nil {
		if errors.Is(err, cliprompt.ErrPickCancelled) || errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	if !launch {
		return nil
	}
	// rootcmd.Use execs into the chosen client; this call doesn't
	// return on success — control transfers to the client process.
	return rootcmd.Use([]string{c.Name})
}

// isAlreadyRegistered detects the "this MCP server is already in
// the client's config" error path across the three supported clients.
// Each one phrases the same condition a little differently — we
// match on substrings rather than exact text so a phrasing tweak
// in a client patch release doesn't suddenly break the idempotent
// path here.
//
// Patterns observed in the wild:
//
//	claude:  "MCP server everyapi already exists in local config"
//	codex:   "is already configured"  (anecdotal — keep the broader match)
//	gemini:  "already configured"     (anecdotal — keep the broader match)
//
// The lowercase compare neutralises capitalisation differences across
// client versions.
func isAlreadyRegistered(stderr string) bool {
	s := strings.ToLower(stderr)
	// Deliberately NOT gated on the server name (unlike isNotRegistered):
	// codex/gemini's "already configured" message (see patterns above) does
	// not name the server, so requiring "everyapi" here would misclassify a
	// legitimate idempotent re-install as a hard failure. The broader match
	// can in principle swallow an unrelated failure that happens to contain
	// these phrases, but that is far rarer than a normal repeat install.
	return strings.Contains(s, "already exists") || strings.Contains(s, "already configured")
}

// runClientAdd shells out `<client> <argv...>` capturing stderr (so the
// caller can inspect it for the already-registered / unknown-flag
// sentinels) while letting stdout pass through to the user.
func runClientAdd(name string, argv []string) (string, error) {
	cmd := clientCmd(name, argv)
	cmd.Stdout = os.Stdout
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf
	err := cmd.Run()
	return stderrBuf.String(), err
}

// looksLikeUnknownFlag reports whether the client rejected an argument
// it didn't recognize — the signal that an older CLI predates a flag we
// injected (e.g. `--scope`). Covers the common phrasings across Go/Node
// arg parsers.
func looksLikeUnknownFlag(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "unknown flag") ||
		strings.Contains(s, "unknown option") ||
		strings.Contains(s, "unrecognized") ||
		strings.Contains(s, "flag provided but not defined") ||
		strings.Contains(s, "invalid option")
}

// hasScopeFlag / stripScopeFlag manage the `--scope user` pair that the
// claude/gemini AddArgv builders inject, so install can drop it and retry
// on a CLI too old to know the flag. The value always immediately follows
// the flag.
func hasScopeFlag(argv []string) bool {
	for _, a := range argv {
		if a == "--scope" {
			return true
		}
	}
	return false
}

func stripScopeFlag(argv []string) []string {
	out := make([]string, 0, len(argv))
	for i := 0; i < len(argv); i++ {
		if argv[i] == "--scope" {
			i++ // also skip the flag's value ("user")
			continue
		}
		out = append(out, argv[i])
	}
	return out
}
