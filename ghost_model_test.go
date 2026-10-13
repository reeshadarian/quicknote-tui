package main

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ghostChoice(t *testing.T, m model, choice int) model {
	t.Helper()
	m = updateTest(t, m, ActionMsg{"note.ghost"})
	if m.state != stateConfirm {
		t.Fatal("missing ghost confirmation")
	}
	for i := 0; i < choice; i++ {
		m = updateTest(t, m, keyMessage("tab"))
	}
	return updateTest(t, m, keyMessage("enter"))
}
func TestQN031T22NoGhostWrites(t *testing.T) {
	m, a, _, _, _ := asyncModel(t)
	m = updateTest(t, m, ActionMsg{"ghost.new"})
	if !m.Note.IsGhost || m.Note.ID != 0 || m.Note.SessionID == 0 {
		t.Fatal("New Ghost did not allocate memory identity before typing")
	}
	m = typeTest(m, "QN031SENTINEL #private #kill 1s")
	m = drainTestCmd(t, m, m.requestSave())
	m = drainTestCmd(t, m, m.requestMaintenance())
	m = updateTest(t, m, ActionMsg{"note.promote"})
	var count int
	if err := a.db.QueryRow("SELECT count(*) FROM notes WHERE content LIKE '%QN031SENTINEL%'").Scan(&count); err != nil || count != 0 {
		t.Fatal("ghost persisted", count, err)
	}
	if err := a.db.QueryRow("SELECT count(*) FROM notes_fts WHERE notes_fts MATCH 'QN031SENTINEL'").Scan(&count); err != nil || count != 0 {
		t.Fatal("ghost indexed", count, err)
	}
	if err := a.db.QueryRow("SELECT count(*) FROM tags WHERE tag='private'").Scan(&count); err != nil || count != 0 {
		t.Fatal("ghost tagged", count, err)
	}
}
func TestQN031T23KeepOriginal(t *testing.T) {
	m, a, _, first, _ := asyncModel(t)
	m = ghostChoice(t, m, 0)
	if m.Note.IsGhost {
		t.Fatal("default Cancel converted")
	}
	m = ghostChoice(t, m, 1)
	n, err := a.GetNote(first.ID)
	if err != nil || n.Content != first.Content || n.DeletedAt != "" || n.Revision != first.Revision {
		t.Fatal("duplicate changed original", n, err)
	}
	if !m.Note.IsGhost || m.Note.ID != 0 || m.buffer.Text() != first.Content || m.Note.Revision != 0 || m.Note.IsPinned || m.Note.ExpiresAt.Valid {
		t.Fatal("unsafe ghost snapshot")
	}
	if _, ok := m.drafts[first.ID]; !ok {
		t.Fatal("source session lost")
	}
}
func TestQN031TrashOriginalRecoverable(t *testing.T) {
	m, a, _, first, _ := asyncModel(t)
	m = ghostChoice(t, m, 2)
	n, err := a.GetNote(first.ID)
	if err != nil || n.Content != first.Content || n.DeletedAt == "" || n.DeleteReason != DeleteManual {
		t.Fatal("original not recoverable in Trash", n, err)
	}
	if !m.Note.IsGhost || m.Note.ID != 0 {
		t.Fatal("not ghost")
	}
}
func TestQN031GhostPersistRequiresConfirmation(t *testing.T) {
	m, a, _, _, _ := asyncModel(t)
	m = updateTest(t, m, ActionMsg{"ghost.new"})
	if !m.Note.IsGhost {
		t.Fatal("ghost unavailable")
	}
	m = typeTest(m, "explicit QN031PERSIST")
	m = ghostChoice(t, m, 0)
	if !m.Note.IsGhost {
		t.Fatal("cancel persisted")
	}
	m = ghostChoice(t, m, 1)
	if m.Note.IsGhost || m.Note.ID <= 0 {
		t.Fatal("confirmed persistence failed")
	}
	var count int
	if err := a.db.QueryRow("SELECT count(*) FROM notes WHERE content LIKE '%QN031PERSIST%'").Scan(&count); err != nil || count != 1 {
		t.Fatal("persistence must create exactly one", count, err)
	}
}
func TestQN031ExplicitOutputConfirmation(t *testing.T) {
	for _, action := range []string{"clipboard.copy", "note.export"} {
		t.Run(action, func(t *testing.T) {
			m, _, _, _, _ := asyncModel(t)
			m = updateTest(t, m, ActionMsg{"ghost.new"})
			if !m.Note.IsGhost {
				t.Fatal("ghost unavailable")
			}
			m = typeTest(m, "QN031OUTPUT")
			m = updateTest(t, m, ActionMsg{action})
			path := filepath.Join(t.TempDir(), "ghost.md")
			if action == "note.export" {
				m.fileInput.SetValue(path)
				m = updateTest(t, m, keyMessage("enter"))
			}
			if m.state != stateConfirm || m.confirmation.Confirm || !strings.Contains(m.confirmation.Body, "memory") {
				t.Fatal("output lacked default-Cancel privacy warning")
			}
			m = updateTest(t, m, keyMessage("enter"))
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("cancel wrote file")
			}
			if !m.Note.IsGhost {
				t.Fatal("output changed persistence")
			}
		})
	}
}
func TestQN031StaleConversion(t *testing.T) {
	m, a, _, first, _ := asyncModel(t)
	m = updateTest(t, m, keyMessage("ctrl+g"))
	m = updateTest(t, m, keyMessage("tab"))
	m, cmd := rawUpdate(m, keyMessage("enter"))
	if cmd == nil {
		t.Fatal("missing conversion worker")
	}
	m = typeTest(m, "latest")
	newest := m.buffer.Text()
	history := len(m.undoStack)
	m = drainTestCmd(t, m, cmd)
	if m.activeID() != first.ID || m.buffer.Text() != newest || len(m.undoStack) != history {
		t.Fatal("late conversion lost source draft")
	}
	n, err := a.GetNote(first.ID)
	if err != nil || n.DeletedAt != "" {
		t.Fatal("late duplicate lost original")
	}
	ghosts := 0
	for _, n := range m.notes {
		if n.IsGhost {
			ghosts++
			if n.Content != first.Content {
				t.Fatal("captured ghost snapshot changed")
			}
		}
	}
	if ghosts != 1 {
		t.Fatal("captured ghost missing")
	}
}

