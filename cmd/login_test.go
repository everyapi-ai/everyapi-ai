package cmd

import (
	"errors"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliprompt"
	"github.com/everyapi-ai/everyapi-sdk/config"
)

func TestEnsureGatewayRegionPreference_PromptsAndPersistsChoice(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	var prompted bool
	err := ensureGatewayRegionPreference(
		func() bool { return true },
		func(prompt string, items []string, initial int) (int, error) {
			prompted = true
			if initial != 0 {
				t.Errorf("initial = %d, want 0", initial)
			}
			if len(items) != 2 {
				t.Fatalf("items len = %d, want 2", len(items))
			}
			return 1, nil
		},
	)
	if err != nil {
		t.Fatalf("ensureGatewayRegionPreference: %v", err)
	}
	if !prompted {
		t.Fatal("picker was not called")
	}
	s, err := config.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.GatewayRegion != "cn" {
		t.Errorf("GatewayRegion = %q, want cn", s.GatewayRegion)
	}
}

func TestEnsureGatewayRegionPreference_SkipsWhenAlreadyConfigured(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	if err := config.SaveSettings(&config.Settings{GatewayRegion: "global"}); err != nil {
		t.Fatal(err)
	}

	err := ensureGatewayRegionPreference(
		func() bool { return true },
		func(string, []string, int) (int, error) {
			t.Fatal("picker should not be called")
			return 0, nil
		},
	)
	if err != nil {
		t.Fatalf("ensureGatewayRegionPreference: %v", err)
	}
}

func TestEnsureGatewayRegionPreference_SkipsWhenNonInteractive(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	err := ensureGatewayRegionPreference(
		func() bool { return false },
		func(string, []string, int) (int, error) {
			t.Fatal("picker should not be called")
			return 0, nil
		},
	)
	if err != nil {
		t.Fatalf("ensureGatewayRegionPreference: %v", err)
	}
	s, err := config.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.GatewayRegion != "" {
		t.Errorf("GatewayRegion = %q, want empty", s.GatewayRegion)
	}
}

func TestResolveLoginAPIBase_ExplicitOverrideSkipsGatewayPrompt(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	got, err := resolveLoginAPIBase("http://localhost:8787/")
	if err != nil {
		t.Fatalf("resolveLoginAPIBase: %v", err)
	}
	if got != "http://localhost:8787" {
		t.Errorf("resolveLoginAPIBase = %q, want override", got)
	}
	s, err := config.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.GatewayRegion != "" {
		t.Errorf("GatewayRegion = %q, want empty", s.GatewayRegion)
	}
}

func TestEnsureGatewayRegionPreference_PropagatesCancel(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	err := ensureGatewayRegionPreference(
		func() bool { return true },
		func(string, []string, int) (int, error) {
			return -1, cliprompt.ErrPickCancelled
		},
	)
	if !errors.Is(err, cliprompt.ErrPickCancelled) {
		t.Fatalf("err = %v, want ErrPickCancelled", err)
	}
}
