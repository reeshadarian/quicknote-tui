package main

import (
	"errors"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"strings"
	"testing"
	"time"
)

func trashModel(t *testing.T) (model, *SQLiteStore, *SQLiteStore, Note) {
	t.Helper()
	m, a, b, _, _ := ttlModel(t)
	a.clock = fixedClock{lifecycleNow().Add(-time.Hour)}
	n, err := a.Save(Note{Content: "#kill 15 mins\nexpired"})
	if err != nil {
		t.Fatal(err)
	}
	n, err = a.TrashSnapshotCAS(n.ID, n.Revision, DeleteExpired, lifecycleNow())
	if err != nil {
		t.Fatal(err)
	}
	m = drainTestCmd(t, m, m.requestOpen(n.ID, true, nil, false))
	return m, a, b, n
}
func restoreChoice(t *testing.T, m model, choice int) model {
	t.Helper()
	m = updateTest(t, m, keyMessage("ctrl+r"))
	if m.state != stateConfirm || m.confirmation.Confirm {
		t.Fatalf("restore must default Cancel: state=%v", m.state)
	}
	for i := 0; i < choice; i++ {
		m = updateTest(t, m, keyMessage("tab"))
	}
	return updateTest(t, m, keyMessage("enter"))
}
func TestQN032T21RestoreWithoutTimer(t *testing.T) {
	m, a, _, n := trashModel(t)
	m = restoreChoice(t, m, 0)
	unchanged, _ := a.GetNote(n.ID)
	if unchanged.Revision != n.Revision || unchanged.DeletedAt == "" {
		t.Fatal("Cancel changed Trash")
	}
	m = restoreChoice(t, m, 1)
	got, err := a.GetNote(n.ID)
	if err != nil || got.ID != n.ID || got.DeletedAt != "" || got.ExpiresAt.Valid || m.state != stateTyping {
		t.Fatal(got, err, m.state)
	}
	restart := NewSQLiteStoreWithClock(a.db, fixedClock{lifecycleNow().Add(24 * time.Hour)})
	got, err = restart.Save(got)
	if err != nil || got.ExpiresAt.Valid {
		t.Fatal("restore rearmed", got, err)
	}
	sweep, err := restart.Maintain(lifecycleNow().Add(48*time.Hour), nil)
	if err != nil || len(sweep.Expired) != 0 {
		t.Fatal(sweep, err)
	}
}
func TestQN032RestoreResetTimer(t *testing.T) {
	m, a, _, n := trashModel(t)
	m = restoreChoice(t, m, 2)
	got, err := a.GetNote(n.ID)
	if err != nil || got.ExpiresAt.String != "2026-10-12T12:15:00.000Z" || got.DeletedAt != "" {
		t.Fatal(got, err)
	}
}
func TestQN032TrashReadOnly(t *testing.T) {
	m, a, _, n := trashModel(t)
	before := m.buffer.Text()
	for _, key := range []string{"x", "=", "enter", "backspace", "ctrl+b", "alt+m", "alt+v", "ctrl+z", "ctrl+k"} {
		m = updateTest(t, m, keyMessage(key))
		if m.state == stateCommands {
			m = updateTest(t, m, keyMessage("esc"))
		}
	}
	m = updateTest(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("paste\n2+2="), Paste: true})
	if m.buffer.Text() != before || m.needsSave {
		t.Fatal("Trash edited")
	}
	got, _ := a.GetNote(n.ID)
	if got.Content != n.Content || got.Revision != n.Revision {
		t.Fatal("Trash saved")
	}
	if !strings.Contains(m.View(), "recoverable until") || !strings.Contains(m.View(), "expired") {
		t.Fatal("missing Trash metadata", m.View())
	}
}
func TestQN032T31UndoSameID(t *testing.T) {
	m, a, _, n, other := ttlModel(t)
	other.Content = n.Content
	var err error
	other, err = a.Save(other)
	if err != nil {
		t.Fatal(err)
	}
	m = updateTest(t, m, keyMessage("ctrl+d"))
	m = drainTestCmd(t, m, m.requestOpen(other.ID, false, nil, false))
	m = updateTest(t, m, keyMessage("ctrl+d"))
	m = updateTest(t, m, ActionMsg{"trash.undoLast"})
	first, _ := a.GetNote(n.ID)
	second, _ := a.GetNote(other.ID)
	if first.DeletedAt == "" || second.DeletedAt != "" || second.ExpiresAt.Valid {
		t.Fatal("undo must restore latest original ID", first, second, m.flashMsg)
	}
}
func TestQN032DeleteForeverGuard(t *testing.T) {
	m, a, _, n := trashModel(t)
	m = updateTest(t, m, ActionMsg{"trash.deleteForever"})
	if m.state != stateConfirm || m.confirmation.Confirm {
		t.Fatal("missing default Cancel", m.state, m.flashMsg)
	}
	m = updateTest(t, m, keyMessage("enter"))
	if _, err := a.GetNote(n.ID); err != nil {
		t.Fatal("Cancel deleted", err)
	}
	m = updateTest(t, m, ActionMsg{"trash.deleteForever"})
	m = updateTest(t, m, keyMessage("tab"))
	m = updateTest(t, m, keyMessage("enter"))
	if _, err := a.GetNote(n.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("not permanently deleted", err)
	}
	if m.state != stateTrash || !strings.Contains(m.View(), "No deleted notes") {
		t.Fatal("missing empty Trash", m.state, m.View())
	}
}
func TestQN032EmptyNoteTrash(t *testing.T) {
	m, a, _, n, _ := ttlModel(t)
	n.Content = ""
	var err error
	n, err = a.Save(n)
	if err != nil {
		t.Fatal(err)
	}
	m = drainTestCmd(t, m, m.requestOpen(n.ID, false, nil, false))
	m = updateTest(t, m, keyMessage("ctrl+d"))
	got, err := a.GetNote(n.ID)
	if err != nil || got.DeletedAt == "" || got.DeleteReason != DeleteManual || m.state == stateConfirm {
		t.Fatal("empty persisted deletion must use Trash", got, err, m.state)
	}
}

