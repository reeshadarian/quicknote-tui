package main

import (
	"errors"
	tea "github.com/charmbracelet/bubbletea"
	"testing"
	"time"
)

func lifecycleModel(t *testing.T) (model, *SQLiteStore, Note) {
	t.Helper()
	db := newTestDB(t)
	now := lifecycleNow()
	s := NewSQLiteStoreWithClock(db, fixedClock{now})
	n, e := s.Save(Note{Content: "#kill 1 minute\noriginal"})
	if e != nil {
		t.Fatal(e)
	}
	m := initialModel(db, now)
	m.startupPending = false
	m.store = s
	m.clock = fixedClock{now.Add(time.Minute)}
	m.notes = []Note{n}
	m.activateNote()
	return m, s, n
}
func TestQN028DirtyDraftExpiry(t *testing.T) {
	m, s, n := lifecycleModel(t)
	m.buffer = stringToBuffer(n.Content + " newest")
	m.needsSave = true
	m.localVersion++
	m = drainTestCmd(t, m, m.requestMaintenance())
	stored, e := s.GetNote(n.ID)
	if e != nil || stored.Content != n.Content+" newest" || stored.DeleteReason != DeleteExpired || m.needsSave {
		t.Fatal(stored, m.needsSave, e)
	}
}
func TestQN028StaleMaintenance(t *testing.T) {
	m, s, n := lifecycleModel(t)
	r, e := s.Maintain(lifecycleNow().Add(time.Minute), nil)
	if e != nil {
		t.Fatal(e)
	}
	m.maintenanceGeneration = 1
	m.maintenancePending = true
	m.buffer = stringToBuffer("newest after sweep started")
	m.localVersion++
	m.needsSave = true
	m.undoStack = []textState{{content: n.Content}}
	m = updateTest(t, m, maintenanceResult{1, r, nil})
	if m.buffer.Text() != "newest after sweep started" || len(m.undoStack) != 1 || !m.needsSave || !errors.Is(m.saveErr, ErrTrashed) {
		t.Fatal("lost raced draft", m.saveErr, m.buffer.Text())
	}
}
func TestQN028MaintenanceInOverlay(t *testing.T) {
	for _, state := range []appState{stateHelp, stateSearch, stateCommands} {
		t.Run(string(rune('a'+state)), func(t *testing.T) {
			m, s, _ := lifecycleModel(t)
			other, e := s.Save(Note{Content: "other #kill 1 minute"})
			if e != nil {
				t.Fatal(e)
			}
			m.state = state
			cmd := m.requestMaintenance()
			if m.requestMaintenance() != nil {
				t.Fatal("overlapping sweep")
			}
			m = drainTestCmd(t, m, cmd)
			n, e := s.GetNote(other.ID)
			if e != nil || n.DeleteReason != DeleteExpired {
				t.Fatal(n, e)
			}
			if m.state != state {
				t.Fatal("overlay lost")
			}
		})
	}
}
func TestQN028LateExpiryKeepsNewerText(t *testing.T) {
	m, s, n := lifecycleModel(t)
	cmd := m.requestLifecycle(lifecycleIntent{Kind: "expire"})
	msg := cmd().(lifecycleResult)
	m.buffer = stringToBuffer("typed while expiration pending")
	m.needsSave = true
	m.localVersion++
	m = updateTest(t, m, msg)
	if m.buffer.Text() != "typed while expiration pending" || !m.needsSave || !errors.Is(m.saveErr, ErrTrashed) {
		t.Fatal("lost late edit", m.saveErr)
	}
	stored, e := s.GetNote(n.ID)
	if e != nil || stored.Content != n.Content || stored.DeleteReason != DeleteExpired {
		t.Fatal(stored, e)
	}
}
func TestQN028FailedDraftSaveDelaysExpiry(t *testing.T) {
	m, s, n := lifecycleModel(t)
	s.db.Exec("CREATE TRIGGER block_save BEFORE UPDATE ON notes BEGIN SELECT RAISE(ABORT,'synthetic save failure'); END")
	m.buffer = stringToBuffer(n.Content + " dirty")
	m.needsSave = true
	m.localVersion++
	m = drainTestCmd(t, m, m.requestMaintenance())
	stored, e := s.GetNote(n.ID)
	if e != nil || stored.DeletedAt != "" || !m.needsSave || m.saveErr == nil {
		t.Fatal(stored, e, m.saveErr)
	}
}
func TestQN028ClockAndGhost(t *testing.T) {
	m, _, n := lifecycleModel(t)
	m.clock = nil
	m.Note.IsGhost = true
	m.Note.ID = 0
	m.Note.Content = n.Content
	m.buffer = stringToBuffer(n.Content)
	m.needsSave = true
	old := m.Note.KillHash
	next, cmd := m.Update(tickMsg(lifecycleNow().Add(2 * time.Hour)))
	m = drainTestCmd(t, next.(model), cmd)
	if m.Note.ID != 0 || m.Note.KillHash != old || !m.Note.IsGhost {
		t.Fatal("ghost timer acted")
	}
	m.clock = fixedClock{lifecycleNow()}
	m.lastMaintenance = lifecycleNow().Add(time.Hour)
	if m.requestMaintenance() == nil {
		t.Fatal("backward clock blocked sweep")
	}
}

func TestQN028LateSaveAfterSweepPreservesDeletion(t *testing.T) {
	m, s, n := lifecycleModel(t)
	m.buffer = stringToBuffer(n.Content + " saved newest")
	m.needsSave = true
	m.localVersion++
	cmd := m.requestSave()
	saved := cmd().(saveResult)
	r, e := s.Maintain(lifecycleNow().Add(time.Minute), nil)
	if e != nil {
		t.Fatal(e)
	}
	m.maintenanceGeneration = 1
	m = updateTest(t, m, maintenanceResult{1, r, nil})
	m = updateTest(t, m, saved)
	if m.Note.DeletedAt == "" || m.Note.Revision != saved.Note.Revision+1 || m.buffer.Text() != saved.Note.Content {
		t.Fatal("late save revived expired metadata", m.Note)
	}
}

func TestQN028MetadataRequestSnapshotAndExit(t *testing.T) {
	m, _, n := lifecycleModel(t)
	at := lifecycleNow().Add(2 * time.Hour)
	expected := at
	cmd := m.requestLifecycle(lifecycleIntent{Kind: "expiration", Expiration: ExpirationChoice{Deadline: &at, Source: ExpirationPreset}})
	at = at.Add(time.Hour)
	escape := tea.KeyMsg{Type: tea.KeyEsc}
	m.pendingAction = &escape
	if m.finishExit() != nil {
		t.Fatal("quit before lifecycle result")
	}
	result := cmd().(lifecycleResult)
	if result.Err != nil || result.Note.ExpiresAt.String != FormatLifecycleTime(expected) || result.Note.ID != n.ID {
		t.Fatal(result)
	}
	quit := m.receiveLifecycle(result)
	if quit == nil {
		t.Fatal("exit did not resume")
	}
	if _, ok := quit().(tea.QuitMsg); !ok {
		t.Fatal("expected quit")
	}
}

func TestQN028StartupFailureKeepsTypedDraftVisible(t *testing.T) {
	m, _, _ := lifecycleModel(t)
	m.buffer = stringToBuffer("typed before startup completed")
	m.needsSave = true
	m.localVersion++
	m = updateTest(t, m, initDataMsg{err: errors.New("synthetic maintenance failure")})
	if m.buffer.Text() != "typed before startup completed" || !m.needsSave || m.state != stateConfirm {
		t.Fatal("startup error hidden or draft lost")
	}
}
