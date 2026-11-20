package tools

import (
	"slices"
	"testing"
)

func TestAntigravityEntryLaunchesNativeCLI(t *testing.T) {
	tool, err := Lookup("antigravity")
	if err != nil {
		t.Fatal(err)
	}
	if tool.ExecName != "agy" {
		t.Fatalf("ExecName = %q, want agy", tool.ExecName)
	}
	if tool.YoloFlag != "--dangerously-skip-permissions" {
		t.Errorf("YoloFlag = %q", tool.YoloFlag)
	}
	if !tool.Native {
		t.Fatal("agy must launch natively without resolving an EveryAPI relay key")
	}
	if tool.SupportsTransparent() {
		t.Fatal("native agy must not receive the EveryAPI transparent connector")
	}
	if env := tool.Env("https://api.everyapi.ai", "secret-relay-key"); len(env) != 0 {
		t.Fatalf("native agy must not receive gateway credentials: %#v", env)
	}
}

// `everyapi use gemini` is the Antigravity launcher under its familiar name, not Google's separate Gemini CLI. #845 made it so and a later registry rewrite split the two again, which sent `use gemini` to Gemini CLI's own sign-in dialog.
func TestGeminiIsAliasOfAntigravity(t *testing.T) {
	for _, name := range []string{"gemini", "Gemini", "GEMINI"} {
		tool, err := Lookup(name)
		if err != nil {
			t.Fatalf("Lookup(%q): %v", name, err)
		}
		if tool != Registry["antigravity"] {
			t.Fatalf("Lookup(%q) = %q (exec %q), want the antigravity entry", name, tool.Name, tool.ExecName)
		}
	}
	if _, registered := Registry["gemini"]; registered {
		t.Fatal(`Registry must not carry a separate "gemini" entry; it would shadow the alias`)
	}
	if slices.Contains(Names(), "gemini") {
		t.Fatal("Names must list antigravity once, not again under its alias")
	}
	for alias, target := range toolAliases {
		if Registry[target] == nil {
			t.Errorf("alias %q points at unregistered tool %q", alias, target)
		}
	}
}