func TestQN031GhostDiscardClearsReferences(t *testing.T) {
	m, _, _, _, _ := asyncModel(t)
	m = updateTest(t, m, ActionMsg{"ghost.new"})
	m = typeTest(m, "DISCARD_PRIVATE")
	key := m.activeKey()
	m.stashNote()
	staleHighlight := m.requestHighlight()
	m = updateTest(t, m, ActionMsg{"note.export"})
	path := filepath.Join(t.TempDir(), "discard.md")
	check := m.checkExport(path)
	m.confirm("replace", "Filter output", "DISCARD_PRIVATE", "DISCARD_PRIVATE")
	m.discardGhost()
	m = drainTestCmd(t, m, check)
	m = drainTestCmd(t, m, staleHighlight)
	if _, ok := m.drafts[key]; ok {
		t.Fatal("discard retained draft/history")
	}
	for _, notes := range [][]Note{m.notes, m.allNotes, m.searchResults} {
		for _, n := range notes {
			if noteKey(n) == key || strings.Contains(n.Content, "DISCARD_PRIVATE") {
				t.Fatal("discard retained summary")
			}
		}
	}
	if strings.Contains(m.buffer.Text()+m.cachedHighlight+m.confirmation.Payload+m.confirmation.Body+m.viewport.View()+m.flashMsg, "DISCARD_PRIVATE") || len(m.undoStack) != 0 || len(m.redoStack) != 0 || m.pendingOpen != nil {
		t.Fatal("discard retained content-bearing references")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("discard allowed obsolete export")
	}
}
func TestQN031DirtySourceSaveGate(t *testing.T) {
	for _, kind := range []string{"new", "duplicate", "trash"} {
		t.Run(kind, func(t *testing.T) {
			for _, fail := range []bool{false, true} {
				m, a, _, first, _ := asyncModel(t)
				m = typeTest(m, " latest")
				text := m.buffer.Text()
				if fail {
					if _, err := a.db.Exec("CREATE TRIGGER reject_ghost_source BEFORE UPDATE ON notes BEGIN SELECT RAISE(ABORT,'synthetic write failure'); END"); err != nil {
						t.Fatal(err)
					}
				}
				if kind == "new" {
					m = updateTest(t, m, ActionMsg{"ghost.new"})
				} else {
					choice := 1
					if kind == "trash" {
						choice = 2
					}
					m = ghostChoice(t, m, choice)
				}
				if fail {
					if m.Note.IsGhost || m.buffer.Text() != text || !m.needsSave || m.saveErr == nil {
						t.Fatal("failed source save transitioned or lost text")
					}
				} else {
					n, err := a.GetNote(first.ID)
					if err != nil || n.Content != text || !m.Note.IsGhost {
						t.Fatal("source not saved before ghost", err)
					}
				}
			}
		})
	}
}
func TestQN031GhostSessionsAndEmptyDatabaseNavigation(t *testing.T) {
	m, a, _, _, _ := asyncModel(t)
	if _, err := a.db.Exec("DELETE FROM tags; DELETE FROM notes"); err != nil {
		t.Fatal(err)
	}
	m.notes = []Note{{}}
	m.drafts = nil
	m.index = 0
	m.activateNote()
	m = updateTest(t, m, ActionMsg{"ghost.new"})
	m = typeTest(m, "first ghost")
	one := m.activeKey()
	oneHistory := len(m.undoStack)
	m = updateTest(t, m, ActionMsg{"ghost.new"})
	m = typeTest(m, "second ghost")
	two := m.activeKey()
	m = updateTest(t, m, ActionMsg{"note.promote"})
	if one == two || m.drafts[one].buffer.Text() != "first ghost" || m.drafts[two].buffer.Text() != "second ghost" || len(m.drafts[one].undoStack) != oneHistory {
		t.Fatal("ghost sessions lost on empty database load")
	}
	for i, n := range m.notes {
		if noteKey(n) == one {
			m.index = i
			m.activateNote()
			break
		}
	}
	m = updateTest(t, m, ActionMsg{"edit.undo"})
	if m.buffer.Text() != "" || m.drafts[two].buffer.Text() != "second ghost" {
		t.Fatal("undo crossed ghost sessions")
	}
}
func TestQN031StaleTrashConversion(t *testing.T) {
	for _, switchNote := range []bool{false, true} {
		t.Run(fmt.Sprint(switchNote), func(t *testing.T) {
			m, a, _, first, second := asyncModel(t)
			cmd := m.requestGhostConversion(TrashOriginal)
			if cmd == nil {
				t.Fatal("missing trash worker")
			}
			if switchNote {
				m = drainTestCmd(t, m, m.requestOpen(second.ID, false, nil, false))
			} else {
				m = typeTest(m, "newer")
			}
			text, history, id := m.buffer.Text(), len(m.undoStack), m.activeID()
			m = drainTestCmd(t, m, cmd)
			if m.activeID() != id || m.buffer.Text() != text || len(m.undoStack) != history {
				t.Fatal("stale trash changed current draft")
			}
			n, err := a.GetNote(first.ID)
			if err != nil || n.DeletedAt == "" || n.Content != first.Content {
				t.Fatal("trash original not preserved")
			}
			ghosts := 0
			for _, n := range m.notes {
				if n.IsGhost {
					ghosts++
					if n.Content != first.Content {
						t.Fatal("wrong captured snapshot")
					}
				}
			}
			if ghosts != 1 {
				t.Fatal("missing captured ghost")
			}
			if !switchNote && (m.state != stateConflict || !m.needsSave) {
				t.Fatal("newer deleted draft lacks recovery")
			}
		})
	}
}
func TestQN031ExternalConversionConflict(t *testing.T) {
	for _, disposition := range []GhostDisposition{KeepOriginal, TrashOriginal} {
		m, a, b, first, _ := asyncModel(t)
		cmd := m.requestGhostConversion(disposition)
		first.Content = "remote QN031"
		saved, err := b.Save(first)
		if err != nil {
			t.Fatal(err)
		}
		m = drainTestCmd(t, m, cmd)
		n, err := a.GetNote(first.ID)
		if err != nil || n.Content != saved.Content || n.DeletedAt != "" || m.Note.IsGhost || m.state != stateConflict {
			t.Fatal("external save lost or conversion bypassed CAS")
		}
	}
}
func TestQN031UnsavedSourceConversionClearsPendingContent(t *testing.T) {
	m, _, _, _, _ := asyncModel(t)
	m = updateTest(t, m, ActionMsg{"note.new"})
	m = typeTest(m, "UNSAVED_SOURCE")
	m = ghostChoice(t, m, 1)
	if !m.Note.IsGhost || len(m.lifecycleQueued) != 0 || len(m.lifecycleRequests) != 0 || len(m.saves) != 0 {
		t.Fatal("conversion leaked content-bearing pending source", m.lifecycleQueued)
	}
}
func TestQN031ConversionCancelPreservesDirtySource(t *testing.T) {
	m, a, _, first, _ := asyncModel(t)
	m = typeTest(m, "draft")
	text := m.buffer.Text()
	history := len(m.undoStack)
	m = ghostChoice(t, m, 0)
	n, err := a.GetNote(first.ID)
	if err != nil || n.Content != first.Content || m.buffer.Text() != text || !m.needsSave || len(m.undoStack) != history {
		t.Fatal("Cancel changed source")
	}
}

