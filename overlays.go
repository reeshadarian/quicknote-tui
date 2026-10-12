package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

type OverlayFrame struct {
	State, Previous, CommandReturn appState
	Viewport                       viewport.Model
}
type Confirmation struct {
	Kind, Title, Body, Payload string
	Key                        int
	Version                    uint64
	Confirm                    bool
}

func isOverlay(s appState) bool { return s != stateTyping && s != stateTrash }
func (m *model) pushOverlay(state appState) {
	m.overlays = append(m.overlays, OverlayFrame{m.state, m.prevState, m.commandReturn, m.viewport})
	m.state = state
}
func (m *model) popOverlay() {
	w, h := m.viewport.Width, m.viewport.Height
	if len(m.overlays) > 0 {
		frame := m.overlays[len(m.overlays)-1]
		m.overlays = m.overlays[:len(m.overlays)-1]
		m.state, m.prevState, m.commandReturn = frame.State, frame.Previous, frame.CommandReturn
		m.viewport = frame.Viewport
		m.viewport.Width, m.viewport.Height = w, h
	} else {
		switch m.state {
		case stateSearch, stateHelp:
			m.state = m.prevState
		case stateCommands:
			m.state = m.commandReturn
		default:
			m.state = stateTyping
		}
	}
	m.openGeneration++
	m.searchGeneration++
	m.loadPending = false
	m.searchPending = false
	m.pendingAction = nil
	m.pendingOpen = nil
}
func (m *model) clearOverlays(state appState) { m.overlays = nil; m.state = state }
func (m *model) confirm(kind, title, body, payload string) {
	m.confirmation = Confirmation{Kind: kind, Title: title, Body: body, Payload: payload, Key: m.activeKey(), Version: m.localVersion}
	m.pushOverlay(stateConfirm)
}
func (m *model) showError(title string, err error) {
	m.flashMsg = title + ": " + err.Error()
	m.confirm("error", title, err.Error()+"\nDraft kept. Close this dialog to retry.", "")
}
func (m model) confirmationView() string {
	choice := "> Cancel | Confirm"
	if m.confirmation.Confirm {
		choice = "Cancel | > Confirm"
	}
	footer := "Esc Cancel · Tab Choose\n" + choice
	if m.confirmation.Kind == "error" {
		footer = "Esc Close · Enter Close"
	}
	if m.confirmation.Kind == "reload" {
		footer = "Esc Cancel · " + m.shortcut("conflict.reload") + " Confirm\n" + choice
	}
	return m.panel(m.confirmation.Title, m.confirmation.Body, footer)
}
func (m *model) confirmationKey(key string) tea.Cmd {
	if m.confirmation.Kind == "reload" && key == strings.ToLower(m.shortcut("conflict.reload")) {
		key = "r"
	}
	switch key {
	case "esc", "ctrl+c":
		m.popOverlay()
		m.confirmReload = false
		return nil
	case "tab", "left", "right":
		m.confirmation.Confirm = !m.confirmation.Confirm
		return nil
	case "r":
		if m.confirmation.Kind != "reload" {
			return nil
		}
		m.confirmation.Confirm = true
	case "enter":
		if m.confirmation.Kind == "error" || !m.confirmation.Confirm {
			m.popOverlay()
			m.confirmReload = false
			return nil
		}
	default:
		return nil
	}
	c := m.confirmation
	m.popOverlay()
	if c.Key != m.activeKey() || c.Version != m.localVersion {
		m.flashMsg = "Confirmation canceled: draft changed"
		return nil
	}
	switch c.Kind {
	case "ghost":
		return m.mutate("ghost")
	case "purge":
		return m.mutate("purge")
	case "reload":
		return m.requestOpen(m.activeID(), false, nil, true)
	case "replace":
		m.undoStack = pushStack(m.undoStack, m.buffer.Text(), m.cursorRow, m.cursorCol)
		m.redoStack = nil
		m.typingGroup = false
		m.buffer = stringToBuffer(c.Payload)
		m.cursorRow, m.cursorCol = 0, 0
		m.needsSave = true
		m.highlightDirty = true
	case "overwrite":
		return m.writeExport(c.Payload, true)
	}
	return nil
}

type exportCheckResult struct {
	Request uint64
	Key     int
	Version uint64
	Path    string
	Exists  bool
	Err     error
}
type exportResult struct {
	Request uint64
	Key     int
	Path    string
	Err     error
}

func (m *model) checkExport(path string) tea.Cmd {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			m.showError("Export failed", err)
			return nil
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	m.exportGeneration++
	request, key, version := m.exportGeneration, m.activeKey(), m.localVersion
	return func() tea.Msg {
		_, err := os.Stat(path)
		exists := err == nil
		if os.IsNotExist(err) {
			err = nil
		}
		return exportCheckResult{request, key, version, path, exists, err}
	}
}
func (m *model) writeExport(path string, allow ...bool) tea.Cmd {
	overwrite := len(allow) > 0 && allow[0]
	request, key, text := m.exportGeneration, m.activeKey(), m.buffer.Text()
	return func() tea.Msg {
		f, err := os.CreateTemp(filepath.Dir(path), ".quicknote-export-*")
		if err != nil {
			return exportResult{request, key, path, err}
		}
		name := f.Name()
		defer os.Remove(name)
		if err = f.Chmod(0600); err == nil {
			_, err = f.WriteString(text)
		}
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil {
			if overwrite {
				err = os.Rename(name, path)
			} else {
				err = os.Link(name, path)
			}
		}
		return exportResult{request, key, path, err}
	}
}
func (m *model) receiveExportCheck(msg exportCheckResult) tea.Cmd {
	if msg.Request != m.exportGeneration || msg.Key != m.activeKey() || msg.Version != m.localVersion || m.state != statePromptFile {
		return nil
	}
	if msg.Err != nil {
		m.showError("Export failed", msg.Err)
		return nil
	}
	if msg.Exists {
		m.confirm("overwrite", "Overwrite file?", "Replace "+msg.Path+"? Existing contents will be replaced.", msg.Path)
		return nil
	}
	return m.writeExport(msg.Path)
}
func (m *model) receiveExport(msg exportResult) {
	if msg.Request != m.exportGeneration {
		return
	}
	if msg.Key != m.activeKey() {
		if msg.Err != nil {
			m.flashMsg = "Earlier draft export failed: " + msg.Err.Error()
		} else {
			m.flashMsg = "Exported earlier snapshot: " + filepath.Base(msg.Path)
		}
		return
	}
	if msg.Err != nil {
		m.showError("Export failed", msg.Err)
		return
	}
	if m.state == statePromptFile {
		m.popOverlay()
		m.fileInput.Reset()
	}
	m.flashMsg = fmt.Sprintf("Exported %s", filepath.Base(msg.Path))
}
