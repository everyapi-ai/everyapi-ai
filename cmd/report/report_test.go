package report

import (
	"testing"

	"github.com/everyapi-ai/everyapi-sdk/config"
)

func TestReportClientConfig_UsesGatewayRegionWithoutCredentials(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	if err := config.SaveSettings(&config.Settings{GatewayRegion: "cn"}); err != nil {
		t.Fatal(err)
	}

	apiBase, token, userID, err := reportClientConfig()
	if err != nil {
		t.Fatalf("reportClientConfig: %v", err)
	}
	if apiBase != config.ChinaAPIBase {
		t.Errorf("apiBase = %q, want %q", apiBase, config.ChinaAPIBase)
	}
	if token != "" || userID != 0 {
		t.Errorf("token/userID = %q/%d, want empty/0", token, userID)
	}
}

func TestReportClientConfig_PreservesCustomCredentialGateway(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	if err := config.SaveSettings(&config.Settings{GatewayRegion: "cn"}); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(&config.Credentials{
		APIBase:     "https://selfhost.example",
		AccessToken: "tok",
		UserID:      42,
	}); err != nil {
		t.Fatal(err)
	}

	apiBase, token, userID, err := reportClientConfig()
	if err != nil {
		t.Fatalf("reportClientConfig: %v", err)
	}
	if apiBase != "https://selfhost.example" {
		t.Errorf("apiBase = %q, want custom", apiBase)
	}
	if token != "tok" || userID != 42 {
		t.Errorf("token/userID = %q/%d, want tok/42", token, userID)
	}
}
