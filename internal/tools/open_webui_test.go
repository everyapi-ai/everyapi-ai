package tools

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestOpenWebUILaunchUsesDocumentedProcessEnvironment(t *testing.T) {
	tool, err := Lookup("open-webui")
	if err != nil {
		t.Fatal(err)
	}
	if tool.ExecName != "open-webui" {
		t.Fatalf("ExecName = %q", tool.ExecName)
	}
	if !reflect.DeepEqual(tool.DefaultArgs, []string{"serve"}) {
		t.Fatalf("DefaultArgs = %v", tool.DefaultArgs)
	}
	env := tool.Env("https://api.everyapi.ai", "secret-relay-key")
	if env["OPENAI_API_BASE_URLS"] != "https://api.everyapi.ai/v1" {
		t.Fatalf("OPENAI_API_BASE_URLS = %q", env["OPENAI_API_BASE_URLS"])
	}
	if env["OPENAI_API_KEYS"] != "secret-relay-key" {
		t.Fatalf("OPENAI_API_KEYS = %q", env["OPENAI_API_KEYS"])
	}
	if env["ENABLE_PERSISTENT_CONFIG"] != "false" {
		t.Fatalf("ENABLE_PERSISTENT_CONFIG = %q", env["ENABLE_PERSISTENT_CONFIG"])
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	extra, err := tool.Prepare("https://api.everyapi.ai", "secret-relay-key")
	if err != nil {
		t.Fatal(err)
	}
	if extra["DATA_DIR"] != filepath.Join(home, ".open-webui") {
		t.Fatalf("DATA_DIR = %q", extra["DATA_DIR"])
	}
}
