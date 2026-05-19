package menubar

import "testing"

// captureNotifier replaces the package-level notify function for the
// duration of a test, recording every dispatched (title, body) pair.
// The previous dispatcher is restored on test cleanup so siblings
// don't see leaked stubs.
func captureNotifier(t *testing.T) *[]notification {
	t.Helper()
	var captured []notification
	prev := notify
	notify = func(title, body string) {
		captured = append(captured, notification{title: title, body: body})
	}
	t.Cleanup(func() { notify = prev })
	return &captured
}

type notification struct{ title, body string }

func TestMaybeNotifySellerEarnings(t *testing.T) {
	tests := []struct {
		name           string
		initialLast    int
		current        int
		perUnit        float64
		wantNotify     bool
		wantTitleHint  string // substring expected in title; empty = skip check
		wantNewLast    int
	}{
		{
			name:        "first observation suppresses",
			initialLast: -1,
			current:     2_500_000,
			perUnit:     500_000,
			wantNotify:  false,
			wantNewLast: 2_500_000,
		},
		{
			name:        "no change",
			initialLast: 1_000_000,
			current:     1_000_000,
			perUnit:     500_000,
			wantNotify:  false,
			wantNewLast: 1_000_000,
		},
		{
			name:          "increase fires notification",
			initialLast:   1_000_000,
			current:       1_500_000,
			perUnit:       500_000,
			wantNotify:    true,
			wantTitleHint: "$1.00",
			wantNewLast:   1_500_000,
		},
		{
			name:        "decrease (refund / withdrawal) does not notify",
			initialLast: 1_000_000,
			current:     500_000,
			perUnit:     500_000,
			wantNotify:  false,
			wantNewLast: 500_000,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &Controller{lastSellerQuota: tc.initialLast}
			got := captureNotifier(t)
			c.maybeNotifySellerEarnings(tc.current, tc.perUnit)

			if tc.wantNotify {
				if len(*got) != 1 {
					t.Fatalf("expected 1 notification, got %d", len(*got))
				}
				if tc.wantTitleHint != "" && !contains((*got)[0].title, tc.wantTitleHint) {
					t.Errorf("title %q missing hint %q", (*got)[0].title, tc.wantTitleHint)
				}
			} else if len(*got) != 0 {
				t.Errorf("expected no notification, got %d: %+v", len(*got), *got)
			}
			if c.lastSellerQuota != tc.wantNewLast {
				t.Errorf("lastSellerQuota = %d, want %d", c.lastSellerQuota, tc.wantNewLast)
			}
		})
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
