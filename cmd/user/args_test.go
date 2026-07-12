package user

import (
	"testing"

	"github.com/everyapi-ai/everyapi-sdk/api"
)

func TestDestructivePositionalsAreExact(t *testing.T) {
	if err := runOAuthUnbind([]string{"1", "extra", "-y"}); err == nil {
		t.Fatal("oauth unbind accepted extra")
	}
	if err := runAff([]string{"reset", "extra"}); err == nil {
		t.Fatal("affiliate reset accepted extra")
	}
	if err := runAffTransfer(&api.Client{}, []string{"1", "extra", "-y"}); err == nil {
		t.Fatal("affiliate transfer accepted extra")
	}
	if err := runAff([]string{"transfer", "1", "extra", "-y"}); err == nil {
		t.Fatal("affiliate dispatcher accepted extra before authentication")
	}
	if err := runAff([]string{"transfer", "not-an-amount", "-y"}); err == nil {
		t.Fatal("affiliate dispatcher deferred invalid amount until after authentication")
	}
}

func TestFlagOnlyCommandsRejectPositionalsBeforeSideEffects(t *testing.T) {
	tests := map[string]func([]string) error{
		"info":         runInfo,
		"2fa status":   runTwoFAStatus,
		"2fa disable":  runTwoFADisable,
		"2fa backup":   runTwoFABackup,
		"2fa enable":   runTwoFAEnable,
		"passkey":      runPasskey,
		"oauth list":   runOAuthList,
		"update":       runUpdate,
		"passwd":       runPasswd,
		"setting":      runSetting,
		"setting test": runSettingTest,
	}
	for name, run := range tests {
		t.Run(name, func(t *testing.T) {
			if err := run([]string{"extra"}); err == nil {
				t.Fatal("accepted extra positional")
			}
		})
	}
}

func TestLeafHelpBeforeValidationAndAuthentication(t *testing.T) {
	for name, run := range map[string]func([]string) error{
		"oauth unbind": runOAuthUnbind,
		"affiliate":    runAff,
	} {
		t.Run(name, func(t *testing.T) {
			if err := run([]string{"--help"}); err != nil {
				t.Fatalf("--help returned error: %v", err)
			}
		})
	}
}