func TestQN031LateTrashAfterObservedRestoreKeepsGhostSnapshot(t *testing.T) {
	m, a, b, first, _ := asyncModel(t)
	cmd := m.requestGhostConversion(TrashOriginal)
	result := cmd().(lifecycleResult)
	restored, err := b.RestoreSnapshotCAS(first.ID, result.Note.Revision, RestoreWithoutTimer, nowForTest())
	if err != nil {
		t.Fatal(err)
	}
	// Observe the newer complete persisted snapshot before the held mutation acknowledgment.
	m = drainTestCmd(t, m, m.requestOpen(restored.ID, false, nil, false))
	m = drainTestCmd(t, m, func() tea.Msg { return result })
	n, err := a.GetNote(first.ID)
	if err != nil || n.DeletedAt != "" || n.Revision != restored.Revision {
		t.Fatal("late conversion regressed restored original")
	}
	ghosts := 0
	for _, n := range m.notes {
		if n.IsGhost {
			ghosts++
			if n.Content != first.Content {
				t.Fatal("wrong captured ghost")
			}
		}
	}
	if ghosts != 1 {
		t.Fatal("late successful conversion lost captured ghost")
	}
}
func TestQN031QueuedConversionCanceledByEditOrSwitch(t *testing.T) {
	for _, switchNote := range []bool{false, true} {
		m, a, _, first, second := asyncModel(t)
		m = typeTest(m, "one")
		save := m.requestGhostConversion(TrashOriginal)
		if save == nil {
			t.Fatal("missing source save")
		}
		if switchNote {
			m = drainTestCmd(t, m, m.requestOpen(second.ID, false, nil, false))
		} else {
			m = typeTest(m, "two")
		}
		text := m.buffer.Text()
		m = drainTestCmd(t, m, save)
		n, err := a.GetNote(first.ID)
		if err != nil || n.DeletedAt != "" || m.buffer.Text() != text {
			t.Fatal("obsolete queued conversion trashed or replaced source")
		}
		for _, n := range m.notes {
			if n.IsGhost {
				t.Fatal("obsolete queued conversion created ghost")
			}
		}
		if len(m.lifecycleQueued) != 0 {
			t.Fatal("obsolete queued conversion retained content")
		}
	}
}

