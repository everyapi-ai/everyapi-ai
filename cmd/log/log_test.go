package log

import (
	"strings"
	"testing"
	"time"

	"github.com/everyapi-ai/everyapi-sdk/api"
)

func TestParseWindow(t *testing.T) {
	now := time.Unix(2_000_000_000, 0) // anchor so the test is deterministic
	cases := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{"", 0, false},
		{"168h", now.Add(-168 * time.Hour).Unix(), false},
		{"24h", now.Add(-24 * time.Hour).Unix(), false},
		{"30m", now.Add(-30 * time.Minute).Unix(), false},
		{"1700000000", 1700000000, false},
		{"banana", 0, true},
		// "d" isn't a Go duration suffix, but parseWindow recognises it explicitly because the help text advertises it.
		{"7d", now.Add(-7 * 24 * time.Hour).Unix(), false},
		{"30d", now.Add(-30 * 24 * time.Hour).Unix(), false},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := parseWindow(c.in, now)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, c.wantErr)
			}
			if !c.wantErr && got != c.want {
				t.Errorf("got %d, want %d", got, c.want)
			}
		})
	}
}

func TestWindowLabel(t *testing.T) {
	if got := windowLabel(0); got != "(open)" {
		t.Errorf("zero → %q, want (open)", got)
	}
	if got := windowLabel(1700000000); got == "(open)" || got == "" {
		t.Errorf("non-zero produced %q", got)
	}
}

// use_time is stored in whole seconds; the list used to print it with an "ms" suffix, so a 49-second request read as 49 milliseconds.
func TestFormatLogRow_UseTimeIsSeconds(t *testing.T) {
	row := api.LogEntry{ID: 1, CreatedAt: time.Now().Unix(), ModelName: "gemini-4-neon-low", TokenName: "k", Quota: 10, PromptTokens: 25860, CompletionTokens: 29, UseTime: 49, ChannelID: 416}
	got := formatLogRow(row)
	if !strings.Contains(got, ", 49s, ch=#416") {
		t.Fatalf("formatLogRow = %q, want the duration rendered as 49s", got)
	}
	if strings.Contains(got, "49ms") {
		t.Fatalf("formatLogRow = %q still labels seconds as milliseconds", got)
	}
}
