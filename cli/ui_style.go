package main

import (
	"charm.land/lipgloss/v2"
)

// Palette and styles of the TUI: dark-friendly, one vivid accent per meaning.
var (
	colAccent = lipgloss.Color("#8B5CF6") // Eriphe, focus
	colUser   = lipgloss.Color("#22D3EE")
	colWarn   = lipgloss.Color("#F59E0B") // waits for me
	colOK     = lipgloss.Color("#22C55E")
	colBad    = lipgloss.Color("#EF4444")
	colDim    = lipgloss.Color("#6B7280")
	colText   = lipgloss.Color("#E5E7EB")
	colBorder = lipgloss.Color("#374151")

	styleTitle   = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	styleDim     = lipgloss.NewStyle().Foreground(colDim)
	styleText    = lipgloss.NewStyle().Foreground(colText)
	styleUser    = lipgloss.NewStyle().Bold(true).Foreground(colUser)
	styleBrain   = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	styleEvent   = lipgloss.NewStyle().Italic(true).Foreground(colWarn)
	styleError   = lipgloss.NewStyle().Foreground(colBad)
	styleOK      = lipgloss.NewStyle().Foreground(colOK)
	styleKey     = lipgloss.NewStyle().Bold(true).Foreground(colText)
	styleBadge   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#111827")).Background(colWarn).Padding(0, 1)
	styleSelect  = lipgloss.NewStyle().Background(lipgloss.Color("#1F2937"))
	stylePane    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colBorder).Padding(0, 1)
	styleFocused = stylePane.BorderForeground(colAccent)
	stylePerm    = lipgloss.NewStyle().Border(lipgloss.ThickBorder()).BorderForeground(colWarn).Padding(0, 1)
)

// stateStyle colours a task state by what it asks of me.
func stateStyle(state string) lipgloss.Style {
	switch state {
	case stateWaitingPermission:
		return lipgloss.NewStyle().Bold(true).Foreground(colWarn)
	case stateRunning, statePending:
		return styleOK
	case stateIdle:
		return lipgloss.NewStyle().Foreground(colUser)
	case stateFailed, stateLost:
		return styleError
	}
	return styleDim
}

// stateIcon is a one-cell symbol for a task state.
func stateIcon(state string) string {
	switch state {
	case stateWaitingPermission:
		return "!"
	case stateRunning:
		return "●"
	case statePending:
		return "○"
	case stateIdle:
		return "◆"
	case stateFailed, stateLost:
		return "✕"
	}
	return "■"
}
