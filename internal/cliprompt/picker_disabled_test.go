package cliprompt

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/v3/internal/i18n"
)

func TestPickWithDisabledRejectsUnavailableRowsInNumberedFallback(t *testing.T) {
	defer func(previous func() bool) { isInteractive = previous }(isInteractive)
	isInteractive = func() bool { return false }
	defer func(previous *os.File) { os.Stdin = previous }(os.Stdin)

	selectWithInput := func(input string) (int, error) {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		os.Stdin = r
		if _, err := io.WriteString(w, input); err != nil {
			t.Fatal(err)
		}
		_ = w.Close()
		return PickWithDisabled(
			"pick",
			[]string{"claude-sonnet-5", "gpt-5.6-terra"},
			[]bool{true, false},
			1,
		)
	}

	if _, err := selectWithInput("1\n"); !errors.Is(err, ErrPickUnavailable) {
		t.Fatalf("disabled selection error = %v, want ErrPickUnavailable", err)
	}
	if index, err := selectWithInput("2\n"); err != nil || index != 1 {
		t.Fatalf("available selection = %d, %v; want 1, nil", index, err)
	}
}

func TestDisabledSelectFieldNavigationSkipsUnavailableRows(t *testing.T) {
	selected := 0
	field := newDisabledSelectField(
		huh.NewSelect[int]().
			Options(
				huh.NewOption("gpt-5.6-sol", 0),
				huh.NewOption("claude-haiku-4-5", 1),
				huh.NewOption("claude-sonnet-5", 2),
				huh.NewOption("gpt-5.6-terra", 3),
			).
			Value(&selected),
		[]bool{false, true, true, false},
	)

	model, _ := field.Update(tea.KeyMsg{Type: tea.KeyDown})
	field = model.(*disabledSelectField)
	if selected != 3 {
		t.Fatalf("down over consecutive disabled rows selected %d; want 3", selected)
	}

	model, _ = field.Update(tea.KeyMsg{Type: tea.KeyUp})
	field = model.(*disabledSelectField)
	if selected != 0 {
		t.Fatalf("up over disabled row selected %d; want 0", selected)
	}
}

func TestDisabledSelectFieldEndSkipsUnavailableLastRow(t *testing.T) {
	selected := 0
	field := newDisabledSelectField(
		huh.NewSelect[int]().
			Options(
				huh.NewOption("gpt-5.6-sol", 0),
				huh.NewOption("gpt-5.6-terra", 1),
				huh.NewOption("claude-haiku-4-5", 2),
			).
			Value(&selected),
		[]bool{false, false, true},
	)

	model, _ := field.Update(tea.KeyMsg{Type: tea.KeyEnd})
	field = model.(*disabledSelectField)
	hovered, ok := field.Hovered()
	if !ok || hovered != 1 {
		t.Fatalf("end over disabled last row hovered (%d, %t); want (1, true)", hovered, ok)
	}
}

func TestDisabledSelectFieldFilterNeverFocusesUnavailableOnlyMatch(t *testing.T) {
	selected := 0
	field := newDisabledSelectField(
		huh.NewSelect[int]().
			Options(
				huh.NewOption("gpt-5.6-sol", 0),
				huh.NewOption("claude-haiku-4-5", 1),
				huh.NewOption("gpt-5.6-terra", 2),
			).
			Value(&selected),
		[]bool{false, true, false},
	)

	model, _ := field.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	field = model.(*disabledSelectField)
	model, _ = field.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("claude")})
	field = model.(*disabledSelectField)

	hovered, ok := field.Hovered()
	if ok && hovered == 1 {
		t.Fatal("filter gave keyboard focus to the unavailable Claude row")
	}
}

