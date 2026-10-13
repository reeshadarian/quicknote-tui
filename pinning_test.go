package main

import (
	"errors"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"strings"
	"testing"
	"time"
)

func pinModel(t *testing.T) (model, *SQLiteStore, *SQLiteStore, Note, Note) {
	t.Helper()
	m, a, b, n, other := ttlModel(t)
	a.clock = fixedClock{lifecycleNow()}
	b.clock = fixedClock{lifecycleNow()}
	n.Content = "#kill 15 mins\nimportant"
	var err error
	n, err = a.Save(n)
	if err != nil {
		t.Fatal(err)
	}
	m.notes = []Note{n, other}
	m.index = 0
	m.activateNote()
	m.allNotes = []Note{n, other}
	m.searchResults = []Note{n}
	return m, a, b, n, other
}
func pinAction(t *testing.T, m model) (model, tea.Cmd) {
	t.Helper()
	next, cmd := rawUpdate(m, ActionMsg{"note.pin"})
	if cmd == nil {
		t.Fatalf("pin action did not start lifecycle worker: %s", next.flashMsg)
	}
	return next, cmd
}
func TestQN030T20PinSuppressesExpiry(t *testing.T) {
	m, a, _, original, _ := pinModel(t)
	if original.ExpiresAt.String != "2026-10-12T12:15:00.000Z" {
		t.Fatal("fixture must be due in 15m", original.ExpiresAt)
	}
	m, cmd := pinAction(t, m)
	result := cmd().(lifecycleResult)
	m = updateTest(t, m, result)
	n, err := a.GetNote(original.ID)
	directive, _ := ParseKillDirective(n.Content)
	if err != nil || !n.IsPinned || n.ExpiresAt.Valid || n.ExpirationSource != ExpirationNone || n.KillHash != directive.Fingerprint || n.Revision != original.Revision+1 {
		t.Fatal("pin transaction", n, err)
	}
	for _, list := range [][]Note{m.notes, m.allNotes, m.searchResults} {
		if !list[0].IsPinned {
			t.Fatal("pin metadata missing from list/search", list[0])
		}
	}
	if m.lifecycleStatus() != "Pinned" {
		t.Fatal(m.lifecycleStatus())
	}
	restarted := NewSQLiteStoreWithClock(a.db, fixedClock{lifecycleNow().Add(time.Hour)})
	n, err = restarted.Save(n)
	if err != nil {
		t.Fatal(err)
	}
	n.Content = "#kill 2 hours\nimportant"
	n, err = restarted.Save(n)
	if err != nil || n.ExpiresAt.Valid {
		t.Fatal("hidden pinned timer", n, err)
	}
	directive, _ = ParseKillDirective(n.Content)
	if n.KillHash != directive.Fingerprint {
		t.Fatal("changed directive not observed")
	}
	sweep, err := restarted.Maintain(lifecycleNow().Add(4*time.Hour), nil)
	if err != nil || len(sweep.Expired) != 0 {
		t.Fatal(sweep, err)
	}
	trashed, err := restarted.TrashSnapshotCAS(n.ID, n.Revision, DeleteManual, lifecycleNow().Add(4*time.Hour))
	if err != nil || trashed.DeleteReason != DeleteManual || trashed.DeletedAt == "" || !trashed.IsPinned {
		t.Fatal("manual pinned trash", trashed, err)
	}
	retained, err := restarted.Maintain(lifecycleNow().Add(4*time.Hour+72*time.Hour), nil)
	if err != nil || len(retained.Purged) != 0 {
		t.Fatal("72h recovery", retained, err)
	}
}
func TestQN030UnpinNeverRearms(t *testing.T) {
	m, a, _, original, _ := pinModel(t)
	m, cmd := pinAction(t, m)
	m = drainTestCmd(t, m, cmd)
	pinned := m.Note
	for _, cancel := range []string{"enter", "esc"} {
		m = updateTest(t, m, ActionMsg{"note.pin"})
		if m.state != stateConfirm || m.confirmation.Confirm || !strings.Contains(m.View(), "no timer") {
			t.Fatal("unpin safe confirmation", m.state, m.View())
		}
		m = updateTest(t, m, keyMessage(cancel))
		stored, _ := a.GetNote(original.ID)
		if !stored.IsPinned || stored.Revision != pinned.Revision {
			t.Fatal("cancel changed pin", stored)
		}
	}
	m = updateTest(t, m, ActionMsg{"note.pin"})
	m = updateTest(t, m, keyMessage("tab"))
	m = updateTest(t, m, keyMessage("enter"))
	n, err := a.GetNote(original.ID)
	if err != nil || n.IsPinned || n.ExpiresAt.Valid || n.Revision != pinned.Revision+1 {
		t.Fatal("unpin", n, err)
	}
	restarted := NewSQLiteStoreWithClock(a.db, fixedClock{lifecycleNow().Add(24 * time.Hour)})
	n, err = restarted.Save(n)
	if err != nil || n.ExpiresAt.Valid {
		t.Fatal("unchanged directive rearmed", n, err)
	}
	result, err := restarted.Maintain(lifecycleNow().Add(25*time.Hour), nil)
	if err != nil || len(result.Expired) != 0 {
		t.Fatal(result, err)
	}
	m = drainTestCmd(t, m, m.requestOpen(n.ID, false, nil, false))
	m = ttlReview(t, m, 0)
	m = updateTest(t, m, keyMessage("tab"))
	m = updateTest(t, m, keyMessage("enter"))
	stored, _ := a.GetNote(n.ID)
	if !stored.ExpiresAt.Valid || stored.ExpirationSource != ExpirationPreset {
		t.Fatal("new ttl failed", stored)
	}
}
func TestQN030PinRace(t *testing.T) {
	t.Run("dirty save and late edit", func(t *testing.T) {
		m, a, _, n, other := pinModel(t)
		m = typeTest(m, " dirty")
		m, save := pinAction(t, m)
		saved := save().(saveResult)
		m, pin := rawUpdate(m, saved)
		if pin == nil {
			t.Fatal("save did not dispatch pin")
		}
		result := pin().(lifecycleResult)
		m = typeTest(m, " newest")
		text, history := m.buffer.Text(), len(m.undoStack)
		m = drainTestCmd(t, m, m.requestOpen(other.ID, false, nil, false))
		active := m.buffer.Text()
		m = updateTest(t, m, result)
		d := m.drafts[n.ID]
		stored, err := a.GetNote(n.ID)
		if err != nil || !stored.IsPinned || stored.Content != text || d.buffer.Text() != text || len(d.undoStack) != history || m.activeID() != other.ID || m.buffer.Text() != active {
			t.Fatal("late pin lost owner text/history", stored, d, err)
		}
	})
	t.Run("remote conflict", func(t *testing.T) {
		m, a, b, n, _ := pinModel(t)
		m, cmd := pinAction(t, m)
		deadline := lifecycleNow().Add(time.Hour)
		remote, err := b.ApplyExpirationCAS(n.ID, n.Revision, ExpirationChoice{Deadline: &deadline, Source: ExpirationPreset}, lifecycleNow())
		if err != nil {
			t.Fatal(err)
		}
		m = updateTest(t, m, cmd())
		stored, _ := a.GetNote(n.ID)
		if stored.IsPinned || stored.ExpiresAt != remote.ExpiresAt || stored.Revision != remote.Revision || !errors.Is(m.saveErr, ErrConflict) || m.buffer.Text() != n.Content {
			t.Fatal("conflict cleared remote metadata", stored, m.saveErr)
		}
	})
	t.Run("write failure", func(t *testing.T) {
		m, a, _, n, _ := pinModel(t)
		_, err := a.db.Exec("CREATE TRIGGER block_pin BEFORE UPDATE ON notes BEGIN SELECT RAISE(ABORT,'synthetic pin failure'); END")
		if err != nil {
			t.Fatal(err)
		}
		m, cmd := pinAction(t, m)
		m = updateTest(t, m, cmd())
		stored, _ := a.GetNote(n.ID)
		if stored.IsPinned || stored.Revision != n.Revision || stored.ExpiresAt != n.ExpiresAt || m.saveErr == nil {
			t.Fatal("failed pin changed state", stored, m.saveErr)
		}
	})
}
func TestQN030KeyboardAndLayout(t *testing.T) {
	for _, profile := range []string{"default", "legacy"} {
		m, _, _, _, _ := pinModel(t)
		m.keymap = defaultKeymap(profile)
		if err := m.keymap.Validate(); err != nil {
			t.Fatal(err)
		}
		m = updateTest(t, m, keyMessage(m.keyboard().keys(mustPinCommand(t, "commands.open"))[0]))
		found := false
		for i, a := range m.commandActions() {
			if a.ID == "note.pin" {
				found = true
				m.commandIndex = i
				if a.Title != "Pin note" {
					t.Fatal(a.Title)
				}
			}
		}
		if !found {
			t.Fatal("missing keyboard command")
		}
		m = updateTest(t, m, keyMessage("enter"))
		if !m.Note.IsPinned {
			t.Fatal("Commands pin failed")
		}
		m = updateTest(t, m, ActionMsg{"commands.open"})
		for i, a := range m.commandActions() {
			if a.ID == "note.pin" {
				m.commandIndex = i
				if a.Title != "Unpin note" {
					t.Fatal(a.Title)
				}
			}
		}
		m = updateTest(t, m, keyMessage("enter"))
		for _, size := range [][2]int{{120, 40}, {80, 24}, {50, 12}, {25, 5}, {1, 1}, {2, 2}} {
			m = updateTest(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			view := m.View()
			if lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] {
				t.Fatal(size, view)
			}
			if size[0] >= 25 && (!strings.Contains(view, "Cancel") || !strings.Contains(view, "Confirm")) {
				t.Fatal("hidden confirmation choice", view)
			}
			t.Logf("%s unpin %dx%d:\n%s", profile, size[0], size[1], view)
		}
		m = updateTest(t, m, keyMessage("esc"))
		if m.state != stateTyping {
			t.Fatal("lost Commands parent", m.state)
		}
		m.keymap.Bindings["note.pin"] = []string{"alt+p"}
		if profile == "legacy" {
			m.keymap.Bindings["note.pin"] = []string{"alt+q"}
		}
		if err := m.keymap.Validate(); err != nil {
			t.Fatal(err)
		}
		m = updateTest(t, m, keyMessage(m.keymap.Bindings["note.pin"][0]))
		if m.state != stateConfirm {
			t.Fatal("custom binding failed")
		}
	}
	a := mustPinCommand(t, "note.pin")
	for _, c := range []ActionContext{{State: stateTyping, Note: Note{ID: 3, IsGhost: true}}, {State: stateTrash, Note: Note{ID: 3}}, {State: stateTyping, Note: Note{ID: 3, DeletedAt: "trashed"}}, {State: stateTyping}} {
		if enabled, reason := a.Enabled(c); enabled || reason == "" {
			t.Fatal("invalid pin available", c)
		}
	}
	m, _, _, _, _ := pinModel(t)
	m.Note.IsPinned = true
	m.pushOverlay(stateSearch)
	m.searchResults[0].IsPinned = true
	if !strings.Contains(m.View(), "Pinned") {
		t.Fatal("search pin label missing", m.View())
	}
}
func mustPinCommand(t *testing.T, id string) Action {
	t.Helper()
	a, ok := findAction(id)
	if !ok {
		t.Fatal(id)
	}
	return a
}

