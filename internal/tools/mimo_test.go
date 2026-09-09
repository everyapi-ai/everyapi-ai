package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMiMoConfigUsesOfficialSurface(t *testing.T) {
	tool, err := Lookup("mimo-code")
	if err != nil {
		t.Fatal(err)
	}
	if tool.ExecName != "mimo" {
		t.Fatalf("unexpected executable: %s", tool.ExecName)
	}
	models := []Model{
		{ID: "chat-model", SupportedEndpointTypes: []string{"openai"}},
		{ID: "response-model", SupportedEndpointTypes: []string{"openai-response"}},
	}
	env, err := tool.PrepareWithModels("https://example.com", "secret-fixture", models, "response-model")
	if err != nil {
		t.Fatal(err)
	}
	var config openCodeConfig
	if err := json.Unmarshal([]byte(env["MIMOCODE_CONFIG_CONTENT"]), &config); err != nil {
		t.Fatal(err)
	}
	if config.Schema != "https://mimo.xiaomi.com/mimocode/config.json" || config.Model != "everyapi-responses/response-model" {
		t.Fatalf("unexpected config: %#v", config)
	}
	if config.Provider["everyapi"].Options.BaseURL != "https://example.com/v1" ||
		config.Provider["everyapi-responses"].NPM != "@ai-sdk/openai" {
		t.Fatalf("unexpected providers: %#v", config.Provider)
	}
	if strings.Contains(env["MIMOCODE_CONFIG_CONTENT"], "secret-fixture") || env["OPENCODE_CONFIG_CONTENT"] != "" {
		t.Fatal("config leaked credentials or used the wrong runtime")
	}
	if env["MIMOCODE_DISABLE_PROJECT_CONFIG"] != "true" {
		t.Fatal("project override guard missing")
	}
	if tool.Env("https://example.com", "secret-fixture")[openCodeCredentialEnv] != "secret-fixture" {
		t.Fatal("credential missing from child environment")
	}
}