func TestDisabledSelectFieldTreatsJKGAsTextWhileFiltering(t *testing.T) {
	selected := 1
	field := newDisabledSelectField(
		huh.NewSelect[int]().
			Options(
				huh.NewOption("kilo", 0),
				huh.NewOption("other", 1),
				huh.NewOption("k-claude", 2),
				huh.NewOption("kimi", 3),
			).
			Value(&selected),
		[]bool{false, false, true, false},
	)

	model, _ := field.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	field = model.(*disabledSelectField)
	model, _ = field.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	field = model.(*disabledSelectField)

	hovered, ok := field.Hovered()
	if !ok || hovered != 3 {
		t.Fatalf("filter text k hovered (%d, %t); want available filtered row (3, true)", hovered, ok)
	}
}

func TestDisabledSelectFieldAllNavigationKeysSkipUnavailableRows(t *testing.T) {
	tests := []struct {
		name     string
		initial  int
		key      tea.KeyMsg
		expected int
	}{
		{name: "up wraps", initial: 1, key: tea.KeyMsg{Type: tea.KeyUp}, expected: 5},
		{name: "down wraps", initial: 5, key: tea.KeyMsg{Type: tea.KeyDown}, expected: 1},
		{name: "home", initial: 5, key: tea.KeyMsg{Type: tea.KeyHome}, expected: 1},
		{name: "g", initial: 5, key: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")}, expected: 1},
		{name: "end", initial: 1, key: tea.KeyMsg{Type: tea.KeyEnd}, expected: 5},
		{name: "G", initial: 1, key: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")}, expected: 5},
		{name: "half page up", initial: 5, key: tea.KeyMsg{Type: tea.KeyCtrlU}, expected: 1},
		{name: "half page down", initial: 1, key: tea.KeyMsg{Type: tea.KeyCtrlD}, expected: 5},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			selected := test.initial
			field := newDisabledSelectField(
				huh.NewSelect[int]().
					Options(
						huh.NewOption("disabled-0", 0),
						huh.NewOption("available-1", 1),
						huh.NewOption("disabled-2", 2),
						huh.NewOption("disabled-3", 3),
						huh.NewOption("disabled-4", 4),
						huh.NewOption("available-5", 5),
						huh.NewOption("disabled-6", 6),
					).
					Value(&selected),
				[]bool{true, false, true, true, true, false, true},
			)

			model, _ := field.Update(test.key)
			field = model.(*disabledSelectField)
			hovered, ok := field.Hovered()
			if !ok || hovered != test.expected {
				t.Fatalf("hovered (%d, %t); want (%d, true)", hovered, ok, test.expected)
			}
		})
	}
}

func TestDisabledPickerLabelsRemainExplicitWithoutTerminalColor(t *testing.T) {
	labels := disabledPickerLabels([]string{"claude-sonnet-5", "gpt-5.6-terra"}, []bool{true, false})
	if labels[0] == "claude-sonnet-5" || labels[1] != "gpt-5.6-terra" {
		t.Fatalf("disabled labels = %#v", labels)
	}
}

func TestPickWithDisabledRendersAllUnavailableRowsBeforeRejecting(t *testing.T) {
	defer func(previous func() bool) { isInteractive = previous }(isInteractive)
	isInteractive = func() bool { return false }
	previousOut := cliout.Out
	var output bytes.Buffer
	cliout.Out = &output
	t.Cleanup(func() { cliout.Out = previousOut })

	_, err := PickWithDisabled(
		"pick",
		[]string{"claude-opus-5", "claude-sonnet-5"},
		[]bool{true, true},
		0,
	)
	if !errors.Is(err, ErrPickUnavailable) {
		t.Fatalf("all-disabled picker error = %v, want ErrPickUnavailable", err)
	}
	if strings.Contains(err.Error(), ErrPickUnavailable.Error()) {
		t.Fatalf("all-disabled picker leaked internal sentinel in user error: %q", err)
	}
	if err.Error() != i18n.T("cliprompt.pick_nothing_available") {
		t.Fatalf("all-disabled picker error = %q, want localized message %q", err, i18n.T("cliprompt.pick_nothing_available"))
	}
	for _, model := range []string{"claude-opus-5", "claude-sonnet-5"} {
		if !strings.Contains(output.String(), model) {
			t.Fatalf("all-disabled picker output %q does not contain %s", output.String(), model)
		}
	}
}
