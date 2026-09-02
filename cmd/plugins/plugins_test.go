package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
)

func captureOutput(t *testing.T) *bytes.Buffer {
	t.Helper()
	buffer := &bytes.Buffer{}
	previous := cliout.Out
	cliout.Out = buffer
	t.Cleanup(func() { cliout.Out = previous })
	return buffer
}

func installInvoker(t *testing.T, invoker codexInvoker) {
	t.Helper()
	previous := invokeCodex
	invokeCodex = invoker
	t.Cleanup(func() { invokeCodex = previous })
}

func fixtureInvoker(t *testing.T, root string, calls *[][]string) codexInvoker {
	t.Helper()
	return func(_ context.Context, args []string, _ time.Duration, target any) error {
		*calls = append(*calls, append([]string(nil), args...))
		var value any = map[string]any{}
		switch strings.Join(args, " ") {
		case "plugin list --available --json":
			value = map[string]any{
				"installed": []any{},
				"available": []any{map[string]any{
					"pluginId": "review-kit@personal", "name": "review-kit", "marketplaceName": "personal", "version": "1.2.3", "installed": false, "enabled": false,
					"source": map[string]any{"source": "local", "path": root}, "installPolicy": "AVAILABLE", "authPolicy": "ON_USE",
				}},
			}
		case "plugin marketplace list --json":
			value = map[string]any{"marketplaces": []any{map[string]any{"name": "personal", "root": filepath.Dir(root)}}}
		}
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		return json.Unmarshal(data, target)
	}
}

func TestCatalogNormalizesManifestAndComponents(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".codex-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "skills", "review"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		filepath.Join(root, ".codex-plugin", "plugin.json"): `{"name":"review-kit","version":"1.2.3","description":"Fallback","author":{"name":"EveryAPI"},"homepage":"https://example.com/docs","repository":{"url":"https://example.com/source"},"skills":"./skills","mcpServers":{"review":{"type":"http","url":"https://example.com/mcp"}},"apps":"./.app.json","interface":{"displayName":"Review Kit","shortDescription":"Review pull requests","longDescription":"A complete review workflow.","developerName":"EveryAPI Labs","category":"Developer Tools","capabilities":["Read","Write"],"defaultPrompt":["Review this pull request","Find regressions","Draft fixes","ignored"],"brandColor":"#3b82f6","websiteURL":"https://example.com"}}`,
		filepath.Join(root, "skills", "review", "SKILL.md"): "# Review\n",
		filepath.Join(root, ".app.json"):                    "{}\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var calls [][]string
	installInvoker(t, fixtureInvoker(t, root, &calls))
	output := captureOutput(t)
	if err := Run([]string{"catalog", "--json"}); err != nil {
		t.Fatal(err)
	}
	var catalog Catalog
	if err := json.Unmarshal(output.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Plugins) != 1 || len(catalog.Marketplaces) != 1 {
		t.Fatalf("catalog = %#v", catalog)
	}
	plugin := catalog.Plugins[0]
	if plugin.DisplayName != "Review Kit" || plugin.SkillCount != 1 || !plugin.HasMCPServer || !plugin.HasApp {
		t.Fatalf("plugin = %#v", plugin)
	}
	if !reflect.DeepEqual(plugin.DefaultPrompts, []string{"Review this pull request", "Find regressions", "Draft fixes"}) {
		t.Fatalf("default prompts = %#v", plugin.DefaultPrompts)
	}
	if plugin.BrandColor == nil || *plugin.BrandColor != "#3B82F6" {
		t.Fatalf("brand color = %#v", plugin.BrandColor)
	}
	wantCalls := [][]string{{"plugin", "list", "--available", "--json"}, {"plugin", "marketplace", "list", "--json"}}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %#v, want %#v", calls, wantCalls)
	}
}

func TestInstallReconcilesCatalogBeforeMutationAndReturnsFreshCatalog(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".codex-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".codex-plugin", "plugin.json"), []byte(`{"name":"review-kit","version":"1.0.0","description":"Review","author":{"name":"EveryAPI"},"interface":{"displayName":"Review Kit","shortDescription":"Review","longDescription":"Review","developerName":"EveryAPI","category":"Developer Tools","capabilities":[],"websiteURL":"https://example.com","privacyPolicyURL":"https://example.com/privacy","termsOfServiceURL":"https://example.com/terms","defaultPrompt":[],"brandColor":"#123456"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	installInvoker(t, fixtureInvoker(t, root, &calls))
	output := captureOutput(t)
	if err := Run([]string{"install", "review-kit@personal", "--json"}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output.Bytes(), []byte(`"plugin_id":"review-kit@personal"`)) {
		t.Fatalf("output = %s", output.String())
	}
	wantMutation := []string{"plugin", "add", "--json", "review-kit@personal"}
	found := false
	for _, call := range calls {
		if reflect.DeepEqual(call, wantMutation) {
			found = true
		}
	}
	if !found {
		t.Fatalf("mutation call missing from %#v", calls)
	}
}

func TestRejectsUntrustedSelectorsBeforeSpawningCodex(t *testing.T) {
	called := false
	installInvoker(t, func(context.Context, []string, time.Duration, any) error {
		called = true
		return nil
	})
	for _, args := range [][]string{
		{"install", "--help@personal"},
		{"remove", "plugin@market/place"},
		{"marketplace", "add", "--bad"},
		{"marketplace", "remove", "../personal"},
	} {
		if err := Run(args); err == nil {
			t.Errorf("Run(%#v) succeeded", args)
		}
	}
	if called {
		t.Fatal("invalid input reached Codex")
	}
}

func TestBoundedBufferRejectsOversizedOutput(t *testing.T) {
	buffer := &boundedBuffer{limit: 4}
	written, err := buffer.Write([]byte("abcdef"))
	if err == nil || written != 4 || !buffer.exceeded || buffer.buffer.String() != "abcd" {
		t.Fatalf("write = (%d, %v), buffer = %#v", written, err, buffer)
	}
}
