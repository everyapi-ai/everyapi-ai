package edge

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
)

// ensureDocker verifies the supplier's machine has both the docker CLI and the v2 compose subcommand (NOT docker-compose v1 binary). Both must be on PATH; the v2 plugin is what `docker compose` resolves to. Returns a human-readable error pointing at install docs if either is missing — no auto-install (cross-platform sudo-needed install is too fragile for a CLI to attempt).
func ensureDocker() error {
	if _, err := exec.LookPath("docker"); err != nil {
		return errors.New(i18n.T("edge.docker.not_found"))
	}
	// `docker compose version` returns exit 0 + a version string when v2 is installed. v1 (the standalone `docker-compose` binary) is EOL'd and we don't try to support it.
	cmd := exec.Command("docker", "compose", "version")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf(i18n.T("edge.docker.compose_v2_missing"),
			err, strings.TrimSpace(string(out)))
	}
	return nil
}

// runComposeCmd execs `docker compose -f <workdir>/docker-compose.yml -p <project> <args...>` in the node's workdir with stdout/stderr wired through. -p pins the compose project name to a stable string so containers from different nodes don't share the default name (cwd-derived) and clobber each other's `ps`/`logs` output.
func runComposeCmd(workdir string, projectName string, args ...string) error {
	base := []string{"compose", "-p", projectName, "-f", "docker-compose.yml"}
	cmd := exec.Command("docker", append(base, args...)...)
	cmd.Dir = workdir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// composeFileExists reports whether the node's docker-compose.yml has been rendered yet. Only `edge start` writes it (via writeCompose), so a registered-but-never-started node has none — and invoking `docker compose -f docker-compose.yml ...` against a missing file aborts with a cryptic "no configuration file provided: not found". Callers use this to no-op cleanly instead.
func composeFileExists(workdir string) bool {
	_, err := os.Stat(filepath.Join(workdir, "docker-compose.yml"))
	return err == nil
}

// projectFor returns the stable -p name for a node. Keeping it derived from the node id (not the user-chosen --name) means renaming the node on the dashboard doesn't orphan the running containers.
func projectFor(nodeID int) string {
	return fmt.Sprintf("everyapi-edge-%d", nodeID)
}
