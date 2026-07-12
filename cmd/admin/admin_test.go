package admin

import (
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
)

func TestRunHelp(t *testing.T) {
	for _, arg := range []string{"help", "--help", "-h"} {
		if err := Run([]string{arg}); err != nil {
			t.Errorf("Run(%q) returned error: %v", arg, err)
		}
	}
	// The usage text moved to the admin.help.usage locale key; the
	// command-syntax column stays English in every locale, so these
	// tokens are present regardless of language.
	usage := i18n.T("admin.help.usage")
	for _, want := range []string{"marketplace", "status", "on", "off"} {
		if !strings.Contains(usage, want) {
			t.Errorf("admin.help.usage missing %q", want)
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

func TestAdminContainerHelp(t *testing.T) {
	tests := map[string]func([]string) error{
		"user":       adminUser,
		"channel":    adminChannel,
		"abuse":      adminAbuse,
		"redemption": adminRedemption,
	}
	for name, run := range tests {
		t.Run(name, func(t *testing.T) {
			if err := run([]string{"--help"}); err != nil {
				t.Fatalf("admin %s --help returned error: %v", name, err)
			}
		})
	}
}
