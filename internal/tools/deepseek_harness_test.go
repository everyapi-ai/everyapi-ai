package tools

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDeepSeekHarnessPreparationPreservesUserConfigAndWritesPrivateCredential(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DSH_HOME", "")
	dshHome := filepath.Join(home, ".dsh")
	if err := os.MkdirAll(dshHome, 0o700); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(dshHome, "settings.yaml")
	if err := os.WriteFile(settingsPath, []byte("# keep me\nui:\n  theme: dark\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	credentialsPath := filepath.Join(dshHome, ".credentials.yaml")
	if err := os.WriteFile(credentialsPath, []byte("OTHER_KEY: keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	env, err := prepareDeepSeekHarnessWithModels(
		"https://api.everyapi.ai",
		"sk-everyapi-test",
		[]Model{
			{ID: "chat-model", SupportedEndpointTypes: []string{"openai"}},
			{ID: "responses-only", SupportedEndpointTypes: []string{"openai-response"}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(env) != 0 {
		t.Fatalf("env = %#v, want no process credential copy", env)
	}

	settings, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# keep me", "theme: dark", "everyapi:", "displayName: EveryAPI",
		"apiKeyEnv: EVERYAPI_API_KEY", "api: openai-completions",
		"baseURL: https://api.everyapi.ai/v1", "id: chat-model",
	} {
		if !strings.Contains(string(settings), want) {
			t.Errorf("settings missing %q:\n%s", want, settings)
		}
	}
	if strings.Contains(string(settings), "responses-only") {
		t.Fatalf("responses-only model leaked into chat-completions config:\n%s", settings)
	}

	credentials, err := os.ReadFile(credentialsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(credentials), "OTHER_KEY: keep") ||
		!strings.Contains(string(credentials), "EVERYAPI_API_KEY: sk-everyapi-test") {
		t.Fatalf("credentials were not merged:\n%s", credentials)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(credentialsPath)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("credential mode = %v; want 0600", info.Mode().Perm())
		}
	}
}

func TestDeepSeekHarnessPreparationHonorsDSHHome(t *testing.T) {
	home := t.TempDir()
	dshHome := filepath.Join(t.TempDir(), "custom-dsh")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DSH_HOME", dshHome)

	_, err := prepareDeepSeekHarnessWithModels(
		"https://api.everyapi.ai",
		"sk-everyapi-test",
		[]Model{{ID: "chat-model", SupportedEndpointTypes: []string{"openai"}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"settings.yaml", ".credentials.yaml"} {
		if _, err := os.Stat(filepath.Join(dshHome, name)); err != nil {
			t.Fatalf("custom DSH_HOME missing %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".dsh")); !os.IsNotExist(err) {
		t.Fatalf("default Harness home was unexpectedly created: %v", err)
	}
}

func TestDeepSeekHarnessPreparationRejectsIncompatibleSettingsWithoutOverwriting(t *testing.T) {
	for name, existing := range map[string]string{
		"root section": "llm-pi-ai: keep-me\n",
		"providers":    "llm-pi-ai:\n  providers: [keep, me]\n",
	} {
		t.Run(name, func(t *testing.T) {
			dshHome := t.TempDir()
			t.Setenv("DSH_HOME", dshHome)
			settingsPath := filepath.Join(dshHome, "settings.yaml")
			if err := os.WriteFile(settingsPath, []byte(existing), 0o600); err != nil {
				t.Fatal(err)
			}

			_, err := prepareDeepSeekHarnessWithModels(
				"https://api.everyapi.ai",
				"sk-everyapi-test",
				[]Model{{ID: "chat-model", SupportedEndpointTypes: []string{"openai"}}},
			)
			if err == nil || !strings.Contains(err.Error(), "must be a YAML mapping") {
				t.Fatalf("err = %v, want incompatible mapping error", err)
			}
			body, readErr := os.ReadFile(settingsPath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(body) != existing {
				t.Fatalf("settings changed after rejection:\n%s", body)
			}
			if _, statErr := os.Stat(filepath.Join(dshHome, ".credentials.yaml")); !os.IsNotExist(statErr) {
				t.Fatalf("credential file created after rejection: %v", statErr)
			}
		})
	}
}

func TestDeepSeekHarnessPreparationRejectsMissingCompatibleModels(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DSH_HOME", "")
	_, err := prepareDeepSeekHarnessWithModels(
		"https://api.everyapi.ai",
		"sk-everyapi-test",
		[]Model{{ID: "responses-only", SupportedEndpointTypes: []string{"openai-response"}}},
	)
	if err == nil || !strings.Contains(err.Error(), "OpenAI chat-completions") {
		t.Fatalf("err = %v, want compatible-model error", err)
	}
}

func TestDeepSeekHarnessRegistryUsesOfficialPackageAndWebLauncher(t *testing.T) {
	tool, err := Lookup("deepseek-harness")
	if err != nil {
		t.Fatal(err)
	}
	if tool.ExecName != "dsh" {
		t.Fatalf("ExecName = %q, want dsh", tool.ExecName)
	}
	if tool.InstallCmd != "npm install -g @deepseek-ai/dsh" {
		t.Fatalf("InstallCmd = %q", tool.InstallCmd)
	}
	if strings.Join(tool.DefaultArgs, " ") != "web" {
		t.Fatalf("DefaultArgs = %v, want [web]", tool.DefaultArgs)
	}
	if tool.RequiredEndpoint != "openai" || tool.prepareCatalogFn == nil {
		t.Fatalf("registry entry does not configure the EveryAPI provider: %#v", tool)
	}
}
