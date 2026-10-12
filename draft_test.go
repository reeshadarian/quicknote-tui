package main

import (
	tea "github.com/charmbracelet/bubbletea"
	"testing"
	"time"
)

func TestQN013ListReorderCannotRedirectSave(t *testing.T) {
	m, a, _, first, second := asyncModel(t)
	m = typeTest(m, "!")
	text := m.buffer.Text()
	m.notes[0], m.notes[1] = m.notes[1], m.notes[0]
	m.index = 0
	m = drainTestCmd(t, m, m.requestSave())
	n, e := a.GetNote(first.ID)
	if e != nil || n.Content != text {
		t.Fatal("index redirected authoritative save")
	}
	n, e = a.GetNote(second.ID)
	if e != nil || n.Content != second.Content {
		t.Fatal("unrelated note overwritten")
	}
}
func TestQN013GhostDraftsHaveIndependentIdentity(t *testing.T) {
	m, _, _, _, _ := asyncModel(t)
	m.Note.ID = 0
	m.Note.SessionID = 0
	m.Note.IsGhost = true
	m.assignSession()
	m.buffer = stringToBuffer("ghost one")
	m.needsSave = true
	m.stashNote()
	first := m.activeKey()
	m = updateTest(t, m, tea.KeyMsg{Type: tea.KeyCtrlN})
	m.Note.IsGhost = true
	m.buffer = stringToBuffer("ghost two")
	m.needsSave = true
	m.stashNote()
	second := m.activeKey()
	if first == second || m.drafts[first].buffer.Text() != "ghost one" || m.drafts[second].buffer.Text() != "ghost two" {
		t.Fatal("ID-zero drafts collided")
	}
	if m.requestSave() != nil {
		t.Fatal("ghost persisted")
	}
}
func TestQN013ContentIsExactAndCursorKeysDoNotEdit(t *testing.T) {
	m, _, _, _, _ := asyncModel(t)
	for _, s := range []string{"a\tb", "a\n", "a\n\n", " leading  "} {
		m.buffer = stringToBuffer(s)
		m.needsSave = false
		m.cursorRow = 0
		m.cursorCol = 0
		m = updateTest(t, m, tea.KeyMsg{Type: tea.KeyRight})
		if m.buffer.Text() != s || m.needsSave {
			t.Fatalf("navigation changed bytes: %q", m.buffer.Text())
		}
	}
}
func TestQN013HistoryGroupingRedoAndBounds(t *testing.T) {
	m, _, _, _, _ := asyncModel(t)
	m = typeTest(m, "x")
	m = typeTest(m, "y")
	if len(m.undoStack) != 1 {
		t.Fatal("typing burst not grouped")
	}
	m = updateTest(t, m, tea.KeyMsg{Type: tea.KeyCtrlZ})
	if m.buffer.Text() != "alpha" {
		t.Fatal("burst undo failed")
	}
	m = typeTest(m, "z")
	if len(m.redoStack) != 0 {
		t.Fatal("redo survived new edit")
	}
	m.HistoryLimits = HistoryLimits{Transactions: 2, Bytes: 100}
	for range 10 {
		m.lastEdit = time.Now().Add(-time.Second)
		m = typeTest(m, "q")
	}
	if len(m.undoStack) > 2 {
		t.Fatal("history not bounded")
	}
}
func TestQN013BufferOperationsAndSourceMap(t *testing.T) {
	b := stringToBuffer("e\u0301\t👩‍💻\n尾")
	points := lineSourceMap(b[0])
	if len(points) != 4 || points[1].Rune != 2 || points[2].Cell != 4 {
		t.Fatalf("source map=%+v", points)
	}
	out, c := b.DeleteBackward(BufferCursor{0, 2})
	if out.Text() != "\t👩‍💻\n尾" || c.Col != 0 || b.Text() != "e\u0301\t👩‍💻\n尾" {
		t.Fatal("grapheme deletion mutated source")
	}
	out, c = out.SplitLine(c)
	out, c = out.JoinLine(c.Row)
	if out.Text() != "\t👩‍💻\n尾" {
		t.Fatal("split/join changed text")
	}
}
