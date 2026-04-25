package tui

import "github.com/charmbracelet/lipgloss"

// Palette — tuned for dark terminals (which is ~all of them).
var (
	colAccent    = lipgloss.Color("39")  // cyan — primary accent
	colBorder    = lipgloss.Color("238") // dim grey — panel borders
	colFg        = lipgloss.Color("252") // near-white
	colFgMuted   = lipgloss.Color("244")
	colFgDim     = lipgloss.Color("240")
	colOK        = lipgloss.Color("42")  // green
	colWarn      = lipgloss.Color("214") // orange
	colErr       = lipgloss.Color("203") // red
	colErrBg     = lipgloss.Color("52")  // dark red bg for banner
	colStatusBg  = lipgloss.Color("237") // statusline background
	colBlack     = lipgloss.Color("0")
)

// Panel wraps the main content area with a rounded border.
var panelStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(colBorder).
	Padding(0, 1)

// Header strip.
var (
	headerBrand = lipgloss.NewStyle().
			Bold(true).
			Foreground(colAccent).
			Render("mcp-gateway")
	headerInfo       = lipgloss.NewStyle().Foreground(colFgMuted)
	headerSep        = lipgloss.NewStyle().Foreground(colFgDim).Render(" │ ")
	headerConnected  = lipgloss.NewStyle().Foreground(colOK).Render("● connected")
	headerDisconnect = lipgloss.NewStyle().Foreground(colErr).Bold(true).Render("⬤ disconnected")
)

// Status-line footer.
var (
	statusTabActive = lipgloss.NewStyle().
			Background(colAccent).
			Foreground(colBlack).
			Bold(true).
			Padding(0, 1)

	statusTabInactive = lipgloss.NewStyle().
				Background(colStatusBg).
				Foreground(colFgMuted).
				Padding(0, 1)

	statusHints = lipgloss.NewStyle().
			Background(colStatusBg).
			Foreground(colFgMuted).
			Padding(0, 1)
)

// Error banner (non-fatal errors above the content area).
var errorBanner = lipgloss.NewStyle().
	Background(colErrBg).
	Foreground(colFg).
	Bold(true).
	Padding(0, 1)

// Table primitives.
var (
	colHeader = lipgloss.NewStyle().
			Foreground(colFgMuted).
			Bold(true)

	selectedRow = lipgloss.NewStyle().
			Foreground(colFg).
			Bold(true)

	selectedBar   = lipgloss.NewStyle().Foreground(colAccent).Render("▌")
	unselectedBar = " "

	disabledText = lipgloss.NewStyle().Foreground(colFgDim).Italic(true)
	mutedText    = lipgloss.NewStyle().Foreground(colFgMuted)
	accentText   = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	errorText    = lipgloss.NewStyle().Foreground(colErr)
)

// State-text styles (colored, used for server.state rendering).
var stateStyle = map[string]lipgloss.Style{
	"running":    lipgloss.NewStyle().Foreground(colOK).Bold(true),
	"starting":   lipgloss.NewStyle().Foreground(colWarn),
	"restarting": lipgloss.NewStyle().Foreground(colWarn).Bold(true),
	"errored":    lipgloss.NewStyle().Foreground(colErr).Bold(true),
	"disabled":   lipgloss.NewStyle().Foreground(colFgDim),
	"stopped":    lipgloss.NewStyle().Foreground(colFgMuted),
}

// State glyphs (colored).
var stateGlyphs = map[string]string{
	"running":    lipgloss.NewStyle().Foreground(colOK).Render("●"),
	"starting":   lipgloss.NewStyle().Foreground(colWarn).Render("◐"),
	"restarting": lipgloss.NewStyle().Foreground(colWarn).Render("◑"),
	"errored":    lipgloss.NewStyle().Foreground(colErr).Render("!"),
	"disabled":   lipgloss.NewStyle().Foreground(colFgDim).Render("○"),
	"stopped":    lipgloss.NewStyle().Foreground(colFgMuted).Render("·"),
}

// stateText returns the human-readable state string styled by category.
func stateText(state string) string {
	if s, ok := stateStyle[state]; ok {
		return s.Render(state)
	}
	return state
}

// glyph returns the state glyph or "?" for unknown states.
func glyph(state string) string {
	if g, ok := stateGlyphs[state]; ok {
		return g
	}
	return "?"
}

// windowAround returns a [start, end) sub-range of n items that contains
// selected and fits in pageSize. Keeps selected roughly centered so the
// viewport scrolls as navigation reaches either edge.
func windowAround(n, selected, pageSize int) (int, int) {
	if pageSize <= 0 || pageSize >= n {
		return 0, n
	}
	if selected < 0 {
		selected = 0
	}
	if selected >= n {
		selected = n - 1
	}
	start := selected - pageSize/2
	if start < 0 {
		start = 0
	}
	end := start + pageSize
	if end > n {
		end = n
		start = end - pageSize
	}
	return start, end
}

// --- backward-compat aliases for pre-polish callers (detail.go, tests).
// Kept narrow so new code uses the named styles above.
var (
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	helpStyle   = mutedText
	errorStyle  = errorText
)
