package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// requireAgentInstructions asserts what an agent actually has to act on — a command it can run, and the boundary it must not cross — rather than a title string that could survive the section being gutted.
func requireAgentInstructions(t *testing.T, body, where string) {
	t.Helper()
	for _, required := range []string{
		"EveryAPI CLI",
		"docs list",
		"auth status",
		"changes state",
		"EveryAPI Artifact delivery standard",
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("%s is missing %q:\n%s", where, required, body)
		}
	}
}

// TestCrushContextPathStaysInsideThePreparedHome is the property that makes this injection safe to ship: Crush's own global context file is ~/.config/crush/CRUSH.md, and a launcher that wrote there would leave the machine changed after a launch that was supposed to be process-scoped.
func TestCrushContextPathStaysInsideThePreparedHome(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv(TerminalModeEnvironment, "native")
	t.Setenv(crushModelEnv, "gpt-4o")

	env, err := prepareCrushWithModels("https://api.everyapi.ai", "", []Model{{ID: "gpt-4o"}})
	if err != nil {
		t.Fatal(err)
	}
	home := env[preparedHomeMarker]
	if home == "" {
		t.Fatal("Crush launch produced no prepared home")
	}
	t.Cleanup(func() { removePreparedHomeAfterQuiet(home) })

	body, err := os.ReadFile(filepath.Join(home, "crush.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Options struct {
			ContextPaths []string `json:"context_paths"`
		} `json:"options"`
	}
	if err := json.Unmarshal(body, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Options.ContextPaths) != 1 {
		t.Fatalf("Crush context_paths = %#v, want exactly the generated file", config.Options.ContextPaths)
	}
	path := config.Options.ContextPaths[0]
	if filepath.Dir(path) != home {
		t.Errorf("Crush context path %q escapes the prepared home %q", path, home)
	}
	contextBody, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	requireAgentInstructions(t, string(contextBody), "Crush context file")
}

// TestAiderReadsTheContextFileFromItsPreparedHome: `--read` is Aider's documented read-only context surface, and passing it on the command line leaves the user's own CONVENTIONS.md and .aider.conf.yml alone.
func TestAiderReadsTheContextFileFromItsPreparedHome(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv(TerminalModeEnvironment, "native")
	t.Setenv(aiderModelEnv, "gpt-4o")

	env, err := prepareAider("", "")
	if err != nil {
		t.Skipf("Aider preparation unavailable in this environment: %v", err)
	}
	home := env[preparedHomeMarker]
	if home == "" {
		t.Fatal("Aider launch produced no prepared home")
	}
	t.Cleanup(func() { removePreparedHomeAfterQuiet(home) })

	args := TakePreparedArgs(env)
	if len(args) != 2 || args[0] != "--read" {
		t.Fatalf("Aider argv = %#v, want --read <path>", args)
	}
	if filepath.Dir(args[1]) != home {
		t.Errorf("Aider context path %q escapes the prepared home %q", args[1], home)
	}
	body, err := os.ReadFile(args[1])
	if err != nil {
		t.Fatal(err)
	}
	requireAgentInstructions(t, string(body), "Aider context file")
}

// TestAgentContextCanBeDisabled keeps one escape hatch for a user who does not want the launcher adding anything to their agent's context.
func TestAgentContextCanBeDisabled(t *testing.T) {
	t.Setenv("EVERYAPI_NO_AGENT_CONTEXT", "1")
	t.Setenv(crushModelEnv, "gpt-4o")

	env, err := prepareCrushWithModels("https://api.everyapi.ai", "", []Model{{ID: "gpt-4o"}})
	if err != nil {
		t.Fatal(err)
	}
	home := env[preparedHomeMarker]
	t.Cleanup(func() { removePreparedHomeAfterQuiet(home) })

	if _, err := os.Stat(filepath.Join(home, agentContextFileName)); !os.IsNotExist(err) {
		t.Errorf("context file was generated despite EVERYAPI_NO_AGENT_CONTEXT")
	}
	body, _ := os.ReadFile(filepath.Join(home, "crush.json"))
	if strings.Contains(string(body), "context_paths") {
		t.Errorf("Crush config still declares context_paths:\n%s", body)
	}
}

// TestEveryPreparedHomeCarriesTheContextFiles is what makes this generic rather than a list of adapters: the files are written by newPreparedHome, the single door every process-scoped client home comes through, so a client added later inherits the reach with no wiring at all.
func TestEveryPreparedHomeCarriesTheContextFiles(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv(TerminalModeEnvironment, "native")

	home, err := newPreparedHome("generic-coverage")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { removePreparedHomeAfterQuiet(home) })

	for _, name := range []string{agentContextFileName, agentsConventionName} {
		body, err := os.ReadFile(filepath.Join(home, name))
		if err != nil {
			t.Errorf("prepared home is missing %s: %v", name, err)
			continue
		}
		requireAgentInstructions(t, string(body), name)
	}
}

// TestPreparedHomeHonoursTheOptOut keeps the escape hatch honest at the generic layer, not just per adapter.
func TestPreparedHomeHonoursTheOptOut(t *testing.T) {
	t.Setenv(disableEnv, "1")

	home, err := newPreparedHome("optout-coverage")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { removePreparedHomeAfterQuiet(home) })

	for _, name := range []string{agentContextFileName, agentsConventionName} {
		if _, err := os.Stat(filepath.Join(home, name)); !os.IsNotExist(err) {
			t.Errorf("%s was written despite %s", name, disableEnv)
		}
	}
}

// TestAgentContextIsNotWrittenOutsideAnEveryAPIDirectory states the rule the reviewer should check every new adapter against: the generic writer only ever targets a directory EveryAPI created.
func TestAgentContextIsNotWrittenOutsideAnEveryAPIDirectory(t *testing.T) {
	dir := t.TempDir()
	path, err := writeAgentContextFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != dir {
		t.Errorf("wrote to %q, outside the directory it was given (%q)", path, dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("wrote %d files, want exactly EVERYAPI.md and AGENTS.md", len(entries))
	}
}
