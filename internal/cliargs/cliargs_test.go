package cliargs

import "testing"

func TestIsHelp(t *testing.T) {
	for _, token := range []string{"help", "--help", "-h"} {
		if !IsHelp([]string{token}) {
			t.Fatalf("IsHelp(%q) = false", token)
		}
	}
	for _, args := range [][]string{nil, {}, {"1"}, {"--", "--help"}} {
		if IsHelp(args) {
			t.Fatalf("IsHelp(%q) = true", args)
		}
	}
}
