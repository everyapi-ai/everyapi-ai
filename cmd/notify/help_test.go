package notify

import "testing"

func TestLeafHelpBeforeAuthOrPositionalParsing(t *testing.T) {
	for _, sub := range []string{"list", "count", "read", "readall"} {
		for _, help := range []string{"help", "--help", "-h"} {
			if err := Run([]string{sub, help}); err != nil {
				t.Errorf("notify %s %s: %v", sub, help, err)
			}
		}
	}
}
