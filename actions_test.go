package main

import (
	tea "github.com/charmbracelet/bubbletea"
	"strings"
	"testing"
)

func TestQN014RegistryCompleteUniqueExecutable(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range actionRegistry() {
		if seen[a.ID] || a.ID == "" || a.Title == "" || a.Description == "" || a.Category == "" || a.Execute == nil || a.Available == nil || a.Enabled == nil {
			t.Fatalf("bad action %+v", a)
		}
		seen[a.ID] = true
	}
	for _, id := range []string{"note.new", "note.trash", "note.restore", "edit.undo", "edit.redo", "edit.mathMode", "edit.codeMode", "edit.codePair", "shell.pipe", "clipboard.copy", "file.confirm", "search.openResult", "conflict.copy"} {
		if !seen[id] {
			t.Fatal("missing action", id)
		}
	}
}
func TestQN014ActionContextAndDispatch(t *testing.T) {
	m, _, _, _, _ := asyncModel(t)
	m = updateTest(t, m, ActionMsg{"note.new"})
	if m.activeID() != 0 {
		t.Fatal("new action not executed")
	}
	m.state = stateTrash
	before := m.buffer.Text()
	m = updateTest(t, m, ActionMsg{"edit.bold"})
	if m.buffer.Text() != before || m.flashMsg != "Action unavailable" {
		t.Fatal("editable action leaked into Trash")
	}
	restore, _ := findAction("note.restore")
	if ok, reason := restore.Enabled(ActionContext{State: stateTrash, Note: Note{ID: -1}}); ok || reason == "" {
		t.Fatal("missing disabled reason")
	}
}
func TestQN014HelpUsesRegistryBindings(t *testing.T) {
	m, _, _, _, _ := asyncModel(t)
	help := m.generateHelpText()
	for _, want := range []string{"New note", "ctrl+n", "Undo", "Toggle line numbers", "Restore note"} {
		if !strings.Contains(help, want) {
			t.Fatal("help missing", want)
		}
	}
	m = updateTest(t, m, tea.KeyMsg{Type: tea.KeyCtrlN})
	if m.activeID() != 0 {
		t.Fatal("registry keyboard routing failed")
	}
}
