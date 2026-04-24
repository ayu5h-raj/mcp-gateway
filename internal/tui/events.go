// events.go
package tui

import tea "github.com/charmbracelet/bubbletea"

type eventsModel struct{}

func newEventsModel() eventsModel { return eventsModel{} }

func (e eventsModel) Update(_ tea.Msg) (eventsModel, tea.Cmd) { return e, nil }
func (e eventsModel) view(_ model) string                     { return "  events view coming in phase 3" }
