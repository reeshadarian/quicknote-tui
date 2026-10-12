package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// QN-002 / T-01: selecting a search result must change authoritative
// active-note identity, not merely the editor buffer. This test is
// intentionally skipped until the fix task is implemented.
func TestRegressionSearchSelectionMaintainsNoteIdentity(t *testing.T) {
	db := newTestDB(t)
	first := insertTestNote(t, db, "alpha")
	second := insertTestNote(t, db, "bravo")
	m := initialModel(db, nowForTest())
	m.notes = []Note{mustGetTestNote(t, db, first), mustGetTestNote(t, db, second)}
	m.index = 0
	m.buffer = stringToBuffer("alpha")
	m.state = stateSearch
	m.prevState = stateTyping
	m.searchResults = []Note{mustGetTestNote(t, db, second)}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(model)
	if got.notes[got.index].ID != second {
		t.Fatalf("active ID=%d, want selected search ID=%d", got.notes[got.index].ID, second)
	}
	if bufferToString(got.buffer) != "bravo" {
		t.Fatalf("editor buffer = %q; want bravo", bufferToString(got.buffer))
	}
	if got.state != stateTyping {
		t.Fatalf("state = %v; want typing", got.state)
	}
	if got.needsSave {
		t.Fatal("selection should not leave stale needsSave")
	}
	if len(got.undoStack) != 0 || len(got.redoStack) != 0 {
		t.Fatal("undo history leaked across notes")
	}
	got.buffer = stringToBuffer("bravo edited")
	got.notes[got.index].Content = bufferToString(got.buffer)
	saveNote(db, got.notes[got.index])
	if gotContent := loadNoteContent(db, first); gotContent != "alpha" {
		t.Fatalf("source overwritten: %q", gotContent)
	}
	if gotContent := loadNoteContent(db, second); gotContent != "bravo edited" {
		t.Fatalf("selected note not saved: %q", gotContent)
	}
}

// QN-004 / T-04: include multi-byte and combining-mark operations in the
// next editor implementation. This baseline asserts only round-trip safety.
func TestEditorBufferUnicodeRoundTrip(t *testing.T) {
	cases := []string{"plain", "café", "東京", "🧪 test", "e\u0301", "line one\nline two"}
	for _, want := range cases {
		if got := bufferToString(stringToBuffer(want)); got != want {
			t.Errorf("roundtrip %#v => %#v", want, got)
		}
	}
}

func TestSearchSelectionResetsHistoryAndKeepsTrashReadOnly(t *testing.T) {
	db := newTestDB(t)
	first := insertTestNote(t, db, "first")
	second := insertTestNote(t, db, "second")
	trashNote(db, second)
	m := initialModel(db, nowForTest())
	m.notes = []Note{mustGetTestNote(t, db, first)}
	m.index = 0
	m.buffer = stringToBuffer("first")
	m.undoStack = []textState{{content: "old note"}}
	m.redoStack = []textState{{content: "redo old note"}}
	m.state = stateSearch
	m.prevState = stateTrash
	m.searchResults = []Note{{ID: second, Content: "second", MatchRow: 200, MatchCol: 300}}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(model)
	if got.state != stateTrash {
		t.Fatalf("trashed result entered editable state: %v", got.state)
	}
	if got.notes[got.index].ID != second {
		t.Fatalf("opened wrong trashed ID: %d", got.notes[got.index].ID)
	}
	if len(got.undoStack) != 0 || len(got.redoStack) != 0 {
		t.Fatal("history leaked into trash")
	}
	if got.cursorRow != 0 || got.cursorCol != len([]rune("second")) {
		t.Fatalf("cursor not clamped: %d/%d", got.cursorRow, got.cursorCol)
	}
}

func TestSearchSelectionSavesDirtyDraftBeforeSwitch(t *testing.T) {
	db := newTestDB(t)
	first := insertTestNote(t, db, "first")
	second := insertTestNote(t, db, "second")
	m := initialModel(db, nowForTest())
	m.notes = []Note{mustGetTestNote(t, db, first), mustGetTestNote(t, db, second)}
	m.index = 0
	m.buffer = stringToBuffer("unsaved first")
	m.needsSave = true
	m.state = stateSearch
	m.prevState = stateTyping
	m.searchResults = []Note{mustGetTestNote(t, db, second)}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(model)
	if loadNoteContent(db, first) != "unsaved first" {
		t.Fatal("dirty draft was lost")
	}
	if got.notes[got.index].ID != second {
		t.Fatal("selected note identity mismatch")
	}
}
