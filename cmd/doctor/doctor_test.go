package doctor

import "testing"

func TestRunRejectsExtraArgumentsBeforeDiagnostics(t *testing.T) {
	if err := Run([]string{"extra"}); err == nil {
		t.Fatal("doctor accepted an extra argument")
	}
}

func TestRunHelpBeforeArgumentValidation(t *testing.T) {
	for _, help := range []string{"help", "--help", "-h"} {
		if err := Run([]string{help}); err != nil {
			t.Fatalf("Run(%q) returned error: %v", help, err)
		}
	}
}