func TestQN031EmptyGhostExplicitPersistenceCreatesOneNote(t *testing.T) {
	m, a, _, _, _ := asyncModel(t)
	m = updateTest(t, m, ActionMsg{"ghost.new"})
	var before, after int
	if err := a.db.QueryRow("SELECT count(*) FROM notes").Scan(&before); err != nil {
		t.Fatal(err)
	}
	m = ghostChoice(t, m, 1)
	if err := a.db.QueryRow("SELECT count(*) FROM notes").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if m.Note.IsGhost || m.activeID() <= 0 || after != before+1 {
		t.Fatal("explicit empty ghost persistence failed to create one note")
	}
}

func TestQN031EmptyGhostPersistFailureRetainsRetryAuthorization(t *testing.T) {
	m, a, _, _, _ := asyncModel(t)
	m = updateTest(t, m, ActionMsg{"ghost.new"})
	if _, err := a.db.Exec("CREATE TRIGGER reject_empty_ghost BEFORE INSERT ON notes BEGIN SELECT RAISE(ABORT,'synthetic failure'); END"); err != nil {
		t.Fatal(err)
	}
	m = ghostChoice(t, m, 1)
	if m.saveErr == nil || !m.needsSave || m.activeID() != 0 {
		t.Fatal("failed explicit creation not retryable")
	}
	if _, err := a.db.Exec("DROP TRIGGER reject_empty_ghost"); err != nil {
		t.Fatal(err)
	}
	m = updateTest(t, m, keyMessage("esc"))
	m = updateTest(t, m, keyMessage("enter"))
	if m.activeID() <= 0 || m.saveErr != nil || m.needsSave {
		t.Fatal("empty explicit persistence retry lost authorization")
	}
}
func TestQN031KeyboardDiscardDropsHistory(t *testing.T) {
	m, _, _, _, _ := asyncModel(t)
	m = updateTest(t, m, ActionMsg{"ghost.new"})
	m = typeTest(m, "discard me")
	key := m.activeKey()
	m = updateTest(t, m, keyMessage("ctrl+d"))
	if m.Note.IsGhost || m.buffer.Text() != "" {
		t.Fatal("Ctrl+D did not discard ghost")
	}
	if _, ok := m.drafts[key]; ok {
		t.Fatal("Ctrl+D retained ghost history")
	}
}

