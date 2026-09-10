package computeruse

import (
	"strings"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
	"github.com/everyapi-ai/everyapi-sdk/sanitizer"
)

func sensitiveMatches(text string) []sanitizer.Match {
	return sanitizer.Scan(text, sanitizer.BuiltinDetectors())
}

func rejectSensitiveText(text string) error {
	matches := sensitiveMatches(sanitizeObservedText(text))
	if len(matches) == 0 {
		return nil
	}
	names := make([]string, 0, len(matches))
	seen := make(map[string]bool, len(matches))
	for _, match := range matches {
		if !seen[match.DetectorName] {
			seen[match.DetectorName] = true
			names = append(names, match.DetectorName)
		}
	}
	return NewError(CodeSensitiveText, "refusing to send detected credential text through computer use ("+strings.Join(names, ", ")+")", nil)
}

func redactSensitiveText(text string) string {
	text = sanitizeObservedText(text)
	matches := sensitiveMatches(text)
	if len(matches) == 0 {
		return text
	}
	var b strings.Builder
	cursor := 0
	for _, match := range matches {
		b.WriteString(text[cursor:match.Start])
		b.WriteString("[REDACTED:")
		b.WriteString(match.DetectorName)
		b.WriteByte(']')
		cursor = match.End
	}
	b.WriteString(text[cursor:])
	return b.String()
}

func sanitizeObservedText(text string) string {
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = cliout.Sanitize(lines[i])
	}
	return strings.Join(lines, "\n")
}

func redactState(state State) State {
	state.App.Name = redactSensitiveText(state.App.Name)
	state.App.BundleID = redactSensitiveText(state.App.BundleID)
	state.Window.Title = redactSensitiveText(state.Window.Title)
	state.Snapshot.TreeText = redactSensitiveText(state.Snapshot.TreeText)
	for i := range state.Snapshot.Elements {
		state.Snapshot.Elements[i].Title = redactSensitiveText(state.Snapshot.Elements[i].Title)
		state.Snapshot.Elements[i].Description = redactSensitiveText(state.Snapshot.Elements[i].Description)
		state.Snapshot.Elements[i].Value = redactSensitiveText(state.Snapshot.Elements[i].Value)
	}
	if state.RefreshError != nil {
		state.RefreshError.Message = redactSensitiveText(state.RefreshError.Message)
	}
	if state.ScreenshotError != nil {
		state.ScreenshotError.Message = redactSensitiveText(state.ScreenshotError.Message)
	}
	return state
}
