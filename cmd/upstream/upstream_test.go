package upstream

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
	"github.com/everyapi-ai/everyapi-sdk/api"
)

// render is locale-sensitive through indicatorLabel; pin English so the label assertions are deterministic regardless of the dev's $LANG.
func init() { i18n.SetLanguage("en") }

// TestRenderCollapsesHealthyProviders is the core of the change: a green provider becomes a single aligned line and its operational sub-components (which the backend still sends) are dropped as noise.
func TestRenderCollapsesHealthyProviders(t *testing.T) {
	out := render([]api.UpstreamProvider{{
		Name:        "OpenAI",
		Indicator:   "none",
		Description: "All Systems Operational",
		Components: []api.UpstreamComponent{
			{Name: "Agent", Status: "operational"},
			{Name: "Batch", Status: "operational"},
		},
	}})

	if got := strings.Count(out, "\n"); got != 1 {
		t.Fatalf("healthy provider should render exactly one line, got %d:\n%s", got, out)
	}
	if !strings.Contains(out, "[ ok ]  OpenAI  operational") {
		t.Errorf("unexpected rollup line:\n%q", out)
	}
	for _, noise := range []string{"Agent", "Batch", "All Systems Operational"} {
		if strings.Contains(out, noise) {
			t.Errorf("healthy line must not carry %q:\n%s", noise, out)
		}
	}
}

// TestRenderExpandsDegradedProvider checks that a non-green provider shows its summary, ONLY the broken components (operational ones still filtered out), and incidents — with snake_case enums humanized.
func TestRenderExpandsDegradedProvider(t *testing.T) {
	out := render([]api.UpstreamProvider{{
		Name:        "xAI",
		Indicator:   "minor",
		Description: "Partial Outage",
		Components: []api.UpstreamComponent{
			{Name: "Chat", Status: "operational"},
			{Name: "Images", Status: "degraded_performance"},
		},
		Incidents: []api.UpstreamIncident{
			{Name: "Elevated errors", Status: "investigating", Impact: "minor"},
		},
	}})

	want := []string{
		"[warn]  xAI  minor issues",
		"        Partial Outage",
		"        - Images: degraded performance", // humanized, indented
		"        ! Elevated errors (investigating / minor)",
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("missing line %q in:\n%s", w, out)
		}
	}
	if strings.Contains(out, "Chat") {
		t.Errorf("operational component must be filtered out:\n%s", out)
	}
}

// TestRenderAlignsLabelColumn verifies names of different display widths (including CJK, where fmt's rune-counting %-Ns drifts) line their labels up at the same column.
func TestRenderAlignsLabelColumn(t *testing.T) {
	out := render([]api.UpstreamProvider{
		{Name: "xAI", Indicator: "none"},
		{Name: "Google AI / Vertex", Indicator: "none"},
		{Name: "深度求索", Indicator: "none"}, // 4 CJK runes = 8 cells
	})

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %d:\n%s", len(lines), out)
	}
	// Compare the label's start in display CELLS, not bytes: a CJK name has more bytes than cells, so byte offsets would differ even when the columns line up visually.
	col := -1
	for _, ln := range lines {
		i := strings.Index(ln, "operational")
		if i < 0 {
			t.Fatalf("line missing label: %q", ln)
		}
		c := lipgloss.Width(ln[:i])
		if col == -1 {
			col = c
		} else if c != col {
			t.Errorf("label column drift: %q starts label at cell %d, want %d", ln, c, col)
		}
	}
}
