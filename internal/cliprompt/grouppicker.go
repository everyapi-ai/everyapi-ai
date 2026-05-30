package cliprompt

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// MenuGroup is one category in the grouped launcher: a Title header and
// the pre-formatted command rows under it (each Label is already the
// aligned "name  description" string the flat picker used).
type MenuGroup struct {
	Title  string
	Labels []string
}

// PickGrouped renders every group on one screen under dim, unselectable
// category headers and lets the user arrow through the commands (the
// cursor skips headers). It returns the FLAT index of the chosen
// command — i.e. its position across all groups' Labels concatenated in
// order — so callers can index a parallel command slice built the same
// way. ErrPickCancelled is returned on Esc / Ctrl-C.
//
// initial is the flat index to start the cursor on (clamped). This is
// the single-screen counterpart to PickWithSelected; the nested layout
// reuses PickWithSelected directly instead.
func PickGrouped(title string, groups []MenuGroup, initial int) (int, error) {
	// Non-TTY (CI / piped): the bubbletea model can't drive a real
	// terminal, so degrade to the numbered prompt over the flattened
	// label list — same fallback contract as PickWithSelected. The
	// returned index is the flat command index, matching the TTY path.
	if !isInteractive() {
		var flat []string
		for _, g := range groups {
			flat = append(flat, g.Labels...)
		}
		return pickByNumber(title, flat)
	}
	m := newGroupModel(title, groups, initial)
	res, err := tea.NewProgram(m).Run()
	if err != nil {
		return -1, err
	}
	final := res.(groupModel)
	if final.canceled {
		return -1, ErrPickCancelled
	}
	return final.chosen, nil
}

// gpRow is one rendered line: either a group header (selectable=false)
// or a command row carrying its flat item index.
type gpRow struct {
	header  bool
	text    string
	itemIdx int
}

type groupModel struct {
	title string
	rows  []gpRow
	// selectable holds the indices into rows that are command rows, in
	// order; cur is an index into selectable (the highlighted command).
	selectable []int
	cur        int
	height     int // terminal height (0 until first WindowSizeMsg)
	chosen     int // flat item index picked
	canceled   bool
	// done is set the moment we quit. View() then renders nothing, so
	// bubbletea's final inline frame ERASES the menu instead of leaving
	// it on screen — otherwise the launcher menu would stay visible
	// stacked above whatever the chosen command renders next (e.g. a
	// sub-picker).
	done bool
}

func newGroupModel(title string, groups []MenuGroup, initial int) groupModel {
	m := groupModel{title: title}
	flat := 0
	for _, g := range groups {
		if len(g.Labels) == 0 {
			continue
		}
		m.rows = append(m.rows, gpRow{header: true, text: g.Title})
		for _, lbl := range g.Labels {
			m.rows = append(m.rows, gpRow{text: lbl, itemIdx: flat})
			m.selectable = append(m.selectable, len(m.rows)-1)
			flat++
		}
	}
	if initial >= 0 && initial < len(m.selectable) {
		m.cur = initial
	}
	return m
}

func (m groupModel) Init() tea.Cmd { return nil }

func (m groupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc", "q":
			m.canceled = true
			m.done = true
			return m, tea.Quit
		case "up", "k":
			if m.cur > 0 {
				m.cur--
			}
		case "down", "j":
			if m.cur < len(m.selectable)-1 {
				m.cur++
			}
		case "home", "g":
			m.cur = 0
		case "end", "G":
			m.cur = len(m.selectable) - 1
		case "enter":
			if len(m.selectable) > 0 {
				m.chosen = m.rows[m.selectable[m.cur]].itemIdx
			}
			m.done = true
			return m, tea.Quit
		}
	}
	return m, nil
}

// gpTheme reuses huh's default theme (the same one every other cliprompt
// picker renders with) so the grouped launcher looks identical to the
// rest of the CLI instead of inventing its own palette. Colors resolve
// against the terminal at render time. gpHintStyle is the one extra
// style — a faint footer, since huh's own help line is suppressed here.
var (
	gpTheme     = huh.ThemeCharm()
	gpHintStyle = lipgloss.NewStyle().Faint(true)
)

func (m groupModel) View() string {
	if m.done {
		// Final frame after quit: render nothing so bubbletea erases the
		// menu instead of leaving it stacked above the next screen.
		return ""
	}
	var b strings.Builder
	if m.title != "" {
		b.WriteString(gpTheme.Focused.Title.Render(m.title))
		b.WriteString("\n\n")
	}

	// Reserve rows for the title (2 lines) and the help footer (2
	// lines); the rest is the scroll window. Fall back to showing
	// everything when we don't know the height yet.
	window := len(m.rows)
	if m.height > 0 {
		window = m.height - 4
		if window < 3 {
			window = 3
		}
	}

	curRow := -1
	if len(m.selectable) > 0 {
		curRow = m.selectable[m.cur]
	}
	anchor := curRow
	if anchor < 0 {
		anchor = 0
	}
	top := m.scrollTop(anchor, window)

	end := top + window
	if end > len(m.rows) {
		end = len(m.rows)
	}
	for i := top; i < end; i++ {
		r := m.rows[i]
		switch {
		case r.header:
			// Category title, styled like a huh group title (flush-left).
			b.WriteString(gpTheme.Group.Title.Render(r.text))
		case i == curRow:
			// huh's selector ("> ", themed) + the selected-option color,
			// matching every other picker. The selector is 2 cols wide,
			// so it lines up with the non-cursor "  " indent.
			b.WriteString(gpTheme.Focused.SelectSelector.String() + gpTheme.Focused.SelectedOption.Render(r.text))
		default:
			b.WriteString("  " + gpTheme.Focused.UnselectedOption.Render(r.text))
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(gpHintStyle.Render(menuNavHint()))
	b.WriteString("\n")
	return b.String()
}

// scrollTop returns the first rows[] line to display so curRow stays
// visible within a window of the given size. Stateless — derived purely
// from the cursor each render, so no scroll offset is persisted. The
// cursor sits at the window's bottom edge once the list scrolls; the
// command's own category header naturally stays in view above it
// (window is always >= 3, so the row directly above the cursor shows).
func (m groupModel) scrollTop(curRow, window int) int {
	if window >= len(m.rows) || curRow < window {
		return 0
	}
	top := curRow - window + 1
	if hi := len(m.rows) - window; top > hi {
		top = hi
	}
	return top
}

// menuNavHint is overridable so the launcher can localize the footer.
var menuNavHint = func() string { return "↑/↓ select · enter confirm · esc back" }

// SetMenuNavHint lets the caller install a localized nav-hint footer
// for PickGrouped. Pass the already-translated string.
func SetMenuNavHint(s string) { menuNavHint = func() string { return s } }
