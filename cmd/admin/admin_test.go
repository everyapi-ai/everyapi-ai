package admin

import (
	"strings"
	"testing"
)

func TestRunHelp(t *testing.T) {
	for _, arg := range []string{"help", "--help", "-h"} {
		if err := Run([]string{arg}); err != nil {
			t.Errorf("Run(%q) returned error: %v", arg, err)
		}
	}
	for _, want := range []string{"marketplace", "status", "on", "off"} {
		if !strings.Contains(adminUsage, want) {
			t.Errorf("adminUsage missing %q", want)
		}
	}
}

func TestRunUnknownSubcommand(t *testing.T) {
	if err := Run([]string{"flobnar"}); err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
}

func TestRunNoArgs(t *testing.T) {
	if err := Run(nil); err == nil {
		t.Fatal("expected error for empty args")
	}
}

func TestMarketplaceNoSub(t *testing.T) {
	if err := adminMarketplace(nil); err == nil {
		t.Fatal("expected usage error from 'admin marketplace' with no sub")
	}
}

func TestMarketplaceUnknownSub(t *testing.T) {
	if err := adminMarketplace([]string{"xxx"}); err == nil {
		t.Fatal("expected error for unknown marketplace sub")
	}
}

func TestPrevOrUnset(t *testing.T) {
	if got := prevOrUnset(""); got != "<unset>" {
		t.Errorf("prevOrUnset(\"\") = %q, want <unset>", got)
	}
	if got := prevOrUnset("false"); got != "false" {
		t.Errorf("prevOrUnset(\"false\") = %q, want false", got)
	}
}
