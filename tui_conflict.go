package main

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
)

func (m *model) recordSaveError(err error) {
	m.saveErr = err
	m.needsSave = true
	m.flashMsg = "Save failed: " + err.Error()
	if errors.Is(err, ErrConflict) || errors.Is(err, ErrTrashed) || errors.Is(err, ErrNotFound) {
		m.state = stateConflict
		m.confirmReload = false
		m.conflictTop = 0
		m.conflictInspect = false
		m.conflictRemote = Note{}
		m.conflictReadErr = fmt.Errorf("loading stored version")
	}
	// Store the base revision with the failed buffer, never adopt the remote
	// revision while retaining local text: doing so would bypass CAS on retry.
	m.stashNote()
}

func (m *model) handleConflictKey(key string) {
	switch key {
	case "esc":
		m.state = stateTyping // Keep editing, with a persistent conflict status.
		m.confirmReload = false
	case "i":
		m.confirmReload = false
		m.conflictInspect = !m.conflictInspect
		m.conflictTop = 0
	case "up":
		if m.conflictTop > 0 {
			m.conflictTop--
		}
	case "down":
		m.conflictTop++
	case "pgup":
		m.conflictTop -= max(1, m.viewport.Height-3)
		if m.conflictTop < 0 {
			m.conflictTop = 0
		}
	case "pgdown":
		m.conflictTop += max(1, m.viewport.Height-3)

	}
}

// Compare corresponding lines without altering either version. Navigation
// through this preview is read-only; inserted lines may shift subsequent pairs.
func (m model) conflictBody() string {
	local := bufferToString(m.buffer)
	remote := m.conflictRemote.Content
	if m.conflictReadErr != nil {
		return fmt.Sprintf("Stored version unavailable: %v\nLocal draft:\n%s", m.conflictReadErr, local)
	}
	if !m.conflictInspect {
		return "Local draft:\n" + local + "\nStored version:\n" + remote
	}
	left, right := strings.Split(local, "\n"), strings.Split(remote, "\n")
	var lines []string
	for i := 0; i < max(len(left), len(right)); i++ {
		switch {
		case i < len(left) && i < len(right) && left[i] == right[i]:
			lines = append(lines, fmt.Sprintf("= %d: %s", i+1, left[i]))
		default:
			if i < len(right) {
				lines = append(lines, fmt.Sprintf("Stored %d: %s", i+1, right[i]))
			}
			if i < len(left) {
				lines = append(lines, fmt.Sprintf("Local %d: %s", i+1, left[i]))
			}
		}
	}
	return strings.Join(lines, "\n")
}

func (m model) conflictView() string {
	width := m.viewport.Width
	if width < 1 {
		width = 80
	}
	height := m.viewport.Height
	if height < 1 {
		height = 20
	}
	header := "Conflict: draft kept\nC Copy | R Reload\nI Compare | Esc Edit"
	if m.copyPending {
		header = "Saving copy…\nEsc Keep editing"
	}
	if m.confirmReload {
		header = "Discard local draft?\nR Confirm | Esc Cancel"
	}
	style := lipgloss.NewStyle().Width(width).MaxWidth(width)
	header = style.Render(header)
	available := max(0, height-lipgloss.Height(header))
	// Escape controls in the preview only. Stored and copied bytes stay intact.
	body := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			return r
		}
		return '�'
	}, stripANSI(m.conflictBody()))
	lines := strings.Split(style.Render(body), "\n")
	top := min(m.conflictTop, max(0, len(lines)-available))
	if available > 0 {
		header += "\n" + strings.Join(lines[top:min(len(lines), top+available)], "\n")
	}
	return lipgloss.NewStyle().MaxWidth(width).MaxHeight(height).Render(header)
}
