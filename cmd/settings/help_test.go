package settings

import "testing"

func TestLeafHelpBeforePositionalParsing(t *testing.T) {
	for _, sub := range []string{"list", "get", "set", "reset"} {
		for _, help := range []string{"help", "--help", "-h"} {
			if err := Run([]string{sub, help}); err != nil {
				t.Errorf("settings %s %s: %v", sub, help, err)
			}
		}
	}
}
