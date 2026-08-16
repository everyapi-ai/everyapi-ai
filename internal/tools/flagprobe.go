package tools

import (
	"context"
	"os/exec"
	"time"
)

// flagProbeTimeout bounds a single probe launch. The probe execs the real tool binary, so it inherits whatever cold-start cost that binary has; 5s matches the npm-prefix probe in resolve.go and is well below the point where a user would read the launch as hung.
const flagProbeTimeout = 5 * time.Second

// FlagProbe answers one question about a pending launch: would prepending this flag to argv be REJECTED by the binary we are about to exec?
//
// It exists because the binary on $PATH is not always the tool. A terminal or session manager may install a shim that execs a wrapper, and the wrapper prepends its own flags before the real binary parses argv — cmux does exactly this for codex, injecting `--enable hooks --dangerously-bypass-hook-trust -c hooks.<Event>=...` so its session hooks fire with codex's real session id. EveryAPI cannot see that injection: it builds argv, hands it to exec, and the wrapper appends to it afterwards. When both sides prepend the SAME flag and the tool's parser declares that flag non-repeatable — codex's clap does, for both --dangerously-bypass-hook-trust and --dangerously-bypass-approvals-and-sandbox — argv parsing fails with "cannot be used multiple times" and the launch dies before any session starts.
//
// The probe settles this empirically rather than guessing: it runs the resolved executable through the same $PATH entry Exec will use, passing the candidate flag plus the tool's declared side-effect-free argv (Tool.FlagProbeArgs). Whatever a wrapper injects for a real launch, it injects for the probe too.
//
// Attribution is exit-code-only, never message matching: a duplicate-flag diagnostic is the tool's own wording and is free to change or localize. Comparing "fails with the flag" against "succeeds without it" pins the failure on the flag without reading a byte of the tool's output.
type FlagProbe struct {
	tool *Tool
	path string
	// ok reports whether the probe can run at all: a tool that declares no probe argv, or an executable that does not resolve, leaves every flag unexamined.
	ok bool
	// baseline caches the flag-free control run. It is identical for every flag, so it executes at most once per launch — and only after some flag probe has already failed and needs attributing.
	baseline     bool
	baselineDone bool
}

// NewFlagProbe prepares a probe for one launch of t. Resolution happens once here so repeated Accepts calls cost at most one subprocess each.
func NewFlagProbe(t *Tool) *FlagProbe {
	p := &FlagProbe{tool: t}
	if t == nil || len(t.FlagProbeArgs) == 0 {
		return p
	}
	path, err := ResolveExec(t)
	if err != nil {
		// Nothing to exec. Exec will fail with ErrToolNotFound and report the install hint; a resolution failure must not also change which flags a launch would have carried.
		return p
	}
	p.path = path
	p.ok = true
	return p
}

// Accepts reports whether flag can be added to this launch's argv.
//
// False means the flag is already reaching the tool from somewhere EveryAPI does not control, and adding a second copy would abort the launch. True is the answer for every case that is not a demonstrated rejection: a tool with no probe argv, an unresolvable executable, a probe that cannot run, or a baseline that fails for its own reasons. Adding the flag is the behavior users configured, so an inconclusive probe leaves it in place rather than silently dropping a preference.
func (p *FlagProbe) Accepts(flag string) bool {
	if p == nil || !p.ok || flag == "" {
		return true
	}
	if p.run(flag) {
		return true
	}
	// The flagged run failed. Only a passing control run makes the flag the culprit; if the tool rejects its own probe argv, the failure predates anything we added.
	if !p.baselineDone {
		p.baseline = p.run("")
		p.baselineDone = true
	}
	return !p.baseline
}

// run executes one probe and reports a clean exit. An empty flag runs the control arm. Ambient environment is inherited deliberately: a wrapper commonly decides whether to inject by reading its own env vars, so a scrubbed environment would probe a launch shape that never happens.
func (p *FlagProbe) run(flag string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), flagProbeTimeout)
	defer cancel()
	args := make([]string, 0, len(p.tool.FlagProbeArgs)+1)
	if flag != "" {
		args = append(args, flag)
	}
	args = append(args, p.tool.FlagProbeArgs...)
	cmd := exec.CommandContext(ctx, p.path, args...)
	// Mirror Exec's argv0 so a wrapper that branches on how it was invoked sees the same name it will see at launch.
	cmd.Args = append([]string{p.tool.ExecName}, args...)
	// Stdin, stdout, and stderr stay nil: the probe must not consume the user's stdin (the tool is about to read it) nor print to the terminal.
	return cmd.Run() == nil
}