func TestQN031CommandsExplainGhostDiscard(t *testing.T) {
	m, _, _, _, _ := asyncModel(t)
	m = updateTest(t, m, ActionMsg{"ghost.new"})
	m = updateTest(t, m, ActionMsg{"commands.open"})
	found := false
	for _, a := range m.commandActions() {
		if a.ID == "note.trash" {
			found = true
			if !strings.Contains(strings.ToLower(a.Title), "discard") || !strings.Contains(strings.ToLower(a.Description), "memory") {
				t.Fatal("ghost deletion promises recoverable Trash")
			}
		}
	}
	if !found {
		t.Fatal("ghost discard is inaccessible")
	}
}

func TestQN031UnsavedEditedConversionDropsOldSessionIntent(t *testing.T) {
	m, _, _, _, _ := asyncModel(t)
	m = updateTest(t, m, ActionMsg{"note.new"})
	m = typeTest(m, "initial")
	save := m.requestGhostConversion(KeepOriginal)
	m = typeTest(m, "newest")
	text := m.buffer.Text()
	m = drainTestCmd(t, m, save)
	if m.Note.IsGhost || m.buffer.Text() != text || len(m.lifecycleQueued) != 0 {
		t.Fatal("canceled unsaved conversion retained an orphan content-bearing intent", len(m.lifecycleQueued))
	}
}

