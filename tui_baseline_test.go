package main

import (
    "testing"

    tea "github.com/charmbracelet/bubbletea"
)

// QN-002 / T-01: selecting a search result must change authoritative
// active-note identity, not merely the editor buffer. This test is
// intentionally skipped until the fix task is implemented.
func TestRegressionSearchSelectionMaintainsNoteIdentity(t *testing.T) {
    t.Skip("KNOWN FAILURE QN-002: search Enter changes buffer without selected note identity")
    db := newTestDB(t)
    first := insertTestNote(t,db,"alpha")
    second := insertTestNote(t,db,"bravo")
    m := initialModel(db, nowForTest())
    m.notes = []Note{{ID:first,Content:"alpha"},{ID:second,Content:"bravo"}}
    m.index = 0
    m.buffer = stringToBuffer("alpha")
    m.state = stateSearch
    m.prevState = stateTyping
    m.searchResults = []Note{{ID:second,Content:"bravo"}}
    updated, _ := m.Update(tea.KeyMsg{Type:tea.KeyEnter})
    got := updated.(model)
    if got.notes[got.index].ID != second {
        t.Fatalf("active ID=%d, want selected search ID=%d",got.notes[got.index].ID,second)
    }
}

// QN-004 / T-04: include multi-byte and combining-mark operations in the
// next editor implementation. This baseline asserts only round-trip safety.
func TestEditorBufferUnicodeRoundTrip(t *testing.T) {
    cases := []string{"plain", "café", "東京", "🧪 test", "e\u0301", "line one\nline two"}
    for _, want := range cases {
        if got := bufferToString(stringToBuffer(want)); got != want {
            t.Errorf("roundtrip %#v => %#v",want,got)
        }
    }
}
