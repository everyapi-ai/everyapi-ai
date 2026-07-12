package models

import (
	"testing"
)

func TestRunWithNoArgsDoesNotPanic(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Run(nil) panicked: %v", r)
		}
	}()
	if err := Run(nil); err == nil {
		t.Fatal("Run(nil) unexpectedly succeeded without credentials")
	}
}
