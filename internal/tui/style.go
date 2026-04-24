package tui

import "github.com/charmbracelet/lipgloss"

var (
	colorAccent   = lipgloss.Color("39")  // cyan-ish
	colorMuted    = lipgloss.Color("240") // dim grey
	colorOK       = lipgloss.Color("42")  // green
	colorWarn     = lipgloss.Color("214") // orange
	colorError    = lipgloss.Color("203") // red
	colorDisabled = lipgloss.Color("244") // dimmer grey

	headerStyle   = lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Padding(0, 1)
	tabActive     = lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Padding(0, 1)
	tabInactive   = lipgloss.NewStyle().Foreground(colorMuted).Padding(0, 1)
	helpStyle     = lipgloss.NewStyle().Foreground(colorMuted)
	errorStyle    = lipgloss.NewStyle().Foreground(colorError)
	disabledStyle = lipgloss.NewStyle().Foreground(colorDisabled)

	stateGlyph = map[string]string{
		"running":    lipgloss.NewStyle().Foreground(colorOK).Render("●"),
		"starting":   lipgloss.NewStyle().Foreground(colorWarn).Render("◐"),
		"restarting": lipgloss.NewStyle().Foreground(colorWarn).Render("◑"),
		"errored":    lipgloss.NewStyle().Foreground(colorError).Render("!"),
		"disabled":   lipgloss.NewStyle().Foreground(colorDisabled).Render("○"),
		"stopped":    lipgloss.NewStyle().Foreground(colorMuted).Render("·"),
	}
)

// glyph returns the state glyph or "?" for unknown states.
func glyph(state string) string {
	if g, ok := stateGlyph[state]; ok {
		return g
	}
	return "?"
}