// Public key routing must cancel an unstarted conversion before the save gate
// resumes navigation. A direct requestOpen call does not exercise this contract.
func TestQN031ReviewPublicTransitionCancelsQueuedConversion(t *testing.T) {
	for _, unsaved := range []bool{false, true} {
		for _, choice := range []int{1, 2} {
			for _, key := range []string{"alt+right", "alt+left", "ctrl+n", "alt+g", "esc"} {
				t.Run(fmt.Sprintf("unsaved=%v/choice=%d/%s", unsaved, choice, key), func(t *testing.T) {
					m, a, _, first, second := asyncModel(t)
					target := second.ID
					if key == "alt+left" {
						m.index = 1
						m.activateNote()
						first = second
						target = m.notes[0].ID
					}
					if unsaved {
						m = updateTest(t, m, keyMessage("ctrl+n"))
						if key == "alt+left" {
							source := m.notes[0]
							m.notes = append(m.notes[1:], source)
							m.index = len(m.notes) - 1
							m.activateNote()
							target = m.notes[m.index-1].ID
						} else {
							target = m.notes[1].ID
						}
					}
					m = typeTest(m, " source dirty")
					text := m.buffer.Text()
					history := len(m.undoStack)
					m = updateTest(t, m, keyMessage("ctrl+g"))
					for i := 0; i < choice; i++ {
						m = updateTest(t, m, keyMessage("tab"))
					}
					var held tea.Cmd
					m, held = rawUpdate(m, keyMessage("enter"))
					if held == nil {
						t.Fatal("source save not held")
					}
					m, early := rawUpdate(m, keyMessage(key))
					if early != nil {
						t.Fatal("transition bypassed source save")
					}
					// Edit once more while the save is held, forcing a second save after an
					// unsaved source receives its persisted identity.
					if unsaved {
						m = typeTest(m, " newest")
						text = m.buffer.Text()
						history = len(m.undoStack)
					}
					var quit bool
					m, quit = drainGhostReviewCmd(t, m, held)
					var id int
					if err := a.db.QueryRow("SELECT id FROM notes WHERE content=?", text).Scan(&id); err != nil {
						t.Fatal("latest source not saved", err)
					}
					stored, err := a.GetNote(id)
					if err != nil || stored.DeletedAt != "" {
						t.Fatal("queued competing transition trashed source", err)
					}
					d := m.drafts[id]
					if d.buffer.Text() != text || len(d.undoStack) != history {
						t.Fatal("source history lost")
					}
					switch key {
					case "alt+right", "alt+left":
						if m.activeID() != target {
							t.Fatal("wrong navigation target", m.activeID(), target)
						}
					case "ctrl+n":
						if m.activeID() != 0 || m.Note.IsGhost || m.buffer.Text() != "" {
							t.Fatal("New note not completed")
						}
					case "alt+g":
						if !m.Note.IsGhost || m.buffer.Text() != "" {
							t.Fatal("New Ghost not completed")
						}
					case "esc":
						if !quit {
							t.Fatal("exit not completed")
						}
					}
					ghosts := 0
					for _, n := range m.notes {
						if n.IsGhost {
							ghosts++
						}
					}
					want := 0
					if key == "alt+g" {
						want = 1
					}
					if ghosts != want || m.pendingAction != nil || len(m.lifecycleQueued) != 0 || len(m.lifecycleRequests) != 0 || len(m.saves) != 0 {
						t.Fatal("queued conversion or continuation leaked", ghosts, m.pendingAction)
					}
					_ = first
				})
			}
		}
	}
}

// Like the common test driver, with an observable terminal Quit outcome.
func drainGhostReviewCmd(t *testing.T, m model, cmd tea.Cmd) (model, bool) {
	t.Helper()
	queue := []tea.Cmd{cmd}
	quit := false
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 200 {
			t.Fatal("command cycle")
		}
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		msg := c()
		switch msg := msg.(type) {
		case tea.QuitMsg:
			quit = true
		case tea.BatchMsg:
			queue = append(queue, msg...)
		case saveResult, lifecycleResult, loadResult, recoveryResult, highlightMsg:
			var next tea.Cmd
			m, next = rawUpdate(m, msg)
			queue = append(queue, next)
		}
	}
	return m, quit
}

