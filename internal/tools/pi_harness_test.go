package tools

import (
	"os"
	"strings"
	"testing"
)

func TestPiHarnessUsesTheWebEntrypointAndDurablePiProfile(t *testing.T) {
	tool, err := Lookup("pi-harness")
	if err != nil {
		t.Fatal(err)
	}
	if tool.ExecName != "pi-harness" {
		t.Fatalf("ExecName = %q, want pi-harness", tool.ExecName)
	}
	if len(tool.DefaultArgs) != 0 {
		t.Fatalf("DefaultArgs = %v, want none: the web bin is the executable", tool.DefaultArgs)
	}
	if tool.RequiredEndpoint != "openai" || tool.AlternativeEndpoint != "openai-response" {
		t.Fatalf("endpoint contract = %q/%q, want openai/openai-response", tool.RequiredEndpoint, tool.AlternativeEndpoint)
	}
	if tool.prepareCatalogFn == nil {
		t.Fatal("Pi Harness must prepare the durable Pi model catalog")
	}
	if !strings.Contains(tool.InstallCmd, "github:pi-harness/pi-harness") {
		t.Fatalf("InstallCmd = %q, want the official GitHub repository", tool.InstallCmd)
	}
}

func TestPiHarnessSelectsEveryAPIAsTheActiveProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", "")
	t.Setenv("PI_AGENT_DIR", home+"/agent")
	env, err := preparePiHarnessWithModels("https://api.everyapi.ai", "ignored", []Model{
		{ID: "gpt-5.6-sol"},
		{ID: "deepseek-v4-flash"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if env["PI_HARNESS_PROVIDER"] != "everyapi" || env["PI_HARNESS_MODEL"] != "deepseek-v4-flash" {
		t.Fatalf("Pi Harness active model env = %#v", env)
	}
	if env["PI_AGENT_DIR"] == "" {
		t.Fatal("PI_AGENT_DIR was not configured")
	}
	if _, err := os.Stat(env["PI_AGENT_DIR"] + "/models.json"); err != nil {
		t.Fatalf("durable Pi model catalog was not written: %v", err)
	}
}