func TestQN030SearchPreviewSurvivesPin(t *testing.T) {
	m, _, _, _, _ := pinModel(t)
	m.searchResults[0].Preview = "important <<M_START>>snippet<<M_END>>"
	m, cmd := pinAction(t, m)
	m = drainTestCmd(t, m, cmd)
	m.pushOverlay(stateSearch)
	view := m.View()
	if !strings.Contains(view, "[Pinned] important snippet") {
		t.Fatal("pin result erased search preview", view)
	}
}
func TestQN030QueuedPinAndCancellation(t *testing.T) {
	for _, change := range []string{"none", "edit", "switch"} {
		t.Run(change, func(t *testing.T) {
			m, a, _, n, other := pinModel(t)
			at := lifecycleNow().Add(time.Hour)
			first := m.requestLifecycle(lifecycleIntent{Kind: "expiration", Expiration: ExpirationChoice{Deadline: &at, Source: ExpirationPreset}})
			m, _ = rawUpdate(m, ActionMsg{"note.pin"})
			switch change {
			case "edit":
				m = typeTest(m, " newest")
			case "switch":
				m = drainTestCmd(t, m, m.requestOpen(other.ID, false, nil, false))
			}
			escape := keyMessage("esc")
			m, exit := rawUpdate(m, escape)
			if exit != nil && len(m.lifecycleRequests) == 0 {
				t.Fatal("exited before request finished")
			}
			m = drainTestCmd(t, m, first)
			stored, err := a.GetNote(n.ID)
			if err != nil {
				t.Fatal(err)
			}
			if change == "none" {
				if !stored.IsPinned || stored.ExpiresAt.Valid || stored.Revision != n.Revision+2 {
					t.Fatal("queued pin dropped", stored)
				}
			} else if stored.IsPinned {
				t.Fatal("queued pin escaped source guard", stored)
			}
			if len(m.lifecycleRequests) != 0 || len(m.lifecycleQueued) != 0 || m.exitPending() {
				t.Fatal("canceled pin stalled exit")
			}
		})
	}
}
func TestQN030StaleUnpinConfirmation(t *testing.T) {
	m, a, _, n, other := pinModel(t)
	m, cmd := pinAction(t, m)
	m = drainTestCmd(t, m, cmd)
	m = updateTest(t, m, ActionMsg{"note.pin"})
	c := m.confirmation
	c.Confirm = true
	m.popOverlay()
	m = drainTestCmd(t, m, m.requestOpen(other.ID, false, nil, false))
	if cmd := m.confirmPin(c); cmd != nil {
		t.Fatal("stale confirmation dispatched on another note")
	}
	stored, _ := a.GetNote(n.ID)
	if !stored.IsPinned {
		t.Fatal("unpin crossed notes")
	}
}
