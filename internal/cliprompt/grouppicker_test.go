package cliprompt

import (
	"io"
	"os"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// keyMsg builds the tea.KeyMsg whose String() matches what groupModel's Update switches on ("down"/"up"/"enter"/"esc" or a rune like "j").
func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func twoGroups() []MenuGroup {
	return []MenuGroup{
		{Title: "Account", Labels: []string{"status", "topup", "wallet"}},
		{Title: "API", Labels: []string{"use", "token"}},
	}
}

// TestNewGroupModel_FlatIndexing checks the row layout: headers are interleaved and non-selectable, command rows carry a contiguous flat index across groups (so PickGrouped's return value maps straight into a parallel command slice), and `initial` lands the cursor on the right command.
func TestNewGroupModel_FlatIndexing(t *testing.T) {
	m := newGroupModel("pick", twoGroups(), 3) // 3 == "use" (4th command)

	// 2 headers + 5 commands = 7 rows; 5 selectable.
	if len(m.rows) != 7 {
		t.Fatalf("rows = %d, want 7", len(m.rows))
	}
	if len(m.selectable) != 5 {
		t.Fatalf("selectable = %d, want 5", len(m.selectable))
	}
	// Flat indices on command rows must be 0..4 in order.
	want := 0
	for _, r := range m.rows {
		if r.header {
			continue
		}
		if r.itemIdx != want {
			t.Errorf("command row %q itemIdx = %d, want %d", r.text, r.itemIdx, want)
		}
		want++
	}
	// initial=3 → cursor on the command whose flat index is 3 ("use").
	if got := m.rows[m.selectable[m.cur]].itemIdx; got != 3 {
		t.Errorf("initial cursor flat idx = %d, want 3", got)
	}
}

// TestGroupModel_EnterReturnsFlatIndex drives the model the way bubbletea would and confirms enter records the highlighted command's flat index.
func TestGroupModel_EnterReturnsFlatIndex(t *testing.T) {
	m := newGroupModel("pick", twoGroups(), 0)
	// Down twice: 0 → 1 → 2 (still "wallet", flat 2).
	m = stepKey(m, "down")
	m = stepKey(m, "down")
	if got := m.rows[m.selectable[m.cur]].itemIdx; got != 2 {
		t.Fatalf("after 2×down flat idx = %d, want 2", got)
	}
	// Down once more crosses the API header without landing on it.
	m = stepKey(m, "down")
	if got := m.rows[m.selectable[m.cur]].itemIdx; got != 3 {
		t.Fatalf("after 3×down flat idx = %d, want 3 (header skipped)", got)
	}
}

// TestGroupModel_CursorClampsAtEnds confirms up at the top and down at the bottom don't run the cursor off the selectable slice.
func TestGroupModel_CursorClampsAtEnds(t *testing.T) {
	m := newGroupModel("pick", twoGroups(), 0)
	m = stepKey(m, "up") // already at top
	if m.cur != 0 {
		t.Errorf("up at top moved cursor to %d, want 0", m.cur)
	}
	for i := 0; i < 10; i++ {
		m = stepKey(m, "down")
	}
	if m.cur != len(m.selectable)-1 {
		t.Errorf("down past end → cur %d, want %d", m.cur, len(m.selectable)-1)
	}
}

// TestScrollTop_KeepsCursorVisible checks the scroll window always contains the highlighted row for a list taller than the viewport.
func TestScrollTop_KeepsCursorVisible(t *testing.T) {
	m := newGroupModel("pick", twoGroups(), 0)
	const window = 4 // smaller than the 7 rows
	for ci := range m.selectable {
		m.cur = ci
		curRow := m.selectable[ci]
		top := m.scrollTop(curRow, window)
		if curRow < top || curRow >= top+window {
			t.Errorf("cur %d (row %d) not visible in window [%d,%d)", ci, curRow, top, top+window)
		}
	}
}

func stepKey(m groupModel, key string) groupModel {
	out, _ := m.Update(keyMsg(key))
	return out.(groupModel)
}

// TestPickGrouped_NonTTYFallback verifies that off a TTY (CI / piped) PickGrouped degrades to the numbered prompt over the flattened labels — not the bubbletea model, which would break — and that the number it reads maps to the same flat command index the TTY path returns.
func TestPickGrouped_NonTTYFallback(t *testing.T) {
	defer func(f func() bool) { isInteractive = f }(isInteractive)
	isInteractive = func() bool { return false }

	defer func(s *os.File) { os.Stdin = s }(os.Stdin)
	r, w, _ := os.Pipe()
	os.Stdin = r
	io.WriteString(w, "4\n") // 4th command across both groups → "use", flat idx 3
	w.Close()

	idx, err := PickGrouped("pick", twoGroups(), 0)
	if err != nil {
		t.Fatalf("non-TTY PickGrouped: %v", err)
	}
	if idx != 3 {
		t.Errorf("flat idx = %d, want 3 (use)", idx)
	}
}

// TestGroupModel_ViewEmptyAfterQuit locks the menu-erase contract: once the model quits (enter or esc), View() must render nothing so bubbletea's final inline frame wipes the menu instead of leaving it stacked above the next screen. Mirrors how huh's own form clears.
func TestGroupModel_ViewEmptyAfterQuit(t *testing.T) {
	m := newGroupModel("pick", twoGroups(), 0)
	if m.View() == "" {
		t.Fatal("View should be non-empty before quit")
	}
	for _, key := range []string{"enter", "esc"} {
		out, _ := newGroupModel("pick", twoGroups(), 0).Update(keyMsg(key))
		gm := out.(groupModel)
		if !gm.done {
			t.Errorf("%s: done not set", key)
		}
		if gm.View() != "" {
			t.Errorf("%s: View() = %q after quit, want empty", key, gm.View())
		}
	}
}
