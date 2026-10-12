package main

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestQN017NestedOverlayFocusAndCancel(t *testing.T) {
	m, _, _, _, _ := asyncModel(t)
	m = typeTest(m, "dirty")
	before, version, undo := m.buffer.Text(), m.localVersion, len(m.undoStack)
	m = updateTest(t, m, keyMessage("ctrl+f"))
	m.searchInput.SetValue("query")
	m = updateTest(t, m, keyMessage("f1"))
	if m.state != stateHelp || len(m.overlays) != 2 {
		t.Fatalf("help stack=%+v", m.overlays)
	}
	m = updateTest(t, m, keyMessage("ctrl+k"))
	if m.state != stateCommands || len(m.overlays) != 3 {
		t.Fatal("nested Commands not focused")
	}
	m = updateTest(t, m, keyMessage("esc"))
	if m.state != stateHelp {
		t.Fatal("cancel skipped parent help")
	}
	m = updateTest(t, m, keyMessage("esc"))
	if m.state != stateSearch || m.searchInput.Value() != "query" || m.prevState != stateTyping {
		t.Fatal("help cancel lost search state")
	}
	m = updateTest(t, m, keyMessage("esc"))
	if m.state != stateTyping || len(m.overlays) != 0 || m.buffer.Text() != before || m.localVersion != version || len(m.undoStack) != undo || !m.needsSave {
		t.Fatal("cancel altered underlying draft")
	}
}
func TestQN017GhostConfirmationDefaultsCancel(t *testing.T) {
	m, a, _, first, _ := asyncModel(t)
	m = updateTest(t, m, keyMessage("ctrl+g"))
	if m.state != stateConfirm || m.confirmation.Confirm {
		t.Fatal("destructive confirmation not default Cancel")
	}
	m = updateTest(t, m, keyMessage("enter"))
	n, e := a.GetNote(first.ID)
	if e != nil || n.ID != first.ID || m.Note.IsGhost {
		t.Fatal("default confirmation removed original")
	}
	m = updateTest(t, m, keyMessage("ctrl+g"))
	m = updateTest(t, m, keyMessage("tab"))
	m = updateTest(t, m, keyMessage("enter"))
	if !m.Note.IsGhost || m.activeID() != 0 || m.activeKey() >= 0 {
		t.Fatal("confirmed ghost identity invalid")
	}
	if _, e = a.GetNote(first.ID); e == nil {
		t.Fatal("confirmed original not removed")
	}
}
func TestQN017OverwriteCancelAndAtomicConfirmation(t *testing.T) {
	m, _, _, _, _ := asyncModel(t)
	path := filepath.Join(t.TempDir(), "note.md")
	if e := os.WriteFile(path, []byte("original"), 0600); e != nil {
		t.Fatal(e)
	}
	m = updateTest(t, m, keyMessage("ctrl+s"))
	m.fileInput.SetValue(path)
	m = updateTest(t, m, keyMessage("enter"))
	if m.state != stateConfirm {
		t.Fatal("overwrite not confirmed")
	}
	m = updateTest(t, m, keyMessage("esc"))
	data, _ := os.ReadFile(path)
	if string(data) != "original" || m.state != statePromptFile {
		t.Fatal("cancel overwrote file or lost prompt")
	}
	m = updateTest(t, m, keyMessage("enter"))
	m = updateTest(t, m, keyMessage("tab"))
	m = updateTest(t, m, keyMessage("enter"))
	data, _ = os.ReadFile(path)
	if string(data) != m.buffer.Text() || m.state != stateTyping {
		t.Fatal("confirmed export failed")
	}
}
func TestQN017ReplacementIsConfirmedAndUndoable(t *testing.T) {
	m, _, _, _, _ := asyncModel(t)
	before := m.buffer.Text()
	m.confirm("replace", "Replace?", "preview", "replacement")
	m = updateTest(t, m, keyMessage("enter"))
	if m.buffer.Text() != before {
		t.Fatal("default cancel replaced draft")
	}
	m.confirm("replace", "Replace?", "preview", "replacement")
	m = updateTest(t, m, keyMessage("tab"))
	m = updateTest(t, m, keyMessage("enter"))
	if m.buffer.Text() != "replacement" || !m.needsSave {
		t.Fatal("confirmed replacement failed")
	}
	m = updateTest(t, m, keyMessage("ctrl+z"))
	if m.buffer.Text() != before {
		t.Fatal("replacement not one undo transaction")
	}
}
func TestQN017ErrorAndConfirmationLayout(t *testing.T) {
	m, _, _, _, _ := asyncModel(t)
	m.confirm("purge", "Delete forever?", "Irreversible", "")
	for _, size := range [][2]int{{120, 40}, {80, 24}, {50, 12}, {25, 5}, {1, 1}} {
		m = updateTest(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		view := m.View()
		if lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] {
			t.Fatal("confirmation overflow")
		}
		if size[0] >= 25 && !strings.Contains(view, "Cancel") {
			t.Fatal("cancel not visible")
		}
	}
	m = updateTest(t, m, keyMessage("esc"))
	m = updateTest(t, m, keyMessage("ctrl+s"))
	m.fileInput.SetValue(filepath.Join(t.TempDir(), "missing", "note.md"))
	m = updateTest(t, m, keyMessage("enter"))
	if m.state != stateConfirm || m.confirmation.Kind != "error" {
		t.Fatal("export error silently swallowed")
	}
	m = updateTest(t, m, keyMessage("esc"))
	if m.state != statePromptFile {
		t.Fatal("error dialog lost parent")
	}
}
func TestQN017MouseCannotEditThroughOverlay(t *testing.T) {
	m, _, _, _, _ := asyncModel(t)
	m.buffer = stringToBuffer("abcd")
	m.cursorCol = 1
	m = updateTest(t, m, keyMessage("ctrl+f"))
	m = updateTest(t, m, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 20, Y: 2})
	if m.cursorCol != 1 {
		t.Fatal("overlay mouse changed editor focus")
	}
}
func TestQN017ChangedDraftRejectsConfirmation(t *testing.T) {
	m, _, _, _, _ := asyncModel(t)
	m.confirm("replace", "Replace?", "old", "old result")
	m.buffer = stringToBuffer("newer")
	m.localVersion++
	m = updateTest(t, m, keyMessage("tab"))
	m = updateTest(t, m, keyMessage("enter"))
	if m.buffer.Text() != "newer" {
		t.Fatal("stale confirmation overwrote newer version")
	}
}
func TestQN017ModalCancelCannotBeUnbound(t *testing.T) {
	k := defaultKeymap("default")
	k.Bindings["search.cancel"] = []string{"alt+q"}
	if k.Validate() == nil {
		t.Fatal("unreachable Escape accepted")
	}
}
func TestQN017ShellPreviewCancelPreservesHistory(t *testing.T) {
	m, _, _, _, _ := asyncModel(t)
	m = typeTest(m, "x")
	before := m.buffer.Text()
	history := len(m.undoStack)
	m = updateTest(t, m, keyMessage("alt+x"))
	m.pipeInput.SetValue("printf replacement")
	m = updateTest(t, m, keyMessage("enter"))
	if m.state != stateConfirm || m.buffer.Text() != before || len(m.undoStack) != history {
		t.Fatal("shell output applied before review")
	}
	m = updateTest(t, m, keyMessage("esc"))
	if m.buffer.Text() != before || len(m.undoStack) != history {
		t.Fatal("shell cancel changed history")
	}
	m = updateTest(t, m, keyMessage("alt+x"))
	m.pipeInput.SetValue("exit 1")
	m = updateTest(t, m, keyMessage("enter"))
	if m.confirmation.Kind != "error" || m.buffer.Text() != before || len(m.undoStack) != history {
		t.Fatal("failed filter changed draft or hid error")
	}
}
func TestQN017NewExportCannotOverwriteRacingFile(t *testing.T) {
	m, _, _, _, _ := asyncModel(t)
	path := filepath.Join(t.TempDir(), "race.md")
	cmd := m.writeExport(path)
	if err := os.WriteFile(path, []byte("racing original"), 0600); err != nil {
		t.Fatal(err)
	}
	m = drainTestCmd(t, m, cmd)
	data, _ := os.ReadFile(path)
	if string(data) != "racing original" || m.confirmation.Kind != "error" {
		t.Fatal("export race overwrote unconfirmed file")
	}
}
func TestQN017HelpFromTrashSearchRestoresScope(t *testing.T) {
	m, _, _, _, _ := asyncModel(t)
	m.state = stateTrash
	m = updateTest(t, m, keyMessage("ctrl+f"))
	m = updateTest(t, m, keyMessage("f1"))
	m = updateTest(t, m, keyMessage("esc"))
	if m.state != stateSearch || m.prevState != stateTrash {
		t.Fatal("nested help lost Trash scope")
	}
}
func TestQN017LateStartupConfigPreservesDraft(t *testing.T) {
	m, _, _, _, _ := asyncModel(t)
	m = typeTest(m, "kept")
	before := m.buffer.Text()
	m = updateTest(t, m, initDataMsg{keyboard: defaultKeymap("legacy"), notes: []Note{{ID: 777, Content: "old"}}})
	if m.keyboard().Profile != "legacy" || m.buffer.Text() != before || m.activeID() == 777 {
		t.Fatal("late config erased draft or ignored profile")
	}
}
func TestQN017SaveErrorDialogRetainsDirtyRetryableDraft(t *testing.T) {
	m, a, _, _, _ := asyncModel(t)
	m = typeTest(m, "unsaved")
	before := m.buffer.Text()
	a.db.Close()
	m = drainTestCmd(t, m, m.requestSave())
	if m.state != stateConfirm || m.confirmation.Kind != "error" || !m.needsSave || m.buffer.Text() != before {
		t.Fatal("save error lost draft or lacked dialog")
	}
	m = updateTest(t, m, keyMessage("esc"))
	if m.state != stateTyping || !m.needsSave || m.saveErr == nil {
		t.Fatal("error acknowledgement cleared retryable failure")
	}
}