func TestQN031ReviewStartedConversionPreservesPendingTransition(t *testing.T) {
	for _, choice := range []int{1, 2} {
		for _, key := range []string{"alt+right", "alt+left", "ctrl+n", "alt+g", "esc"} {
			for _, dirty := range []bool{false, true} {
				t.Run(fmt.Sprintf("choice=%d/%s/dirty=%v", choice, key, dirty), func(t *testing.T) {
					m, a, _, first, second := asyncModel(t)
					target := second.ID
					if key == "alt+left" {
						m.index = 1
						m.activateNote()
						first = second
						target = m.notes[0].ID
					}
					m = updateTest(t, m, keyMessage("ctrl+g"))
					for i := 0; i < choice; i++ {
						m = updateTest(t, m, keyMessage("tab"))
					}
					var held tea.Cmd
					m, held = rawUpdate(m, keyMessage("enter"))
					result := held().(lifecycleResult)
					if result.Err != nil {
						t.Fatal(result.Err)
					}
					if dirty {
						m = typeTest(m, " newer")
					}
					text, history := m.buffer.Text(), len(m.undoStack)
					var nav tea.Cmd
					m, nav = rawUpdate(m, keyMessage(key))
					// Hold public navigation loads as well as the successful conversion ack.
					// Completion must not activate the captured ghost and invalidate this load.
					var ack tea.Cmd
					m, ack = rawUpdate(m, result)
					var quit bool
					m, quit = drainGhostReviewCmd(t, m, ack)
					var navQuit bool
					m, navQuit = drainGhostReviewCmd(t, m, nav)
					quit = quit || navQuit
					n, err := a.GetNote(first.ID)
					if err != nil || (n.DeletedAt != "") != (choice == 2) {
						t.Fatal("successful conversion write not reconciled", n, err)
					}
					d := m.drafts[first.ID]
					if d.buffer.Text() != text || len(d.undoStack) != history {
						t.Fatal("source latest text/history lost")
					}
					if choice == 2 && dirty && key == "esc" {
						if quit || m.state != stateConflict || !m.needsSave {
							t.Fatal("exit lost dirty trashed source recovery")
						}
						return
					}
					switch key {
					case "alt+right", "alt+left":
						if m.activeID() != target {
							t.Fatal("pending navigation interrupted", m.activeID(), target, m.pendingAction)
						}
					case "ctrl+n":
						if m.activeID() != 0 || m.Note.IsGhost || m.buffer.Text() != "" {
							t.Fatal("pending New note interrupted")
						}
					case "alt+g":
						if !m.Note.IsGhost || m.buffer.Text() != "" {
							t.Fatal("pending New Ghost interrupted")
						}
					case "esc":
						if !quit {
							t.Fatal("exit stalled")
						}
					}
					ghosts := 0
					captured := 0
					for _, n := range m.notes {
						if n.IsGhost {
							ghosts++
							if n.Content == first.Content {
								captured++
							}
						}
					}
					want := 1
					if key == "alt+g" {
						want = 2
					}
					if ghosts != want || captured != 1 || m.pendingAction != nil || len(m.lifecycleRequests) != 0 || len(m.saves) != 0 {
						t.Fatal("captured ghost or continuation incorrect", ghosts, captured, m.pendingAction)
					}
				})
			}
		}
	}
}

func TestQN031ReviewStartedTrashRetainsRecoveryAtNavigationBoundary(t *testing.T) {
	m, _, _, _, _ := asyncModel(t)
	m = updateTest(t, m, keyMessage("ctrl+g"))
	m = updateTest(t, m, keyMessage("tab"))
	m = updateTest(t, m, keyMessage("tab"))
	m, held := rawUpdate(m, keyMessage("enter"))
	result := held().(lifecycleResult)
	m = typeTest(m, " newer")
	text, history := m.buffer.Text(), len(m.undoStack)
	m, _ = rawUpdate(m, keyMessage("alt+left"))
	m, ack := rawUpdate(m, result)
	m, quit := drainGhostReviewCmd(t, m, ack)
	if quit || m.state != stateConflict || m.buffer.Text() != text || len(m.undoStack) != history || !m.needsSave {
		t.Fatal("unavailable navigation bypassed dirty source recovery", m.state)
	}
}
