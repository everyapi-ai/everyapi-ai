package dm

import "testing"

func TestLeafHelpBeforeAuthOrPositionalParsing(t *testing.T) {
	for _, sub := range []string{"threads", "contacts", "count", "open", "messages", "send", "read"} {
		for _, help := range []string{"help", "--help", "-h"} {
			if err := Run([]string{sub, help}); err != nil {
				t.Errorf("dm %s %s: %v", sub, help, err)
			}
		}
	}
}