func TestQN032DiscardUnsavedNeverCreatesTrash(t *testing.T) {
	m, a, _, _, _ := ttlModel(t)
	m = updateTest(t, m, keyMessage("ctrl+n"))
	m = typeTest(m, "unsaved scratch")
	key := m.activeKey()
	m = updateTest(t, m, keyMessage("ctrl+d"))
	var count int
	if err := a.db.QueryRow("SELECT count(*) FROM notes").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 || m.trashUndo.ID != 0 {
		t.Fatal("discard persisted unsaved draft", count, m.trashUndo)
	}
	if _, ok := m.drafts[key]; ok {
		t.Fatal("discard retained unsaved draft")
	}
	m = updateTest(t, m, ActionMsg{"ghost.new"})
	m = typeTest(m, "memory")
	m = updateTest(t, m, keyMessage("ctrl+d"))
	if m.trashUndo.ID != 0 {
		t.Fatal("ghost discard became Trash")
	}
}
func TestQN032RestoreInvalidResetAndPinned(t *testing.T) {
	for _, kind := range []string{"preset", "invalid", "review", "pinned", "malformed-date"} {
		t.Run(kind, func(t *testing.T) {
			m, a, _, n := trashModel(t)
			switch kind {
			case "preset":
				_, _ = a.db.Exec("UPDATE notes SET expiration_source='preset' WHERE id=?", n.ID)
			case "invalid":
				_, _ = a.db.Exec("UPDATE notes SET content='#kill unknown' WHERE id=?", n.ID)
			case "review":
				_, _ = a.db.Exec("UPDATE notes SET expiration_needs_review=1 WHERE id=?", n.ID)
			case "pinned":
				_, _ = a.db.Exec("UPDATE notes SET is_pinned=1 WHERE id=?", n.ID)
			case "malformed-date":
				_, _ = a.db.Exec("UPDATE notes SET deleted_at='uncertain legacy time' WHERE id=?", n.ID)
			}
			m = drainTestCmd(t, m, m.requestOpen(n.ID, true, nil, true))
			m = updateTest(t, m, keyMessage("ctrl+r"))
			if kind != "malformed-date" {
				m = updateTest(t, m, keyMessage("tab"))
				m = updateTest(t, m, keyMessage("tab"))
				if m.confirmation.Confirm {
					t.Fatal("invalid reset offered", kind)
				}
			}
			m = updateTest(t, m, keyMessage("esc"))
			m = restoreChoice(t, m, 1)
			got, err := a.GetNote(n.ID)
			if err != nil || got.DeletedAt != "" || got.ExpiresAt.Valid {
				t.Fatal("safe restore failed", got, err)
			}
			if kind == "pinned" && !got.IsPinned {
				t.Fatal("restore unpinned note")
			}
		})
	}
}
func TestQN032StaleConfirmationAndPurge(t *testing.T) {
	for _, kind := range []string{"restore", "deleteForever"} {
		for _, change := range []string{"local-revision", "remote-restore", "remote-purge"} {
			t.Run(kind+change, func(t *testing.T) {
				m, a, b, n := trashModel(t)
				if kind == "restore" {
					m = updateTest(t, m, keyMessage("ctrl+r"))
				} else {
					m = updateTest(t, m, ActionMsg{"trash.deleteForever"})
				}
				m = updateTest(t, m, keyMessage("tab"))
				switch change {
				case "local-revision":
					m.Note.Revision++
				case "remote-restore":
					if _, err := b.RestoreSnapshotCAS(n.ID, n.Revision, RestoreWithoutTimer, lifecycleNow()); err != nil {
						t.Fatal(err)
					}
				case "remote-purge":
					if err := b.DeleteTrashCAS(n.ID, n.Revision); err != nil {
						t.Fatal(err)
					}
				}
				m = updateTest(t, m, keyMessage("enter"))
				got, err := a.GetNote(n.ID)
				if change == "local-revision" {
					if err != nil || got.DeletedAt == "" || got.Revision != n.Revision {
						t.Fatal("stale confirmation mutated")
					}
				} else if m.state != stateConfirm || m.confirmation.Kind != "error" {
					t.Fatal("remote race error invisible", m.flashMsg, m.state)
				}
			})
		}
	}
}
func TestQN032KeyboardAndLayouts(t *testing.T) {
	for _, profile := range []string{"default", "legacy", "custom"} {
		t.Run(profile, func(t *testing.T) {
			m, _, _, _ := trashModel(t)
			m.keymap = defaultKeymap(profile)
			if profile == "custom" {
				m.keymap = defaultKeymap("default")
				m.keymap.Bindings["trash.deleteForever"] = []string{"alt+d"}
				m.keymap.Bindings["trash.undoLast"] = []string{"alt+u"}
				m.keymap.Bindings["note.restore"] = []string{"alt+r"}
			}
			if err := m.keymap.Validate(); err != nil {
				t.Fatal(err)
			}
			for _, size := range [][2]int{{120, 40}, {80, 24}, {50, 12}, {25, 5}, {1, 1}, {2, 2}} {
				m = updateTest(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				for _, id := range []string{"note.restore", "trash.deleteForever", "trash.undoLast"} {
					m = updateTest(t, m, keyMessage(strings.ToLower(m.shortcut("commands.open"))))
					found := false
					for _, a := range m.commandActions() {
						if a.ID == id {
							found = true
							break
						}
						m = updateTest(t, m, keyMessage("down"))
					}
					if !found {
						t.Fatal("keyboard command missing", id)
					}
					if size[0] >= 25 {
						action, _ := findAction(id)
						if !strings.Contains(m.View(), "> "+action.Title) {
							t.Fatal("selected Trash command hidden", id, m.View())
						}
					}
					t.Logf("%s %dx%d %s menu:\n%s", profile, size[0], size[1], id, m.View())
					if id == "trash.undoLast" {
						m = updateTest(t, m, keyMessage("enter"))
						if m.state != stateCommands || !strings.Contains(m.View(), "No move") && size[0] >= 80 {
							t.Fatal("Undo unavailable explanation missing", m.View())
						}
						m = updateTest(t, m, keyMessage("esc"))
						continue
					}
					m = updateTest(t, m, keyMessage("enter"))
					if m.state != stateConfirm || m.confirmation.Confirm {
						t.Fatal("menu route unsafe", id)
					}
					view := m.View()
					if lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] {
						t.Fatal("overflow", view)
					}
					if size[0] >= 25 && !strings.Contains(view, "Cancel") {
						t.Fatal("Cancel invisible", view)
					}
					m = updateTest(t, m, keyMessage("tab"))
					view = m.View()
					if size[0] >= 25 && id == "trash.deleteForever" && !strings.Contains(view, "Delete forever") {
						t.Fatal("destructive choice must be labeled", view)
					}
					t.Logf("selected:\n%s", view)
					m = updateTest(t, m, keyMessage("esc"))
					if m.state != stateTrash {
						t.Fatal("lost Trash parent")
					}
				}
			}
			if profile == "custom" {
				m = updateTest(t, m, keyMessage("alt+r"))
				if m.state != stateConfirm {
					t.Fatal("custom restore route")
				}
				m = updateTest(t, m, keyMessage("esc"))
				m = updateTest(t, m, keyMessage("alt+d"))
				if m.state != stateConfirm {
					t.Fatal("custom delete route")
				}
			}
		})
	}
}
